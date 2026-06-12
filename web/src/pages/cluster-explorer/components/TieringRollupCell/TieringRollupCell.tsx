import type { JSX } from 'react';
import clsx from 'clsx';
import { Tooltip } from '@/components/Overlays/Tooltip';
import { formatCount } from '@/utils/format';
import type { TieringRollup } from '../../tiering-model';
import { fallbackTieringVisual, TieringChip, tieringVisuals } from '../TieringChip';

/**
 * Table/node-level tiering rollup chip. Shows the most pressing bucket
 * (actions > split > hold > tiered) with the full breakdown in a tooltip.
 */
export function TieringRollupCell({
  rollup,
  className,
}: {
  rollup: TieringRollup | undefined;
  className?: string;
}): JSX.Element {
  if (!rollup || rollup.total === 0) {
    return <div className={clsx('text-right text-xs text-muted', className)}>-</div>;
  }

  let visual = fallbackTieringVisual;
  let label = `${rollup.total} checked`;
  if (rollup.actionable > 0) {
    visual = tieringVisuals.actions;
    label = formatCount(rollup.actionable.toString(), 'action');
  } else if (rollup.split > 0) {
    visual = tieringVisuals.split;
    label = `${rollup.split} split`;
  } else if (rollup.held > 0) {
    visual = tieringVisuals.hold;
    label = `${rollup.held} hold`;
  } else if (rollup.tiered > 0) {
    visual = tieringVisuals.tiered;
    label = `${rollup.tiered} tiered`;
  }

  const breakdown = [
    `${rollup.actionable} actionable`,
    `${rollup.tiered} tiered`,
    `${rollup.split} split`,
    `${rollup.held} held`,
  ].join(' · ');

  return (
    <div className={clsx('flex justify-end', className)}>
      <Tooltip content={`${rollup.total} partitions: ${breakdown}`}>
        <TieringChip label={label} visual={visual} />
      </Tooltip>
    </div>
  );
}
