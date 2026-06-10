import { useMemo, useState, type JSX } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import clsx from 'clsx';
import {
  ArrowPathIcon,
  ChevronRightIcon,
  CircleStackIcon,
  CubeIcon,
  ExclamationTriangleIcon,
  ServerStackIcon,
  Squares2X2Icon,
} from '@heroicons/react/20/solid';
import {
  getTableOptions,
  listNodesOptions,
  listTablePartsOptions,
  listTablesOptions,
} from '@/api/@tanstack/react-query.gen';
import type {
  CollectionMeta,
  EmbeddedCondition,
  Node,
  NodeTableState,
  TableDetail,
  TableListItem,
  TablePart,
} from '@/api/types.gen';

interface ClusterExplorerPageProps {
  defaultExpandedTables?: string[];
  defaultExpandedNodes?: string[];
  defaultExpandedPartitions?: string[];
}

interface NodeGroup {
  nodeId: string;
  node?: Node;
  state?: NodeTableState;
  partitions: PartitionGroup[];
}

interface PartitionGroup {
  key: string;
  partition: string;
  partitionId: string;
  disks: string[];
  rows: string;
  bytesOnDisk: string;
  lastModificationTime?: string;
  parts: TablePart[];
}

type BadgeTone = 'danger' | 'info' | 'muted' | 'success' | 'warning';

const badgeToneClass: Record<BadgeTone, string> = {
  danger: 'bg-danger/10 text-danger',
  info: 'bg-primary/10 text-primary',
  muted: 'bg-muted/10 text-muted',
  success: 'bg-success/10 text-success',
  warning: 'bg-warning/10 text-warning',
};

/**
 * Responsive row template shared by every level:
 * - base (<md): identity + bytes; the rest collapses into a stacked meta line
 * - md-xl: four columns (name, engine/disk, rows, bytes)
 * - xl+: all seven columns
 * Cells 2-6 carry the matching visibility classes below, in DOM order.
 */
const rowGridClass =
  'relative grid items-center gap-3 grid-cols-[minmax(0,1fr)_minmax(4.5rem,auto)] md:grid-cols-[minmax(0,1.8fr)_minmax(0,0.8fr)_minmax(4.5rem,0.45fr)_minmax(5.5rem,0.5fr)] xl:grid-cols-[minmax(20rem,1.6fr)_minmax(10rem,0.8fr)_minmax(8rem,0.7fr)_minmax(6.5rem,0.55fr)_minmax(5.5rem,0.5fr)_minmax(6.5rem,0.55fr)_minmax(7rem,0.6fr)]';

const colEngineClass = 'max-md:hidden';
const colShardClass = 'max-xl:hidden';
const colPartitionsClass = 'max-xl:hidden';
const colPartsClass = 'max-xl:hidden';
const colRowsClass = 'max-md:hidden';

/** Stacked stats line shown under a nested row's identity on mobile only. */
const mobileMetaClass = 'mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted md:hidden';

/** Indentation per tree level: tighter on mobile, roomier from md up. */
const indentClass = ['', 'pl-4 md:pl-5', 'pl-8 md:pl-10', 'pl-12 md:pl-16'] as const;

/** Guide column positions per tree depth, aligned to chevron centers at each breakpoint. */
const guideColClass = ['left-[24px]', 'left-[40px] md:left-[44px]', 'left-[56px] md:left-[64px]'] as const;

/** Horizontal elbow widths bridging a guide column to the row's own control. */
const guideTickClass = ['w-1 md:w-2', 'w-1 md:w-2', 'w-1 md:w-3'] as const;

const guideLineClass = 'pointer-events-none absolute bg-border/70';

interface TreeGuidesProps {
  /**
   * isLast flags for the ancestor chain ending at this row, one per depth.
   * ASCII-tree semantics: pass-through trunks render only while an ancestor
   * still has siblings below; the row's own column renders ├ (more siblings)
   * or └ (last child, line stops and turns right).
   */
  trail: boolean[];
}

/** ASCII-style tree guides connecting nested rows to their ancestors. */
function TreeGuides({ trail }: TreeGuidesProps): JSX.Element {
  const own = trail.length - 1;

  return (
    <>
      {trail
        .slice(0, -1)
        .map((ancestorIsLast, depth) =>
          ancestorIsLast ? null : (
            <span key={depth} aria-hidden className={clsx(guideLineClass, 'inset-y-0 w-px', guideColClass[depth])} />
          )
        )}
      <span
        aria-hidden
        className={clsx(guideLineClass, 'top-0 w-px', trail[own] ? 'h-1/2' : 'h-full', guideColClass[own])}
      />
      <span aria-hidden className={clsx(guideLineClass, 'top-1/2 h-px', guideColClass[own], guideTickClass[own])} />
    </>
  );
}

/** Trunk segment dropping from an expanded row's chevron toward its children. */
function TreeTrunkStart({ depth }: { depth: number }): JSX.Element {
  return <span aria-hidden className={clsx(guideLineClass, 'top-1/2 bottom-0 w-px', guideColClass[depth])} />;
}

const numberFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
const integerFormatter = new Intl.NumberFormat();
const naturalCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'short',
  timeStyle: 'short',
});
const emptyNodes: Node[] = [];
const emptyParts: TablePart[] = [];

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

  const tablesQuery = useQuery({
    ...listTablesOptions(),
    refetchInterval: 15_000,
  });
  const nodesQuery = useQuery({
    ...listNodesOptions(),
    refetchInterval: 15_000,
  });

  const tables = useMemo(() => [...(tablesQuery.data?.items ?? [])].sort(compareTables), [tablesQuery.data?.items]);
  const nodes = useMemo(() => [...(nodesQuery.data?.items ?? emptyNodes)].sort(compareNodes), [nodesQuery.data?.items]);
  const nodeById = useMemo(() => new Map(nodes.map(node => [node.nodeId, node])), [nodes]);
  const collection = strongestCollection([tablesQuery.data?.collection, nodesQuery.data?.collection]);
  const isFetching = tablesQuery.isFetching || nodesQuery.isFetching;
  const error = tablesQuery.error ?? nodesQuery.error;

  const toggleTable = (key: string): void => {
    setExpandedTables(current => toggleSetMember(current, key));
  };

  const toggleNode = (key: string): void => {
    setExpandedNodes(current => toggleSetMember(current, key));
  };

  const togglePartition = (key: string): void => {
    setExpandedPartitions(current => toggleSetMember(current, key));
  };

  const refresh = (): void => {
    void queryClient.invalidateQueries();
  };

  return (
    <section className="mx-auto max-w-[1800px] animate-fade-in space-y-4">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold text-foreground">Cluster tables</h1>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
            <span>{tables.length} watched tables</span>
            <span>{nodes.length} configured nodes</span>
            {collection && <CollectionStatus collection={collection} />}
          </div>
        </div>
        <button
          type="button"
          onClick={refresh}
          className="inline-flex h-9 shrink-0 items-center justify-center gap-2 self-start rounded-md border border-border bg-surface px-3 text-sm font-medium text-foreground transition-colors hover:border-primary/60 hover:text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary sm:self-auto"
        >
          <ArrowPathIcon className={clsx('size-4', isFetching && 'animate-spin')} />
          Refresh
        </button>
      </header>

      {error && <ProblemBanner message={errorMessage(error)} />}
      {collection?.partial && <WarningsBanner collection={collection} />}

      <div className="-mx-4 border-y border-border bg-surface sm:-mx-6 md:mx-0 md:rounded-md md:border">
        <div>
          <div
            className={clsx(
              rowGridClass,
              'sticky top-14 z-10 border-b border-border bg-surface px-3 py-2 text-xs text-muted md:rounded-t-md'
            )}
          >
            <div>Table / node / partition / part</div>
            <div className={colEngineClass}>Engine / disk</div>
            <div className={colShardClass}>Shard / replica</div>
            <div className={clsx(colPartitionsClass, 'text-right')}>Partitions</div>
            <div className={clsx(colPartsClass, 'text-right')}>Parts</div>
            <div className={clsx(colRowsClass, 'text-right')}>Rows</div>
            <div className="text-right">Bytes</div>
          </div>

          {tablesQuery.isLoading ? (
            <LoadingRows />
          ) : tables.length === 0 ? (
            <EmptyState />
          ) : (
            tables.map(table => {
              const key = tableKey(table);
              const expanded = expandedTables.has(key);

              return (
                <TableSection
                  key={key}
                  table={table}
                  expanded={expanded}
                  expandedNodes={expandedNodes}
                  expandedPartitions={expandedPartitions}
                  nodeById={nodeById}
                  onToggleTable={() => toggleTable(key)}
                  onToggleNode={toggleNode}
                  onTogglePartition={togglePartition}
                />
              );
            })
          )}
        </div>
      </div>
    </section>
  );
}

interface TableSectionProps {
  table: TableListItem;
  expanded: boolean;
  expandedNodes: Set<string>;
  expandedPartitions: Set<string>;
  nodeById: Map<string, Node>;
  onToggleTable: () => void;
  onToggleNode: (key: string) => void;
  onTogglePartition: (key: string) => void;
}

function TableSection({
  table,
  expanded,
  expandedNodes,
  expandedPartitions,
  nodeById,
  onToggleTable,
  onToggleNode,
  onTogglePartition,
}: TableSectionProps): JSX.Element {
  const key = tableKey(table);
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
      </div>

      {expanded && (
        <div className="animate-fade-in border-t border-border bg-background/25">
          {expandedError ? (
            <InlineNotice tone="danger" label={errorMessage(expandedError)} />
          ) : loadingExpandedData ? (
            <LoadingNodeRows count={nodeById.size} />
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
                  isLast={index === nodeGroups.length - 1}
                  onToggleNode={() => onToggleNode(nodeKeyValue)}
                  onTogglePartition={onTogglePartition}
                />
              );
            })
          )}
        </div>
      )}
    </div>
  );
}

interface NodeSectionProps {
  tableKeyValue: string;
  group: NodeGroup;
  expanded: boolean;
  expandedPartitions: Set<string>;
  isLast: boolean;
  onToggleNode: () => void;
  onTogglePartition: (key: string) => void;
}

function NodeSection({
  tableKeyValue,
  group,
  expanded,
  expandedPartitions,
  isLast,
  onToggleNode,
  onTogglePartition,
}: NodeSectionProps): JSX.Element {
  const activeParts =
    group.state?.activeParts ?? group.partitions.reduce((sum, partition) => sum + partition.parts.length, 0).toString();
  const rows = group.state?.rows ?? sumStrings(group.partitions.map(partition => partition.rows));
  const bytes = group.state?.bytesOnDisk ?? sumStrings(group.partitions.map(partition => partition.bytesOnDisk));
  const healthTone: BadgeTone = group.node?.reachable === false ? 'danger' : group.state ? 'success' : 'warning';
  const healthLabel = group.node?.reachable === false ? 'unreachable' : group.state ? 'observed' : 'missing state';

  return (
    <div className="border-t border-border/60">
      <div
        onClick={onToggleNode}
        className={clsx(rowGridClass, 'cursor-pointer px-3 py-2 text-sm transition-colors hover:bg-surface/60')}
      >
        <TreeGuides trail={[isLast]} />
        {expanded && group.partitions.length > 0 && <TreeTrunkStart depth={1} />}
        <div className={clsx('flex min-w-0 items-center gap-2', indentClass[1])}>
          <ExpandButton
            expanded={expanded}
            label={`${expanded ? 'Collapse' : 'Expand'} node ${group.nodeId}`}
            onClick={onToggleNode}
          />
          <ServerStackIcon className="size-4 shrink-0 text-muted" />
          <div className="min-w-0">
            <div className="truncate font-medium text-foreground" title={group.nodeId}>
              {group.nodeId}
            </div>
            <div className="mt-0.5 truncate text-xs text-muted" title={group.node?.endpoint}>
              {group.node?.endpoint ?? 'node detail unavailable'}
            </div>
            <div className={mobileMetaClass}>
              <Badge tone={healthTone}>{healthLabel}</Badge>
              <Badge tone="muted">
                {group.node?.shard ?? '-'} / {group.node?.replica ?? '-'}
              </Badge>
              <span>{formatCount(group.partitions.length.toString(), 'partition')}</span>
              <span>{formatCount(activeParts, 'part')}</span>
              <span>{formatCount(rows, 'row')}</span>
            </div>
          </div>
          <span className="max-md:hidden">
            <Badge tone={healthTone}>{healthLabel}</Badge>
          </span>
        </div>
        <div className={clsx(colEngineClass, 'min-w-0')}>
          <div className="truncate text-foreground">{group.state?.engine ?? 'not observed'}</div>
          {group.state?.replica && (
            <div className="mt-0.5 text-xs text-muted">
              queue {formatInteger(group.state.replica.queueSize ?? '0')} / lag{' '}
              {formatInteger(group.state.replica.absoluteDelaySeconds ?? '0')}s
            </div>
          )}
        </div>
        <div className={colShardClass}>
          <Badge tone="muted">
            {group.node?.shard ?? '-'} / {group.node?.replica ?? '-'}
          </Badge>
        </div>
        <Metric className={colPartitionsClass} value={group.partitions.length.toString()} />
        <Metric className={colPartsClass} value={activeParts} />
        <Metric className={colRowsClass} value={rows} />
        <Metric value={formatBytes(bytes)} format="text" />
      </div>

      {expanded && (
        <div className="animate-fade-in bg-background/35">
          {group.partitions.length === 0 ? (
            <InlineNotice indent label="No active partitions reported on this node" />
          ) : (
            group.partitions.map((partition, index) => {
              const partitionKeyValue = partitionKey(tableKeyValue, group.nodeId, partition.partitionId);
              const partitionExpanded = expandedPartitions.has(partitionKeyValue);

              return (
                <PartitionSection
                  key={partitionKeyValue}
                  partition={partition}
                  expanded={partitionExpanded}
                  trail={[isLast, index === group.partitions.length - 1]}
                  onTogglePartition={() => onTogglePartition(partitionKeyValue)}
                />
              );
            })
          )}
        </div>
      )}
    </div>
  );
}

interface PartitionSectionProps {
  partition: PartitionGroup;
  expanded: boolean;
  trail: boolean[];
  onTogglePartition: () => void;
}

function PartitionSection({ partition, expanded, trail, onTogglePartition }: PartitionSectionProps): JSX.Element {
  return (
    <div className="border-t border-border/50">
      <div
        onClick={onTogglePartition}
        className={clsx(rowGridClass, 'cursor-pointer px-3 py-2 text-sm transition-colors hover:bg-surface/50')}
      >
        <TreeGuides trail={trail} />
        {expanded && partition.parts.length > 0 && <TreeTrunkStart depth={2} />}
        <div className={clsx('flex min-w-0 items-center gap-2', indentClass[2])}>
          <ExpandButton
            expanded={expanded}
            label={`${expanded ? 'Collapse' : 'Expand'} partition ${partition.partitionId}`}
            onClick={onTogglePartition}
          />
          <Squares2X2Icon className="size-4 shrink-0 text-muted" />
          <div className="min-w-0">
            <div className="truncate font-medium text-foreground" title={partition.partition || partition.partitionId}>
              {partition.partition || partition.partitionId}
            </div>
            <div className="mt-0.5 truncate font-mono text-[11px] text-muted" title={partition.partitionId}>
              {partition.partitionId}
            </div>
            <div className={mobileMetaClass}>
              <DiskList disks={partition.disks} />
              <span>{formatCount(partition.parts.length.toString(), 'part')}</span>
              <span>{formatCount(partition.rows, 'row')}</span>
              <span>{formatTimestamp(partition.lastModificationTime)}</span>
            </div>
          </div>
        </div>
        <div className={clsx(colEngineClass, 'min-w-0')}>
          <DiskList disks={partition.disks} />
        </div>
        <div className={clsx(colShardClass, 'text-xs text-muted')}>
          {formatTimestamp(partition.lastModificationTime)}
        </div>
        <Metric className={colPartitionsClass} value="1" />
        <Metric className={colPartsClass} value={partition.parts.length.toString()} />
        <Metric className={colRowsClass} value={partition.rows} />
        <Metric value={formatBytes(partition.bytesOnDisk)} format="text" />
      </div>

      {expanded && (
        <div className="animate-fade-in bg-background/50">
          {partition.parts.map((part, index) => (
            <PartRow
              key={`${part.nodeId}/${part.partName}/${part.disk}`}
              part={part}
              trail={[...trail, index === partition.parts.length - 1]}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function PartRow({ part, trail }: { part: TablePart; trail: boolean[] }): JSX.Element {
  return (
    <div className={clsx(rowGridClass, 'border-t border-border/40 px-3 py-1.5 text-xs')}>
      <TreeGuides trail={trail} />
      <div className={clsx('flex min-w-0 items-center gap-2', indentClass[3])}>
        <span className="flex size-6 shrink-0 items-center justify-center rounded-md border border-border bg-surface">
          <CubeIcon className="size-3.5 text-muted" />
        </span>
        <div className="min-w-0">
          <div className="truncate font-mono text-foreground" title={part.partName}>
            {part.partName}
          </div>
          <div className="mt-0.5 truncate text-muted" title={part.path}>
            {part.path}
          </div>
          <div className={mobileMetaClass}>
            <DiskList disks={[part.disk]} />
            <span>
              {part.partType || '-'}
              {part.level ? ` · L${part.level}` : ''}
            </span>
            <span>{formatCount(part.rows, 'row')}</span>
          </div>
        </div>
        <ConditionBadges conditions={part.conditions} />
      </div>
      <div className={clsx(colEngineClass, 'min-w-0')}>
        <DiskList disks={[part.disk]} />
      </div>
      <div className={clsx(colShardClass, 'font-mono text-[11px] text-muted')}>
        {part.minBlockNumber ?? '-'}..{part.maxBlockNumber ?? '-'}
      </div>
      <div className={clsx(colPartitionsClass, 'text-right text-muted')}>{part.partType || '-'}</div>
      <Metric className={colPartsClass} value={part.level ? `L${part.level}` : '-'} format="text" />
      <Metric className={colRowsClass} value={part.rows} />
      <Metric value={formatBytes(part.bytesOnDisk)} format="text" />
    </div>
  );
}

function ExpandButton({
  expanded,
  label,
  onClick,
}: {
  expanded: boolean;
  label: string;
  onClick: () => void;
}): JSX.Element {
  return (
    <button
      type="button"
      aria-expanded={expanded}
      aria-label={label}
      title={label}
      onClick={event => {
        event.stopPropagation();
        onClick();
      }}
      className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted transition-colors hover:bg-primary/10 hover:text-primary focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary"
    >
      <ChevronRightIcon className={clsx('size-4 transition-transform duration-150', expanded && 'rotate-90')} />
    </button>
  );
}

function Badge({ tone, children }: { tone: BadgeTone; children: React.ReactNode }): JSX.Element {
  return (
    <span
      className={clsx('inline-flex rounded-md px-2 py-0.5 text-xs font-medium whitespace-nowrap', badgeToneClass[tone])}
    >
      {children}
    </span>
  );
}

function Metric({
  value,
  format = 'integer',
  className,
}: {
  value: string;
  format?: 'integer' | 'text';
  className?: string;
}): JSX.Element {
  const display = format === 'integer' ? formatInteger(value) : value;

  return (
    <div className={clsx('truncate text-right font-mono text-xs text-foreground tabular-nums', className)}>
      {display}
    </div>
  );
}

function DiskList({ disks }: { disks: string[] }): JSX.Element {
  if (disks.length === 0) {
    return <span className="text-xs text-muted">no disk</span>;
  }

  return (
    <div className="flex min-w-0 flex-wrap justify-start gap-1">
      {disks.map(disk => (
        <Badge key={disk} tone={disk.includes('s3') ? 'info' : 'muted'}>
          {disk}
        </Badge>
      ))}
    </div>
  );
}

function ConditionBadges({ conditions }: { conditions: EmbeddedCondition[] }): JSX.Element {
  if (conditions.length === 0) {
    return <></>;
  }

  const strongest = strongestConditionTone(conditions);

  return (
    <Badge tone={strongest}>
      <ExclamationTriangleIcon className="mr-1 size-3" />
      {conditions.length}
    </Badge>
  );
}

function CollectionStatus({ collection }: { collection: CollectionMeta }): JSX.Element {
  return (
    <Badge tone={collection.partial ? 'warning' : 'success'}>
      {collection.nodesResponded}/{collection.nodesExpected} nodes
    </Badge>
  );
}

function WarningsBanner({ collection }: { collection: CollectionMeta }): JSX.Element {
  return (
    <div className="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm text-warning">
      Partial collection: {collection.nodesFailed} node{collection.nodesFailed === 1 ? '' : 's'} failed.
      {collection.warnings.length > 0 && (
        <span className="ml-2 text-xs">
          {collection.warnings.map(warning => `${warning.nodeId ?? 'cluster'}: ${warning.message}`).join(' · ')}
        </span>
      )}
    </div>
  );
}

function ProblemBanner({ message }: { message: string }): JSX.Element {
  return <div className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">{message}</div>;
}

function InlineNotice({
  label,
  tone = 'muted',
  indent = false,
}: {
  label: string;
  tone?: BadgeTone;
  indent?: boolean;
}): JSX.Element {
  return (
    <div
      className={clsx(
        'px-3 py-3 text-sm',
        indent ? 'pl-20' : 'pl-10',
        tone === 'danger' ? 'text-danger' : 'text-muted'
      )}
    >
      {label}
    </div>
  );
}

function LoadingRows(): JSX.Element {
  return (
    <div className="space-y-0">
      {[0, 1, 2].map(row => (
        <div key={row} className={clsx(rowGridClass, 'border-b border-border px-3 py-3 last:border-b-0')}>
          <div className="h-4 max-w-72 animate-pulse rounded-md bg-muted/15" />
          <div className={clsx(colEngineClass, 'h-4 w-32 animate-pulse rounded-md bg-muted/15')} />
          <div className={clsx(colShardClass, 'h-4 w-20 animate-pulse rounded-md bg-muted/15')} />
          <div className={clsx(colPartitionsClass, 'h-4 w-12 animate-pulse justify-self-end rounded-md bg-muted/15')} />
          <div className={clsx(colPartsClass, 'h-4 w-12 animate-pulse justify-self-end rounded-md bg-muted/15')} />
          <div className={clsx(colRowsClass, 'h-4 w-20 animate-pulse justify-self-end rounded-md bg-muted/15')} />
          <div className="h-4 w-16 animate-pulse justify-self-end rounded-md bg-muted/15" />
        </div>
      ))}
    </div>
  );
}

function LoadingNodeRows({ count }: { count: number }): JSX.Element {
  const rows = Array.from({ length: Math.max(count, 2) }, (_, row) => row);

  return (
    <div className="space-y-0">
      {rows.map(row => (
        <div key={row} className={clsx(rowGridClass, 'border-t border-border/60 px-3 py-2.5')}>
          <TreeGuides trail={[row === rows.length - 1]} />
          <div className={clsx('flex min-w-0 items-center gap-2', indentClass[1])}>
            <div className="size-6 shrink-0 animate-pulse rounded-md bg-muted/15" />
            <div className="size-4 shrink-0 animate-pulse rounded-sm bg-muted/15" />
            <div className="min-w-0 space-y-1.5">
              <div className="h-4 w-56 max-w-full animate-pulse rounded-md bg-muted/15" />
              <div className="h-3 w-36 max-w-full animate-pulse rounded-md bg-muted/15" />
            </div>
          </div>
          <div className={clsx(colEngineClass, 'space-y-1.5')}>
            <div className="h-4 w-40 animate-pulse rounded-md bg-muted/15" />
            <div className="h-3 w-28 animate-pulse rounded-md bg-muted/15" />
          </div>
          <div className={clsx(colShardClass, 'h-5 w-28 animate-pulse rounded-md bg-muted/15')} />
          <div className={clsx(colPartitionsClass, 'h-4 w-12 animate-pulse justify-self-end rounded-md bg-muted/15')} />
          <div className={clsx(colPartsClass, 'h-4 w-12 animate-pulse justify-self-end rounded-md bg-muted/15')} />
          <div className={clsx(colRowsClass, 'h-4 w-20 animate-pulse justify-self-end rounded-md bg-muted/15')} />
          <div className="h-4 w-16 animate-pulse justify-self-end rounded-md bg-muted/15" />
        </div>
      ))}
    </div>
  );
}

function EmptyState(): JSX.Element {
  return (
    <div className="flex flex-col items-center gap-2 px-3 py-14 text-center">
      <span className="flex size-10 items-center justify-center rounded-md border border-border bg-background/50">
        <CircleStackIcon className="size-5 text-muted" />
      </span>
      <div className="text-sm font-medium text-foreground">No watched tables</div>
      <div className="max-w-xs text-xs text-muted">
        Tables matching the watch configuration will appear here once the controller observes them.
      </div>
    </div>
  );
}

function buildNodeGroups(
  nodeById: Map<string, Node>,
  detail: TableDetail | undefined,
  parts: TablePart[]
): NodeGroup[] {
  const groups = new Map<string, NodeGroup>();

  for (const node of nodeById.values()) {
    groups.set(node.nodeId, { nodeId: node.nodeId, node, partitions: [] });
  }

  for (const state of detail?.nodes ?? []) {
    const existing = groups.get(state.nodeId);
    groups.set(state.nodeId, {
      nodeId: state.nodeId,
      node: existing?.node,
      state,
      partitions: existing?.partitions ?? [],
    });
  }

  for (const part of parts) {
    const existing = groups.get(part.nodeId);
    groups.set(part.nodeId, {
      nodeId: part.nodeId,
      node: existing?.node ?? {
        nodeId: part.nodeId,
        shard: part.shard,
        replica: part.replica,
        endpoint: '',
        reachable: true,
        observedAt: part.modificationTime,
        lastError: null,
      },
      state: existing?.state,
      partitions: existing?.partitions ?? [],
    });
  }

  for (const group of groups.values()) {
    group.partitions = buildPartitionGroups(group.nodeId, parts);
  }

  return [...groups.values()].sort(compareNodeGroups);
}

function buildPartitionGroups(nodeId: string, parts: TablePart[]): PartitionGroup[] {
  const grouped = new Map<string, TablePart[]>();

  for (const part of parts) {
    if (part.nodeId !== nodeId) {
      continue;
    }

    const current = grouped.get(part.partitionId) ?? [];
    current.push(part);
    grouped.set(part.partitionId, current);
  }

  return [...grouped.entries()]
    .map(([partitionId, partitionParts]) => {
      const sortedParts = [...partitionParts].sort(compareParts);
      const lastModificationTime = sortedParts
        .map(part => part.modificationTime)
        .sort()
        .at(-1);

      return {
        key: partitionId,
        partition: sortedParts[0]?.partition ?? partitionId,
        partitionId,
        disks: [...new Set(sortedParts.map(part => part.disk))].sort(),
        rows: sumStrings(sortedParts.map(part => part.rows)),
        bytesOnDisk: sumStrings(sortedParts.map(part => part.bytesOnDisk)),
        lastModificationTime,
        parts: sortedParts,
      };
    })
    .sort(comparePartitionGroups);
}

function compareTables(a: TableListItem, b: TableListItem): number {
  return naturalCompare(a.database, b.database) || naturalCompare(a.table, b.table);
}

function compareNodes(
  a: Pick<Node, 'nodeId' | 'shard' | 'replica'>,
  b: Pick<Node, 'nodeId' | 'shard' | 'replica'>
): number {
  return naturalCompare(a.shard, b.shard) || naturalCompare(a.replica, b.replica) || naturalCompare(a.nodeId, b.nodeId);
}

function compareNodeGroups(a: NodeGroup, b: NodeGroup): number {
  return compareNodes(
    { nodeId: a.nodeId, shard: a.node?.shard ?? '', replica: a.node?.replica ?? '' },
    { nodeId: b.nodeId, shard: b.node?.shard ?? '', replica: b.node?.replica ?? '' }
  );
}

function comparePartitionGroups(a: PartitionGroup, b: PartitionGroup): number {
  return naturalCompare(a.partition, b.partition) || naturalCompare(a.partitionId, b.partitionId);
}

function compareParts(a: TablePart, b: TablePart): number {
  return (
    compareOptionalBigInt(a.minBlockNumber, b.minBlockNumber) ||
    compareOptionalBigInt(a.maxBlockNumber, b.maxBlockNumber) ||
    compareOptionalBigInt(a.level, b.level) ||
    naturalCompare(a.partName, b.partName) ||
    naturalCompare(a.disk, b.disk) ||
    naturalCompare(a.path, b.path)
  );
}

function compareOptionalBigInt(a: string | undefined, b: string | undefined): number {
  if (a === undefined && b === undefined) {
    return 0;
  }
  if (a === undefined) {
    return 1;
  }
  if (b === undefined) {
    return -1;
  }

  const left = toBigInt(a);
  const right = toBigInt(b);
  if (left < right) {
    return -1;
  }
  if (left > right) {
    return 1;
  }

  return 0;
}

function naturalCompare(a: string, b: string): number {
  return naturalCollator.compare(a, b);
}

function strongestCollection(collections: Array<CollectionMeta | undefined>): CollectionMeta | undefined {
  return collections
    .filter((collection): collection is CollectionMeta => collection !== undefined)
    .sort((a, b) => Number(b.partial) - Number(a.partial))[0];
}

function strongestConditionTone(conditions: EmbeddedCondition[]): BadgeTone {
  if (conditions.some(condition => condition.severity === 'critical')) {
    return 'danger';
  }
  if (conditions.some(condition => condition.severity === 'warning')) {
    return 'warning';
  }

  return 'info';
}

function toggleSetMember(current: Set<string>, key: string): Set<string> {
  const next = new Set(current);
  if (next.has(key)) {
    next.delete(key);
  } else {
    next.add(key);
  }

  return next;
}

function tableKey(table: Pick<TableListItem, 'database' | 'table'>): string {
  return `${table.database}.${table.table}`;
}

function nodeKey(tableKeyValue: string, nodeId: string): string {
  return `${tableKeyValue}/${nodeId}`;
}

function partitionKey(tableKeyValue: string, nodeId: string, partitionId: string): string {
  return `${tableKeyValue}/${nodeId}/${partitionId}`;
}

function sumStrings(values: string[]): string {
  return values.reduce((sum, value) => sum + toBigInt(value), 0n).toString();
}

function toBigInt(value: string | undefined): bigint {
  try {
    return BigInt(value ?? '0');
  } catch {
    return 0n;
  }
}

function formatInteger(value: string): string {
  if (value === '-') {
    return value;
  }

  const integer = toBigInt(value);
  if (integer <= BigInt(Number.MAX_SAFE_INTEGER)) {
    return integerFormatter.format(Number(integer));
  }

  return integer.toString();
}

function formatCount(value: string, noun: string): string {
  return `${formatInteger(value)} ${noun}${value === '1' ? '' : 's'}`;
}

function formatBytes(value: string): string {
  const bytes = Number(toBigInt(value));
  if (!Number.isFinite(bytes)) {
    return `${value} B`;
  }

  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }

  return `${numberFormatter.format(size)} ${units[unit]}`;
}

function formatTimestamp(value: string | undefined): string {
  if (!value) {
    return 'never';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return dateFormatter.format(date);
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }

  if (typeof error === 'object' && error !== null && 'detail' in error && typeof error.detail === 'string') {
    return error.detail;
  }

  return 'Request failed';
}
