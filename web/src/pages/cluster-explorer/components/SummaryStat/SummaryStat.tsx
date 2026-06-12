import type { ComponentType, JSX } from 'react';
import clsx from 'clsx';

/** Compact label/value stat used in the tiering summary strip. */
export function SummaryStat({
  label,
  value,
  icon: Icon,
  accent = false,
  muted = false,
  danger = false,
}: {
  label: string;
  value: string;
  /** Optional glyph rendered before the value, in the value's color. */
  icon?: ComponentType<{ className?: string }>;
  accent?: boolean;
  muted?: boolean;
  danger?: boolean;
}): JSX.Element {
  return (
    <div className="flex flex-col">
      <span
        className={clsx(
          'flex items-center gap-1 font-mono text-sm font-semibold tabular-nums',
          danger ? 'text-danger' : accent ? 'text-primary' : muted ? 'text-muted' : 'text-foreground'
        )}
      >
        {Icon && <Icon className="size-3.5 shrink-0" />}
        {value}
      </span>
      <span className="text-[10px] font-medium tracking-wide text-muted uppercase">{label}</span>
    </div>
  );
}
