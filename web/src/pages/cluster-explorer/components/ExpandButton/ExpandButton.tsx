import type { JSX } from 'react';
import clsx from 'clsx';
import { ChevronRightIcon } from '@heroicons/react/20/solid';

/**
 * Chevron toggle for tree rows. Stops propagation so it can sit inside rows
 * that are themselves clickable.
 */
export function ExpandButton({
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
