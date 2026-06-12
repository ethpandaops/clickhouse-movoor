import type { ComponentType } from 'react';
import {
  ArrowDownTrayIcon,
  ArrowsRightLeftIcon,
  ArrowUpIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  MinusIcon,
  PauseCircleIcon,
  PencilSquareIcon,
} from '@heroicons/react/20/solid';
import { operationTone } from '../../tiering-model';
import type { BadgeTone } from '../Badge';
import { ActionsIcon, MergeIcon, MoveIcon, OptimizeIcon } from './action-icons';

type TieringIcon = ComponentType<{ className?: string }>;

export interface TieringVisual {
  tone: BadgeTone;
  icon: TieringIcon;
  quiet: boolean;
}

/**
 * State → visual, with a deliberate hierarchy. UX research on table status:
 * actionable statuses must out-shout passive ones, and shape/icon must carry
 * meaning alongside colour (not a "pixelated rainbow"). loud = filled chip
 * (needs action or attention); quiet = icon + label that recedes (done /
 * waiting / passive), so a mostly-tiered table reads calm.
 */
export const tieringVisuals: Record<string, TieringVisual> = {
  consolidate: { tone: 'warning', icon: ArrowsRightLeftIcon, quiet: false },
  optimize: { tone: 'warning', icon: OptimizeIcon, quiet: false },
  tier: { tone: 'warning', icon: MoveIcon, quiet: false },
  append: { tone: 'warning', icon: ArrowUpIcon, quiet: false },
  split: { tone: 'warning', icon: ArrowsRightLeftIcon, quiet: false },
  stalled: { tone: 'warning', icon: ExclamationTriangleIcon, quiet: false },
  misconfigured: { tone: 'danger', icon: ExclamationTriangleIcon, quiet: false },
  tiered: { tone: 'success', icon: CheckCircleIcon, quiet: true },
  ready: { tone: 'success', icon: ArrowUpIcon, quiet: true },
  hold: { tone: 'muted', icon: PauseCircleIcon, quiet: true },
  keep: { tone: 'muted', icon: MinusIcon, quiet: true },
  hot: { tone: 'muted', icon: MinusIcon, quiet: true },
  none: { tone: 'muted', icon: MinusIcon, quiet: true },
  // Not a decision/status: the "N actions" rollup and the actions summary stat.
  actions: { tone: 'warning', icon: ActionsIcon, quiet: false },
};

export const fallbackTieringVisual: TieringVisual = { tone: 'muted', icon: MinusIcon, quiet: true };

const operationIcons: Record<string, TieringIcon> = {
  merge: MergeIcon,
  move: MoveIcon,
  mutation: PencilSquareIcon,
  fetch: ArrowDownTrayIcon,
};

/** Live cluster operation kind → loud chip visual; tone stays with operationTone. */
export function operationVisual(kind: string): TieringVisual {
  return { tone: operationTone(kind), icon: operationIcons[kind] ?? MinusIcon, quiet: false };
}

export const quietToneTextClass: Record<BadgeTone, string> = {
  danger: 'text-danger',
  info: 'text-primary',
  muted: 'text-muted',
  success: 'text-success',
  warning: 'text-warning',
};

/**
 * UI display label for a decision/status. The API vocabulary stays canonical
 * ("tier" = the move-to-target leg); the UI shows the plainer verb.
 */
export function tieringDisplayLabel(label: string): string {
  return label === 'tier' ? 'move' : label;
}
