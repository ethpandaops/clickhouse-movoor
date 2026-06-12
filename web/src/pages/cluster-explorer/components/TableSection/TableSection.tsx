import { useMemo, type JSX } from 'react';
import { useQuery } from '@tanstack/react-query';
import clsx from 'clsx';
import { CircleStackIcon } from '@heroicons/react/20/solid';
import { getTableOptions, listTablePartsOptions } from '@/api/@tanstack/react-query.gen';
import type { Node, TableListItem, TieringPartition } from '@/api/types.gen';
import { errorMessage, formatBytes } from '@/utils/format';
import { buildNodeGroups, nodeKey, tableKey } from '../../explorer-model';
import type { TieringIndex } from '../../tiering-model';
import {
  colEngineClass,
  colPartitionsClass,
  colPartsClass,
  colRowsClass,
  colShardClass,
  colTieringClass,
  rowGridClass,
} from '../../row-grid';
import { Badge } from '../Badge';
import { ConditionBadges } from '../ConditionBadges';
import { NodeRowsSkeleton } from '../ClusterExplorerSkeleton';
import { ExpandButton } from '../ExpandButton';
import { InlineNotice } from '../InlineNotice';
import { Metric } from '../Metric';
import { NodeSection } from '../NodeSection';
import { TieringRollupCell } from '../TieringRollupCell';
import { TreeTrunkStart } from '../TreeGuides';

const emptyParts: never[] = [];

export interface TableSectionProps {
  table: TableListItem;
  expanded: boolean;
  expandedNodes: Set<string>;
  expandedPartitions: Set<string>;
  nodeById: Map<string, Node>;
  tieringIndex: TieringIndex;
  awaitingRefresh: ReadonlyMap<string, string>;
  /** Partition keys with a controller leg currently executing. */
  inFlightKeys: ReadonlySet<string>;
  /** Rejection reasons from failed applies, keyed by partition key. */
  applyErrors: ReadonlyMap<string, string>;
  tieringPaused: boolean;
  flashKey: string | null;
  onToggleTable: () => void;
  onToggleNode: (key: string) => void;
  onTogglePartition: (key: string) => void;
  onApplyTiering: (partition: TieringPartition) => void;
  onRetryTiering: (partition: TieringPartition) => void;
}

/**
 * Top-level table row. Owns the detail + parts queries for its subtree,
 * fetching only while expanded.
 */
export function TableSection({
  table,
  expanded,
  expandedNodes,
  expandedPartitions,
  nodeById,
  tieringIndex,
  awaitingRefresh,
  inFlightKeys,
  applyErrors,
  tieringPaused,
  flashKey,
  onToggleTable,
  onToggleNode,
  onTogglePartition,
  onApplyTiering,
  onRetryTiering,
}: TableSectionProps): JSX.Element {
  const key = tableKey(table);
  const tieringRollup = tieringIndex.byTable.get(key);
  const path = { database: table.database, table: table.table };
  const detailQuery = useQuery({
    ...getTableOptions({ path }),
    enabled: expanded,
    refetchInterval: expanded ? 15_000 : false,
  });
  const partsQuery = useQuery({
    ...listTablePartsOptions({ path, query: { active: true } }),
    enabled: expanded,
    refetchInterval: expanded ? 15_000 : false,
  });

  const detail = detailQuery.data?.item;
  const parts = partsQuery.data?.items ?? emptyParts;
  const nodeGroups = useMemo(() => buildNodeGroups(nodeById, detail, parts), [detail, nodeById, parts]);
  const loadingExpandedData = expanded && (detailQuery.isLoading || partsQuery.isLoading);
  const expandedError = detailQuery.error ?? partsQuery.error;

  return (
    <div className="border-b border-border last:border-b-0">
      <div
        onClick={onToggleTable}
        className={clsx(rowGridClass, 'cursor-pointer px-3 py-2.5 text-sm transition-colors hover:bg-background/35')}
      >
        {expanded && !expandedError && (loadingExpandedData || nodeGroups.length > 0) && <TreeTrunkStart depth={0} />}
        <div className="flex min-w-0 items-center gap-2">
          <ExpandButton
            expanded={expanded}
            label={`${expanded ? 'Collapse' : 'Expand'} ${table.database}.${table.table}`}
            onClick={onToggleTable}
          />
          <CircleStackIcon className="size-4 shrink-0 text-primary" />
          <div className="min-w-0">
            <div className="truncate font-semibold text-foreground" title={`${table.database}.${table.table}`}>
              <span className="font-normal text-muted">{table.database}.</span>
              {table.table}
            </div>
            <div className="mt-0.5 truncate font-mono text-[11px] text-muted" title={table.partitionKey}>
              {table.partitionKey || 'no partition key'}
            </div>
          </div>
          <ConditionBadges conditions={table.conditions} />
        </div>
        <div className={clsx(colEngineClass, 'min-w-0')}>
          <div className="truncate text-foreground" title={table.engine}>
            {table.engine}
          </div>
          <div className="mt-0.5 truncate text-xs text-muted">{table.storagePolicy || 'no policy'}</div>
        </div>
        <div className={colShardClass}>
          <Badge tone="info">
            {table.shardsObserved}x{table.replicasPerShard}
          </Badge>
          <div className="mt-1 text-xs text-muted">{table.nodesObserved} observed</div>
        </div>
        <Metric className={colPartitionsClass} value={table.activePartitions.toString()} />
        <Metric className={colPartsClass} value={table.activeParts} />
        <Metric className={colRowsClass} value={table.rows} />
        <Metric value={formatBytes(table.bytesOnDisk)} format="text" />
        <TieringRollupCell className={colTieringClass} rollup={tieringRollup} />
      </div>

      {expanded && (
        <div className="animate-fade-in border-t border-border bg-background/25">
          {expandedError ? (
            <InlineNotice
              tone="danger"
              label="Couldn't load this table's node data"
              detail={errorMessage(expandedError)}
              onRetry={() => {
                void detailQuery.refetch();
                void partsQuery.refetch();
              }}
            />
          ) : loadingExpandedData ? (
            <NodeRowsSkeleton count={nodeById.size} />
          ) : nodeGroups.length === 0 ? (
            <InlineNotice label="No node state reported for this table" />
          ) : (
            nodeGroups.map((group, index) => {
              const nodeKeyValue = nodeKey(key, group.nodeId);
              const nodeExpanded = expandedNodes.has(nodeKeyValue);

              return (
                <NodeSection
                  key={nodeKeyValue}
                  tableKeyValue={key}
                  group={group}
                  expanded={nodeExpanded}
                  expandedPartitions={expandedPartitions}
                  tieringIndex={tieringIndex}
                  awaitingRefresh={awaitingRefresh}
                  inFlightKeys={inFlightKeys}
                  applyErrors={applyErrors}
                  tieringPaused={tieringPaused}
                  flashKey={flashKey}
                  isLast={index === nodeGroups.length - 1}
                  onToggleNode={() => onToggleNode(nodeKeyValue)}
                  onTogglePartition={onTogglePartition}
                  onApplyTiering={onApplyTiering}
                  onRetryTiering={onRetryTiering}
                />
              );
            })
          )}
        </div>
      )}
    </div>
  );
}
