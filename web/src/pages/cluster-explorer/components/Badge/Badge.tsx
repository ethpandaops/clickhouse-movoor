import type { JSX, ReactNode } from 'react';
import clsx from 'clsx';
import { badgeToneClass, type BadgeTone } from './badge-tones';

/** Small tonal status chip used on every explorer row. */
export function Badge({ tone, children }: { tone: BadgeTone; children: ReactNode }): JSX.Element {
  return (
    <span
      className={clsx(
        'inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        badgeToneClass[tone]
      )}
    >
      {children}
    </span>
  );
}
