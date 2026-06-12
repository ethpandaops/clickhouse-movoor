import { useEffect, useRef, type JSX } from 'react';
import clsx from 'clsx';
import { Squares2X2Icon } from '@heroicons/react/20/solid';
import type { TieringPartition } from '@/api/types.gen';
import { formatBytes, formatCount, formatTimestamp } from '@/utils/format';
import type { PartitionGroup } from '../../explorer-model';
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
import { DiskList } from '../DiskList';
import { ExpandButton } from '../ExpandButton';
import { Metric } from '../Metric';
import { PartRow } from '../PartRow';
import { TieringPartitionCell } from '../TieringPartitionCell';
import { TreeGuides, TreeTrunkStart } from '../TreeGuides';

export interface PartitionSectionProps {
  partition: PartitionGroup;
  expanded: boolean;
  tieringPartition: TieringPartition | undefined;
  awaitingToken: string | undefined;
  /** A controller leg is currently executing for this partition. */
  inFlight: boolean;
  /** Rejection reason from the last failed apply for this partition. */
  applyError?: string;
  tieringPaused: boolean;
  /** Deep-link landing flash: scrolls the row into view while it animates. */
  flash: boolean;
  trail: boolean[];
  onTogglePartition: () => void;
  onApplyTiering: (partition: TieringPartition) => void;
  onRetryTiering: (partition: TieringPartition) => void;
}

/** Partition row with its tiering verdict, expanding into per-part leaf rows. */
export function PartitionSection({
  partition,
  expanded,
  tieringPartition,
  awaitingToken,
  inFlight,
  applyError,
  tieringPaused,
  flash,
  trail,
  onTogglePartition,
  onApplyTiering,
  onRetryTiering,
}: PartitionSectionProps): JSX.Element {
  const rowRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    // Deep-link landing: the drawer expanded our ancestors and named us — pull
    // the row into view while the flash animation runs.
    if (flash) {
      rowRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }, [flash]);

  return (
    <div className="border-t border-border/50">
      <div
        ref={rowRef}
        onClick={onTogglePartition}
        className={clsx(
          rowGridClass,
          'cursor-pointer px-3 py-2 text-sm transition-colors hover:bg-surface/50',
          flash && 'animate-row-flash'
        )}
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
              <DiskList disks={partition.disks} targetDisk={tieringPartition?.targetDisk} />
              <span>{formatCount(partition.parts.length.toString(), 'part')}</span>
              <span>{formatCount(partition.rows, 'row')}</span>
              <span>{formatTimestamp(partition.lastModificationTime)}</span>
            </div>
          </div>
        </div>
        <div className={clsx(colEngineClass, 'min-w-0')}>
          <DiskList disks={partition.disks} targetDisk={tieringPartition?.targetDisk} />
        </div>
        <div className={clsx(colShardClass, 'text-xs text-muted')}>
          {formatTimestamp(partition.lastModificationTime)}
        </div>
        <Metric className={colPartitionsClass} value="1" />
        <Metric className={colPartsClass} value={partition.parts.length.toString()} />
        <Metric className={colRowsClass} value={partition.rows} />
        <Metric value={formatBytes(partition.bytesOnDisk)} format="text" />
        <TieringPartitionCell
          awaitingToken={awaitingToken}
          inFlight={inFlight}
          applyError={applyError}
          className={colTieringClass}
          partition={tieringPartition}
          paused={tieringPaused}
          onApply={onApplyTiering}
          onRetry={onRetryTiering}
        />
      </div>

      {expanded && (
        <div className="animate-fade-in bg-background/50">
          {partition.parts.map((part, index) => (
            <PartRow
              key={`${part.nodeId}/${part.partName}/${part.disk}`}
              part={part}
              targetDisk={tieringPartition?.targetDisk}
              trail={[...trail, index === partition.parts.length - 1]}
            />
          ))}
        </div>
      )}
    </div>
  );
}
