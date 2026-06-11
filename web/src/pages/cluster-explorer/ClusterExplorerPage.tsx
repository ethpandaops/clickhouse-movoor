import { useMemo, useRef, useState, type JSX } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import clsx from 'clsx';
import {
  applyTieringPartitionMutation,
  getTieringHistoryOptions,
  getTieringPlanOptions,
  getTieringStatusOptions,
  listNodesOptions,
  listOperationsOptions,
  listTablesOptions,
  pauseTieringMutation,
  resumeTieringMutation,
  retryTieringPartitionMutation,
} from '@/api/@tanstack/react-query.gen';
import type { Node, Operation, TieringHistoryEntry, TieringPartition, TieringPlanResponse } from '@/api/types.gen';
import { ErrorBoundary } from '@/components/Feedback/ErrorBoundary';
import { errorMessage } from '@/utils/format';
import { EmptyState } from './components/EmptyState';
import { TableRowsSkeleton } from './components/ClusterExplorerSkeleton';
import { InlineNotice } from './components/InlineNotice';
import { ProblemBanner } from './components/ProblemBanner';
import { TableSection } from './components/TableSection';
import { TieringSummaryBar, type TieringControlError } from './components/TieringSummaryBar';
import { WarningsBanner } from './components/WarningsBanner';
import {
  compareNodes,
  compareTables,
  nodeKey,
  partitionKey,
  strongestCollection,
  tableKey,
  toggleSetMember,
} from './explorer-model';
import { buildTieringIndex } from './tiering-model';
import {
  colEngineClass,
  colPartitionsClass,
  colPartsClass,
  colRowsClass,
  colShardClass,
  colTieringClass,
  rowGridClass,
} from './row-grid';

interface ClusterExplorerPageProps {
  defaultExpandedTables?: string[];
  defaultExpandedNodes?: string[];
  defaultExpandedPartitions?: string[];
}

const emptyNodes: Node[] = [];
const emptyOperations: Operation[] = [];
const emptyHistory: TieringHistoryEntry[] = [];

function withoutKey(current: ReadonlyMap<string, string>, key: string): ReadonlyMap<string, string> {
  if (!current.has(key)) {
    return current;
  }
  const next = new Map(current);
  next.delete(key);
  return next;
}

interface TieringActionVariables {
  path: { database: string; table: string; partitionId: string };
  query: { nodeId: string };
  body: { stateToken: string };
}

function tieringActionVariables(partition: TieringPartition): TieringActionVariables {
  return {
    path: {
      database: partition.database,
      table: partition.table,
      partitionId: partition.partitionId,
    },
    query: { nodeId: partition.nodeId },
    body: { stateToken: partition.stateToken },
  };
}

/** One-page operator view for watched tables, nodes, partitions, and parts. */
export function ClusterExplorerPage({
  defaultExpandedTables = [],
  defaultExpandedNodes = [],
  defaultExpandedPartitions = [],
}: ClusterExplorerPageProps): JSX.Element {
  const queryClient = useQueryClient();
  const [expandedTables, setExpandedTables] = useState<Set<string>>(() => new Set(defaultExpandedTables));
  const [expandedNodes, setExpandedNodes] = useState<Set<string>>(() => new Set(defaultExpandedNodes));
  const [expandedPartitions, setExpandedPartitions] = useState<Set<string>>(() => new Set(defaultExpandedPartitions));

  // Live-activity drawer: only polls the cluster while open.
  const [activityOpen, setActivityOpen] = useState(false);
  const tablesQuery = useQuery({
    ...listTablesOptions(),
    refetchInterval: 15_000,
  });
  const nodesQuery = useQuery({
    ...listNodesOptions(),
    refetchInterval: 15_000,
  });
  const tieringPlanQuery = useQuery({
    ...getTieringPlanOptions(),
    refetchInterval: 15_000,
    retry: false,
  });
  const tieringStatusQuery = useQuery({
    ...getTieringStatusOptions(),
    // The drawer renders status.inFlight as "active now" — poll at drawer
    // cadence while it is open so legs appear within a beat of dispatch.
    refetchInterval: activityOpen ? 2_500 : 15_000,
    retry: false,
  });
  // Pause/resume failures surface as a dismissible alert in the summary bar.
  const [controlError, setControlError] = useState<TieringControlError | null>(null);
  const pauseMutation = useMutation({
    ...pauseTieringMutation(),
    meta: { errorHandled: true },
    onMutate: () => {
      setControlError(null);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries();
    },
    onError: error => {
      setControlError({ action: 'pause', message: errorMessage(error) });
    },
  });
  const resumeMutation = useMutation({
    ...resumeTieringMutation(),
    meta: { errorHandled: true },
    onMutate: () => {
      setControlError(null);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries();
    },
    onError: error => {
      setControlError({ action: 'resume', message: errorMessage(error) });
    },
  });
  // Deep-link target from the activity drawer: the partition row to scroll to
  // and flash once the tree has expanded around it.
  const [flashKey, setFlashKey] = useState<string | null>(null);
  const flashTimer = useRef<number | undefined>(undefined);
  const jumpToPartition = (database: string, table: string, nodeId: string, partitionId: string): void => {
    const tableKeyValue = `${database}.${table}`;
    const nodeKeyValue = nodeKey(tableKeyValue, nodeId);
    const partitionKeyValue = partitionKey(tableKeyValue, nodeId, partitionId);
    setExpandedTables(current => new Set(current).add(tableKeyValue));
    setExpandedNodes(current => new Set(current).add(nodeKeyValue));
    setFlashKey(partitionKeyValue);
    window.clearTimeout(flashTimer.current);
    flashTimer.current = window.setTimeout(() => setFlashKey(null), 2_600);
  };
  const operationsQuery = useQuery({
    ...listOperationsOptions(),
    enabled: activityOpen,
    refetchInterval: activityOpen ? 2_500 : false,
    retry: false,
  });
  const recentHistoryQuery = useQuery({
    ...getTieringHistoryOptions(),
    enabled: activityOpen,
    refetchInterval: activityOpen ? 5_000 : false,
    retry: false,
  });
  // Partitions whose apply was dispatched, keyed by partition key with the
  // state token that was visible at click time. While the plan still shows
  // that token the row is stale (the leg ran but the refetch has not landed),
  // so its action button stays disabled; a failed apply re-enables it and
  // surfaces the rejection reason next to the button.
  const [awaitingRefresh, setAwaitingRefresh] = useState<ReadonlyMap<string, string>>(new Map());
  const [applyErrors, setApplyErrors] = useState<ReadonlyMap<string, string>>(new Map());
  const applyMutation = useMutation({
    ...applyTieringPartitionMutation(),
    meta: { errorHandled: true },
    onMutate: variables => {
      const key = partitionKey(tableKey(variables.path), variables.query.nodeId, variables.path.partitionId);
      setAwaitingRefresh(current => new Map(current).set(key, variables.body.stateToken));
      setApplyErrors(current => withoutKey(current, key));
    },
    onSuccess: (_data, variables) => {
      const key = partitionKey(tableKey(variables.path), variables.query.nodeId, variables.path.partitionId);
      setApplyErrors(current => withoutKey(current, key));
      void queryClient.invalidateQueries();
    },
    onError: (error, variables) => {
      const key = partitionKey(tableKey(variables.path), variables.query.nodeId, variables.path.partitionId);
      setAwaitingRefresh(current => withoutKey(current, key));
      setApplyErrors(current => new Map(current).set(key, errorMessage(error)));
    },
  });
  const retryMutation = useMutation({
    ...retryTieringPartitionMutation(),
    meta: { errorHandled: true },
    onMutate: variables => {
      const key = partitionKey(tableKey(variables.path), variables.query.nodeId, variables.path.partitionId);
      setAwaitingRefresh(current => new Map(current).set(key, variables.body.stateToken));
      setApplyErrors(current => withoutKey(current, key));
    },
    onSuccess: (_data, variables) => {
      const key = partitionKey(tableKey(variables.path), variables.query.nodeId, variables.path.partitionId);
      setApplyErrors(current => withoutKey(current, key));
      void queryClient.invalidateQueries();
    },
    onError: (error, variables) => {
      const key = partitionKey(tableKey(variables.path), variables.query.nodeId, variables.path.partitionId);
      setAwaitingRefresh(current => withoutKey(current, key));
      setApplyErrors(current => new Map(current).set(key, errorMessage(error)));
    },
  });

  const tables = useMemo(() => [...(tablesQuery.data?.items ?? [])].sort(compareTables), [tablesQuery.data?.items]);
  const nodes = useMemo(() => [...(nodesQuery.data?.items ?? emptyNodes)].sort(compareNodes), [nodesQuery.data?.items]);
  const nodeById = useMemo(() => new Map(nodes.map(node => [node.nodeId, node])), [nodes]);
  const collection = strongestCollection([tablesQuery.data?.collection, nodesQuery.data?.collection]);
  const error = tablesQuery.error ?? nodesQuery.error;
  const tieringPlan = useMemo<TieringPlanResponse>(() => {
    const data = tieringPlanQuery.data;
    // Defend against a malformed / non-array response so the page never crashes
    // on "plan.items is not iterable" — the controller may be unconfigured.
    return {
      tables: Array.isArray(data?.tables) ? data.tables : [],
      items: Array.isArray(data?.items) ? data.items : [],
    };
  }, [tieringPlanQuery.data]);
  const tieringStatus = tieringStatusQuery.data;
  const tieringPaused = tieringStatus != null && tieringStatus.pauseState !== 'running';
  const tieringError = tieringPlanQuery.error ?? tieringStatusQuery.error;
  const tieringIndex = useMemo(() => buildTieringIndex(tieringPlan), [tieringPlan]);

  // Freshness of the collection snapshot: the older of the two reads, plus
  // whether background refetches are currently failing.
  const oldestUpdatedAt = Math.min(tablesQuery.dataUpdatedAt || Infinity, nodesQuery.dataUpdatedAt || Infinity);
  const collectionUpdatedAt = Number.isFinite(oldestUpdatedAt) ? oldestUpdatedAt : undefined;
  const collectionFailing = tablesQuery.isError || nodesQuery.isError;

  const retryCollection = (): void => {
    void tablesQuery.refetch();
    void nodesQuery.refetch();
  };
  const retryTiering = (): void => {
    void tieringPlanQuery.refetch();
    void tieringStatusQuery.refetch();
  };

  const toggleTable = (key: string): void => {
    setExpandedTables(current => toggleSetMember(current, key));
  };

  const toggleNode = (key: string): void => {
    setExpandedNodes(current => toggleSetMember(current, key));
  };

  const togglePartition = (key: string): void => {
    setExpandedPartitions(current => toggleSetMember(current, key));
  };

  return (
    <section className="mx-auto max-w-[1800px] animate-fade-in space-y-4">
      {error && (
        <ProblemBanner title="Couldn't reach the cluster" detail={errorMessage(error)} onRetry={retryCollection} />
      )}
      {collection?.partial && <WarningsBanner collection={collection} />}
      <TieringSummaryBar
        plan={tieringPlan}
        status={tieringStatus}
        watchedTables={tables.length}
        nodeCount={nodes.length}
        collection={collection}
        collectionUpdatedAt={collectionUpdatedAt}
        collectionFailing={collectionFailing}
        activityOpen={activityOpen}
        operations={operationsQuery.data?.items ?? emptyOperations}
        operationsError={activityOpen ? operationsQuery.error : undefined}
        recent={recentHistoryQuery.data?.items ?? emptyHistory}
        historyError={activityOpen ? recentHistoryQuery.error : undefined}
        loading={tieringPlanQuery.isLoading || tieringStatusQuery.isLoading}
        error={tieringError}
        controlError={controlError}
        mutating={
          pauseMutation.isPending || resumeMutation.isPending || applyMutation.isPending || retryMutation.isPending
        }
        onPause={() => pauseMutation.mutate({})}
        onResume={() => resumeMutation.mutate({})}
        onToggleActivity={() => setActivityOpen(open => !open)}
        onJumpToPartition={jumpToPartition}
        onRetryTiering={retryTiering}
        onDismissControlError={() => setControlError(null)}
      />

      <div className="-mx-4 border-y border-border bg-surface sm:-mx-6 md:mx-0 md:rounded-md md:border">
        <div>
          <div
            className={clsx(
              rowGridClass,
              'sticky top-[var(--app-header-height,0px)] z-10 border-b border-border bg-surface px-3 py-2 text-xs text-muted md:rounded-t-md'
            )}
          >
            <div>Table / node / partition / part</div>
            <div className={colEngineClass}>Engine / disk</div>
            <div className={colShardClass}>Shard / replica</div>
            <div className={clsx(colPartitionsClass, 'text-right')}>Partitions</div>
            <div className={clsx(colPartsClass, 'text-right')}>Parts</div>
            <div className={clsx(colRowsClass, 'text-right')}>Rows</div>
            <div className="text-right">Bytes</div>
            <div className={clsx(colTieringClass, 'text-right')}>Tiering</div>
          </div>

          {tablesQuery.isLoading ? (
            <TableRowsSkeleton />
          ) : tables.length === 0 ? (
            <EmptyState />
          ) : (
            tables.map(table => {
              const key = tableKey(table);
              const expanded = expandedTables.has(key);

              return (
                <ErrorBoundary
                  key={key}
                  fallback={(boundaryError, reset) => (
                    <div className="border-b border-border last:border-b-0">
                      <InlineNotice
                        tone="danger"
                        label={`Couldn't render ${key}`}
                        detail={errorMessage(boundaryError)}
                        onRetry={reset}
                      />
                    </div>
                  )}
                >
                  <TableSection
                    table={table}
                    expanded={expanded}
                    expandedNodes={expandedNodes}
                    expandedPartitions={expandedPartitions}
                    nodeById={nodeById}
                    tieringIndex={tieringIndex}
                    awaitingRefresh={awaitingRefresh}
                    applyErrors={applyErrors}
                    tieringPaused={tieringPaused}
                    flashKey={flashKey}
                    onToggleTable={() => toggleTable(key)}
                    onToggleNode={toggleNode}
                    onTogglePartition={togglePartition}
                    onApplyTiering={partition => applyMutation.mutate(tieringActionVariables(partition))}
                    onRetryTiering={partition => retryMutation.mutate(tieringActionVariables(partition))}
                  />
                </ErrorBoundary>
              );
            })
          )}
        </div>
      </div>
    </section>
  );
}
