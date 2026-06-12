import type { JSX } from 'react';
import clsx from 'clsx';
import {
  colEngineClass,
  colPartitionsClass,
  colPartsClass,
  colRowsClass,
  colShardClass,
  colTieringClass,
  indentClass,
  rowGridClass,
} from '../../row-grid';
import { TreeGuides } from '../TreeGuides';

/** Shimmer rows matching the table-level grid while the table list loads. */
export function TableRowsSkeleton(): JSX.Element {
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
          <div className={clsx(colTieringClass, 'h-6 w-20 animate-pulse justify-self-end rounded-md bg-muted/15')} />
        </div>
      ))}
    </div>
  );
}

/** Shimmer node rows shown while an expanded table's detail and parts load. */
export function NodeRowsSkeleton({ count }: { count: number }): JSX.Element {
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
          <div className={clsx(colTieringClass, 'h-6 w-20 animate-pulse justify-self-end rounded-md bg-muted/15')} />
        </div>
      ))}
    </div>
  );
}
