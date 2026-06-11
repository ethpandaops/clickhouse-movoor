import type { JSX } from 'react';
import clsx from 'clsx';
import { formatInteger } from '@/utils/format';

/** Right-aligned tabular figure cell; integers get locale grouping. */
export function Metric({
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
