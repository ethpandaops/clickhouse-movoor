import type { JSX } from 'react';
import clsx from 'clsx';
import { CubeIcon } from '@heroicons/react/20/solid';
import type { TablePart } from '@/api/types.gen';
import { formatBytes, formatCount } from '@/utils/format';
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
import { ConditionBadges } from '../ConditionBadges';
import { DiskList } from '../DiskList';
import { Metric } from '../Metric';
import { TreeGuides } from '../TreeGuides';

/** Leaf row: one active data part with its disk, block range, and size. */
export function PartRow({
  part,
  trail,
  targetDisk,
}: {
  part: TablePart;
  trail: boolean[];
  targetDisk?: string;
}): JSX.Element {
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
            <DiskList disks={[part.disk]} targetDisk={targetDisk} />
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
        <DiskList disks={[part.disk]} targetDisk={targetDisk} />
      </div>
      <div className={clsx(colShardClass, 'font-mono text-[11px] text-muted')}>
        {part.minBlockNumber ?? '-'}..{part.maxBlockNumber ?? '-'}
      </div>
      <div className={clsx(colPartitionsClass, 'text-right text-muted')}>{part.partType || '-'}</div>
      <Metric className={colPartsClass} value={part.level ? `L${part.level}` : '-'} format="text" />
      <Metric className={colRowsClass} value={part.rows} />
      <Metric value={formatBytes(part.bytesOnDisk)} format="text" />
      <div className={clsx(colTieringClass, 'text-right text-xs text-muted')}>-</div>
    </div>
  );
}
