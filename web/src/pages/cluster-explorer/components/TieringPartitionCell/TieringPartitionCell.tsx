import type { JSX } from 'react';
import clsx from 'clsx';
import { ExclamationTriangleIcon } from '@heroicons/react/20/solid';
import type { TieringPartition } from '@/api/types.gen';
import { Tooltip } from '@/components/Overlays/Tooltip';
import { formatRelative } from '@/utils/format';
import { isActionableDecision } from '../../tiering-model';
import { TieringChip } from '../TieringChip';
import { fallbackTieringVisual, tieringDisplayLabel, tieringVisuals } from '../TieringChip';

/**
 * Partition-level tiering verdict cell: an apply button for actionable
 * decisions, a retry button for stalled verdicts, otherwise a quiet chip; all
 * explain themselves via tooltip. Failed actions surface in place with a danger
 * marker carrying the rejection reason.
 */
export function TieringPartitionCell({
  partition,
  awaitingToken,
  paused,
  applyError,
  className,
  onApply,
  onRetry,
}: {
  partition: TieringPartition | undefined;
  awaitingToken: string | undefined;
  paused: boolean;
  /** Rejection reason from the last failed apply for this partition. */
  applyError?: string;
  className?: string;
  onApply: (partition: TieringPartition) => void;
  onRetry: (partition: TieringPartition) => void;
}): JSX.Element {
  if (!partition) {
    return <div className={clsx('text-right text-xs text-muted', className)}>-</div>;
  }

  const actionable = isActionableDecision(partition.decision);
  const retryable = partition.status === 'stalled';
  const label = partition.decision === 'none' ? partition.status : partition.decision;
  const visual = tieringVisuals[label] ?? fallbackTieringVisual;

  if (!actionable && !retryable) {
    return (
      <div className={clsx('flex min-w-0 items-center justify-end', className)}>
        <Tooltip content={verdictTooltip(partition)}>
          <TieringChip label={label} visual={visual} />
        </Tooltip>
      </div>
    );
  }

  const title = actionTitle(partition, retryable, paused);
  // Stay disabled from click until the plan row actually advances: while the
  // rendered state token still matches the one captured at click time, this
  // row is stale data from before the leg ran.
  const awaitingRow = awaitingToken !== undefined && awaitingToken === partition.stateToken;
  const buttonLabel = retryable ? 'Retry' : tieringDisplayLabel(label);

  return (
    <div className={clsx('flex min-w-0 items-center justify-end gap-1.5', className)}>
      {applyError !== undefined && (
        <Tooltip content={`Last attempt failed: ${applyError}`}>
          <span role="img" aria-label={`Apply failed: ${applyError}`}>
            <ExclamationTriangleIcon className="size-4 shrink-0 text-danger" />
          </span>
        </Tooltip>
      )}
      <Tooltip
        content={
          <div className="space-y-1">
            <div>{title}</div>
            {applyError !== undefined && <div className="text-danger">last attempt failed: {applyError}</div>}
            {!paused && <div className="text-muted">{partition.reason}</div>}
          </div>
        }
      >
        <button
          type="button"
          aria-label={title}
          disabled={awaitingRow || paused}
          onClick={event => {
            event.stopPropagation();
            if (retryable) {
              onRetry(partition);
            } else {
              onApply(partition);
            }
          }}
          className={clsx(
            'inline-flex h-7 min-w-0 items-center rounded-md bg-primary px-2.5 text-xs font-semibold text-on-primary shadow-sm shadow-primary/30 transition-all hover:shadow-primary/60 hover:brightness-110 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary disabled:cursor-not-allowed disabled:opacity-50 disabled:shadow-none',
            applyError !== undefined && 'ring-2 ring-danger/60'
          )}
        >
          <span className="truncate">{buttonLabel}</span>
        </button>
      </Tooltip>
    </div>
  );
}

function actionTitle(partition: TieringPartition, retryable: boolean, paused: boolean): string {
  if (paused) {
    return retryable ? 'Tiering is paused — resume to retry' : 'Tiering is paused — resume to apply';
  }
  const target = `${partition.database}.${partition.table} ${partition.partition || partition.partitionId}`;
  if (retryable) {
    return `Retry ${target}`;
  }
  return `Apply ${partition.decision} to ${target}`;
}

/** Tooltip body for a partition verdict: reason, gate detail, and conditions. */
function verdictTooltip(partition: TieringPartition): JSX.Element {
  const hold = partition.hold;

  return (
    <div className="space-y-1">
      <div>{partition.reason}</div>
      {hold && (
        <div className="space-y-0.5 text-muted">
          <div>
            gate: <span className="font-mono">{hold.gate}</span>
            {hold.window ? ` · window ${hold.window}` : ''}
          </div>
          {hold.lastInsertAt && <div>last insert {formatRelative(hold.lastInsertAt)}</div>}
          {hold.lastChangeAt && <div>last change {formatRelative(hold.lastChangeAt)}</div>}
          {hold.releasesAt && (
            <div className="text-primary">
              {hold.gate === 'successor-activity'
                ? `releases ${formatRelative(hold.releasesAt)} unless a newer partition gets inserts first`
                : `releases ${formatRelative(hold.releasesAt)}`}
            </div>
          )}
          {hold.retryAt && (
            <div className="text-warning">
              retry {formatRelative(hold.retryAt)}
              {hold.failures ? ` (attempt ${hold.failures + 1})` : ''}
            </div>
          )}
        </div>
      )}
      {partition.conditions.slice(0, 3).map(condition => (
        <div key={condition.code} className="text-muted">
          {condition.severity}: {condition.message}
        </div>
      ))}
    </div>
  );
}
