import type { JSX } from 'react';
import clsx from 'clsx';
import { badgeToneClass } from '../Badge';
import { quietToneTextClass, tieringDisplayLabel, type TieringVisual } from './tiering-visuals';

/** A tiering-state chip: loud (filled) for action/attention, quiet for passive. */
export function TieringChip({ label, visual }: { label: string; visual: TieringVisual }): JSX.Element {
  const Icon = visual.icon;

  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1 text-xs font-medium',
        visual.quiet ? quietToneTextClass[visual.tone] : clsx('rounded-md px-1.5 py-0.5', badgeToneClass[visual.tone])
      )}
    >
      <Icon className="size-3.5 shrink-0" />
      {tieringDisplayLabel(label)}
    </span>
  );
}
