import type { JSX } from 'react';
import clsx from 'clsx';

/** Pause-state pill with a live dot, surfacing the pause reason when present. */
export function RunStatePill({ state, reason }: { state: string; reason?: string }): JSX.Element {
  const running = state === 'running';

  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset',
        running ? 'bg-success/10 text-success ring-success/25' : 'bg-warning/10 text-warning ring-warning/25'
      )}
    >
      <span
        className={clsx('size-1.5 rounded-full', running ? 'bg-success' : 'bg-warning', running && 'animate-pulse')}
      />
      {state}
      {reason ? <span className="text-muted">· {reason}</span> : null}
    </span>
  );
}
