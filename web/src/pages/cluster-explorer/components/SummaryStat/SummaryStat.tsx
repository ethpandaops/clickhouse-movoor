import type { JSX } from 'react';
import clsx from 'clsx';

/** Compact label/value stat used in the tiering summary strip. */
export function SummaryStat({
  label,
  value,
  accent = false,
  muted = false,
  danger = false,
}: {
  label: string;
  value: string;
  accent?: boolean;
  muted?: boolean;
  danger?: boolean;
}): JSX.Element {
  return (
    <div className="flex flex-col">
      <span
        className={clsx(
          'font-mono text-sm font-semibold tabular-nums',
          danger ? 'text-danger' : accent ? 'text-primary' : muted ? 'text-muted' : 'text-foreground'
        )}
      >
        {value}
      </span>
      <span className="text-[10px] font-medium tracking-wide text-muted uppercase">{label}</span>
    </div>
  );
}
