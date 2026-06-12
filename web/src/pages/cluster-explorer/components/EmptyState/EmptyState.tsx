import type { JSX } from 'react';
import { CircleStackIcon } from '@heroicons/react/20/solid';

/** Zero-tables state: nothing matches the watch configuration yet. */
export function EmptyState(): JSX.Element {
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
