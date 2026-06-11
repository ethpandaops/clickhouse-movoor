import type { JSX } from 'react';
import clsx from 'clsx';
import { ServerStackIcon } from '@heroicons/react/20/solid';
import type { TieringPartition } from '@/api/types.gen';
import { formatBytes, formatCount, formatInteger, sumStrings } from '@/utils/format';
import { nodeKey, partitionKey, type NodeGroup } from '../../explorer-model';
import type { TieringIndex } from '../../tiering-model';
import {
  colEngineClass,
  colPartitionsClass,
  colPartsClass,
  colRowsClass,
  colShardClass,
  colTieringClass,
  indentClass,
  mobileMetaClass,
  rowGridClass,
} from '../../row-grid';
import { Badge, type BadgeTone } from '../Badge';
import { ExpandButton } from '../ExpandButton';
import { InlineNotice } from '../InlineNotice';
import { Metric } from '../Metric';
import { PartitionSection } from '../PartitionSection';
import { TieringRollupCell } from '../TieringRollupCell';
import { TreeGuides, TreeTrunkStart } from '../TreeGuides';

export interface NodeSectionProps {
  tableKeyValue: string;
  group: NodeGroup;
  expanded: boolean;
  expandedPartitions: Set<string>;
  tieringIndex: TieringIndex;
  awaitingRefresh: ReadonlyMap<string, string>;
  /** Rejection reasons from failed applies, keyed by partition key. */
  applyErrors: ReadonlyMap<string, string>;
  tieringPaused: boolean;
  flashKey: string | null;
  isLast: boolean;
  onToggleNode: () => void;
  onTogglePartition: (key: string) => void;
  onApplyTiering: (partition: TieringPartition) => void;
  onRetryTiering: (partition: TieringPartition) => void;
}

/** Node row under a table: health, replication lag, and partition children. */
export function NodeSection({
  tableKeyValue,
  group,
  expanded,
  expandedPartitions,
  tieringIndex,
  awaitingRefresh,
  applyErrors,
  tieringPaused,
  flashKey,
  isLast,
  onToggleNode,
  onTogglePartition,
  onApplyTiering,
  onRetryTiering,
}: NodeSectionProps): JSX.Element {
  const activeParts =
    group.state?.activeParts ?? group.partitions.reduce((sum, partition) => sum + partition.parts.length, 0).toString();
  const rows = group.state?.rows ?? sumStrings(group.partitions.map(partition => partition.rows));
  const bytes = group.state?.bytesOnDisk ?? sumStrings(group.partitions.map(partition => partition.bytesOnDisk));
  const healthTone: BadgeTone = group.node?.reachable === false ? 'danger' : group.state ? 'success' : 'warning';
  const healthLabel = group.node?.reachable === false ? 'unreachable' : group.state ? 'observed' : 'missing state';
  const tieringRollup = tieringIndex.byNode.get(nodeKey(tableKeyValue, group.nodeId));

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
        <TieringRollupCell className={colTieringClass} rollup={tieringRollup} />
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
                  tieringPartition={tieringIndex.byPartition.get(partitionKeyValue)}
                  awaitingToken={awaitingRefresh.get(partitionKeyValue)}
                  applyError={applyErrors.get(partitionKeyValue)}
                  tieringPaused={tieringPaused}
                  flash={flashKey === partitionKeyValue}
                  trail={[isLast, index === group.partitions.length - 1]}
                  onTogglePartition={() => onTogglePartition(partitionKeyValue)}
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
