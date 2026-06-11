import type { JSX } from 'react';
import clsx from 'clsx';
import { Tooltip } from '@/components/Overlays/Tooltip';
import { collectionFreshness, formatAge } from '../../freshness';
import { useNow } from '../../hooks/useNow';

export interface FreshnessIndicatorProps {
  /** Epoch ms of the last successful collection fetch; hidden when absent. */
  updatedAt?: number;
  /** True while background refetches are currently failing. */
  failing?: boolean;
}

/**
 * Live snapshot-age indicator. Separates "down right now" (refresh failing)
 * from "data quietly ageing" (stale/dead), so transient poll errors read
 * differently from an actually dark cluster.
 */
export function FreshnessIndicator({ updatedAt, failing = false }: FreshnessIndicatorProps): JSX.Element | null {
  const now = useNow(1000);

  if (updatedAt === undefined || updatedAt <= 0) {
    return null;
  }

  const age = now - updatedAt;
  const freshness = collectionFreshness(age);
  const tone = failing || freshness === 'dead' ? 'danger' : freshness === 'stale' ? 'warning' : 'muted';
  const label = failing
    ? `refresh failing · last data ${formatAge(age)} ago`
    : freshness === 'dead'
      ? `stale · updated ${formatAge(age)} ago`
      : `updated ${formatAge(age)} ago`;

  return (
    <Tooltip content="Cluster snapshot age — the explorer polls every 15s.">
      <span
        className={clsx(
          'inline-flex items-center gap-1.5 text-xs tabular-nums',
          tone === 'danger' ? 'text-danger' : tone === 'warning' ? 'text-warning' : 'text-muted'
        )}
      >
        <span
          aria-hidden
          className={clsx(
            'size-1.5 rounded-full',
            tone === 'danger' ? 'bg-danger' : tone === 'warning' ? 'bg-warning' : 'bg-success'
          )}
        />
        {label}
      </span>
    </Tooltip>
  );
}
