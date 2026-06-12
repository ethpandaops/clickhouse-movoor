import type { JSX } from 'react';
import clsx from 'clsx';
import type {
  CollectionMeta,
  Operation,
  TieringHistoryEntry,
  TieringPlanResponse,
  TieringStatus,
} from '@/api/types.gen';
import {
  errorMessage,
  formatBytes,
  formatInteger,
  formatRelative,
  formatTimeOnly,
  formatTimestamp,
  toBigInt,
} from '@/utils/format';
import {
  decisionTone,
  isActionableDecision,
  operationTone,
  statusTone,
  tableErrorCount,
  tieringCounts,
} from '../../tiering-model';
import { Badge } from '../Badge';
import { ExpandButton } from '../ExpandButton';
import { FreshnessIndicator } from '../FreshnessIndicator';
import { RunStatePill } from '../RunStatePill';
import { SummaryStat } from '../SummaryStat';
import { fallbackTieringVisual, TieringChip, tieringVisuals } from '../TieringChip';

/** A failed pause/resume attempt, surfaced as a dismissible alert. */
export interface TieringControlError {
  action: 'pause' | 'resume';
  message: string;
}

export interface TieringSummaryBarProps {
  plan: TieringPlanResponse;
  status: TieringStatus | undefined;
  watchedTables: number;
  nodeCount: number;
  collection: CollectionMeta | undefined;
  /** Epoch ms of the last successful collection fetch, for the freshness dot. */
  collectionUpdatedAt?: number;
  /** True while collection refetches are currently failing. */
  collectionFailing?: boolean;
  activityOpen: boolean;
  operations: Operation[];
  /** Failure of the live-operations query — the drawer must not fake calm. */
  operationsError?: unknown;
  recent: TieringHistoryEntry[];
  /** Failure of the history query. */
  historyError?: unknown;
  loading: boolean;
  error: unknown;
  /** Last failed pause/resume attempt; renders a dismissible alert strip. */
  controlError?: TieringControlError | null;
  mutating: boolean;
  onPause: () => void;
  onResume: () => void;
  onToggleActivity: () => void;
  onJumpToPartition: (database: string, table: string, nodeId: string, partitionId: string) => void;
  /** Refetch the tiering plan + status after a controller outage. */
  onRetryTiering?: () => void;
  onDismissControlError?: () => void;
}

/**
 * Controller status strip: mode, pause state, headline stats, status/decision
 * badges, the pause/resume control, and the collapsible live-activity drawer.
 * When the controller is unreachable the bar degrades in place — last-known
 * state stays visible (marked stale) and the pause control survives — rather
 * than collapsing away exactly when an operator needs it.
 */
export function TieringSummaryBar({
  plan,
  status,
  watchedTables,
  nodeCount,
  collection,
  collectionUpdatedAt,
  collectionFailing = false,
  activityOpen,
  operations,
  operationsError,
  recent,
  historyError,
  loading,
  error,
  controlError = null,
  mutating,
  onPause,
  onResume,
  onToggleActivity,
  onJumpToPartition,
  onRetryTiering,
  onDismissControlError,
}: TieringSummaryBarProps): JSX.Element {
  const legs = status?.inFlight ?? [];
  const degraded = error != null;

  const counts = tieringCounts(plan.items);
  const actionable = plan.items.filter(partition => isActionableDecision(partition.decision)).length;
  const lastReconciledAt = [...plan.tables]
    .map(table => table.reconciledAt)
    .sort()
    .at(-1);
  const tableErrors = tableErrorCount(plan);
  const statusEntries = Object.entries(counts.status).filter(([, count]) => count > 0);
  const decisionEntries = Object.entries(counts.decision).filter(([, count]) => count > 0);

  return (
    <section
      className={clsx(
        'overflow-hidden rounded-md border border-l-[3px] border-border bg-surface',
        degraded ? 'border-l-danger' : 'border-l-primary'
      )}
    >
      <div className="flex flex-col gap-3 px-3 py-2.5 xl:flex-row xl:items-center xl:justify-between">
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
          <div className="flex items-center gap-2">
            <ExpandButton
              expanded={activityOpen}
              label={`${activityOpen ? 'Collapse' : 'Expand'} live tiering activity`}
              onClick={onToggleActivity}
            />
            <h2 className="text-sm font-semibold tracking-tight text-foreground">Tiering</h2>
            {status && (
              <Badge tone={status.mode === 'enforce' ? 'warning' : status.mode === 'off' ? 'muted' : 'info'}>
                {status.mode}
              </Badge>
            )}
            {status && <RunStatePill state={status.pauseState} reason={status.pauseReason} />}
            {degraded && <Badge tone="muted">{status ? 'stale' : 'unavailable'}</Badge>}
            {loading && <Badge tone="muted">loading</Badge>}
          </div>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
            <SummaryStat label="tables" value={formatInteger(watchedTables.toString())} />
            <SummaryStat
              label="nodes"
              value={collection ? `${collection.nodesResponded}/${collection.nodesExpected}` : nodeCount.toString()}
              danger={collection?.partial === true}
            />
            <SummaryStat label="partitions" value={formatInteger(plan.items.length.toString())} />
            <SummaryStat label="actions" value={formatInteger(actionable.toString())} accent={actionable > 0} />
            {status && <SummaryStat label="in flight" value={formatBytes(status.bytesInFlight)} />}
            {status && (
              <SummaryStat
                label="moved today"
                value={formatBytes(status.bytesMovedToday)}
                accent={toBigInt(status.bytesMovedToday) > 0n}
              />
            )}
            {lastReconciledAt && <SummaryStat label="reconciled" value={formatTimestamp(lastReconciledAt)} muted />}
            {tableErrors > 0 && <SummaryStat label="table errors" value={tableErrors.toString()} danger />}
            <FreshnessIndicator updatedAt={collectionUpdatedAt} failing={collectionFailing} />
          </div>
        </div>

        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-end">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            {statusEntries.slice(0, 3).map(([key, count]) => (
              <Badge key={`status-${key}`} tone={statusTone(key)}>
                {key} {count}
              </Badge>
            ))}
            {decisionEntries.slice(0, 3).map(([key, count]) => (
              <Badge key={`decision-${key}`} tone={decisionTone(key)}>
                {key} {count}
              </Badge>
            ))}
            {loading && statusEntries.length === 0 && decisionEntries.length === 0 && (
              <Badge tone="muted">loading</Badge>
            )}
            {!loading && !degraded && plan.items.length === 0 && <Badge tone="muted">empty</Badge>}
          </div>
          {status && (
            <button
              type="button"
              onClick={status.pauseState === 'running' ? onPause : onResume}
              disabled={mutating}
              title={
                status.pauseState === 'running'
                  ? 'Pause all tiering writes (dispatch and manual applies)'
                  : 'Resume tiering writes'
              }
              className={clsx(
                'inline-flex h-8 shrink-0 items-center justify-center rounded-md border border-border bg-background px-3 text-xs font-medium text-foreground transition-colors disabled:cursor-not-allowed disabled:opacity-50',
                status.pauseState === 'running'
                  ? 'hover:border-warning/60 hover:bg-warning/10 hover:text-warning'
                  : 'hover:border-success/60 hover:bg-success/10 hover:text-success'
              )}
            >
              {status.pauseState === 'running' ? 'Pause' : 'Resume'}
            </button>
          )}
        </div>
      </div>
      {degraded && (
        <AlertStrip
          title="Tiering controller unavailable"
          detail={errorMessage(error)}
          note={status ? 'Showing last-known controller state.' : undefined}
          actionLabel="Retry"
          onAction={onRetryTiering}
        />
      )}
      {controlError && (
        <AlertStrip
          title={`Couldn't ${controlError.action} tiering`}
          detail={controlError.message}
          actionLabel="Dismiss"
          onAction={onDismissControlError}
        />
      )}
      {activityOpen && (
        <div className="animate-fade-in border-t border-border bg-background/25 px-3 py-2">
          <div className="grid gap-x-8 gap-y-2 lg:grid-cols-2">
            <div className="min-w-0">
              <div className="text-[10px] font-medium tracking-wide text-muted uppercase">active now</div>
              {legs.length > 0 && (
                <ul className="mt-1 space-y-1">
                  {legs.map(leg => (
                    <li key={`${leg.nodeId}/${leg.database}/${leg.table}/${leg.partitionId}`}>
                      <button
                        type="button"
                        title="Jump to this partition below"
                        onClick={() => onJumpToPartition(leg.database, leg.table, leg.nodeId, leg.partitionId)}
                        className="-mx-1 flex w-full min-w-0 cursor-pointer items-center gap-2 rounded-sm px-1 text-left text-xs transition-colors hover:bg-primary/10"
                      >
                        <TieringChip label={leg.action} visual={tieringVisuals[leg.action] ?? fallbackTieringVisual} />
                        <span className="truncate font-mono text-foreground" title={`${leg.database}.${leg.table}`}>
                          {leg.table} {leg.partition}
                        </span>
                        <span className="shrink-0 text-muted">{leg.nodeId}</span>
                        <span className="shrink-0 font-mono text-primary tabular-nums">
                          {formatRelative(leg.startedAt).replace(' ago', '')}
                        </span>
                        {leg.source === 'supervised' && <span className="shrink-0 text-muted">manual</span>}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              {operationsError != null ? (
                <div className="mt-1 text-xs text-danger">
                  {`Couldn't fetch live operations — ${errorMessage(operationsError)}`}
                </div>
              ) : legs.length === 0 && operations.length === 0 ? (
                <div className="mt-1 text-xs text-muted">nothing in flight</div>
              ) : (
                <ul className="mt-1 space-y-1">
                  {operations.slice(0, 8).map(op => (
                    <li key={op.operationId}>
                      <button
                        type="button"
                        disabled={!op.partitionId}
                        title={op.partitionId ? 'Jump to this partition below' : undefined}
                        onClick={() => {
                          if (op.partitionId) {
                            onJumpToPartition(op.database, op.table, op.nodeId, op.partitionId);
                          }
                        }}
                        className="-mx-1 flex w-full min-w-0 items-center gap-2 rounded-sm px-1 text-left text-xs transition-colors enabled:cursor-pointer enabled:hover:bg-primary/10"
                      >
                        <Badge tone={operationTone(op.kind)}>{op.kind}</Badge>
                        <span className="truncate font-mono text-foreground" title={`${op.database}.${op.table}`}>
                          {op.table}
                          {op.partition ? ` ${op.partition}` : ''}
                        </span>
                        <span className="shrink-0 text-muted">{op.nodeId}</span>
                        {typeof op.elapsedSeconds === 'number' && (
                          <span className="shrink-0 font-mono text-muted tabular-nums">
                            {op.elapsedSeconds.toFixed(1)}s
                          </span>
                        )}
                        {typeof op.progress === 'number' && (
                          <span className="shrink-0 font-mono text-primary tabular-nums">
                            {Math.round(op.progress * 100)}%
                          </span>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div className="min-w-0">
              <div className="text-[10px] font-medium tracking-wide text-muted uppercase">recent actions</div>
              {historyError != null ? (
                <div className="mt-1 text-xs text-danger">
                  {`Couldn't fetch recent actions — ${errorMessage(historyError)}`}
                </div>
              ) : recent.length === 0 ? (
                <div className="mt-1 text-xs text-muted">none yet</div>
              ) : (
                <ul className="mt-1 space-y-1">
                  {recent.slice(0, 8).map(entry => (
                    <li key={`${entry.attemptId ?? entry.time}/${entry.partitionId}`}>
                      <button
                        type="button"
                        title="Jump to this partition below"
                        onClick={() => onJumpToPartition(entry.database, entry.table, entry.nodeId, entry.partitionId)}
                        className="-mx-1 flex w-full min-w-0 cursor-pointer items-center gap-2 rounded-sm px-1 text-left text-xs transition-colors hover:bg-primary/10"
                      >
                        <span className="shrink-0 font-mono text-muted tabular-nums">{formatTimeOnly(entry.time)}</span>
                        <TieringChip
                          label={entry.action}
                          visual={tieringVisuals[entry.action] ?? fallbackTieringVisual}
                        />
                        <span className="truncate font-mono text-foreground" title={`${entry.database}.${entry.table}`}>
                          {entry.table} {entry.partition}
                        </span>
                        <span
                          className={clsx('shrink-0', entry.outcome === 'success' ? 'text-success' : 'text-danger')}
                          title={entry.error}
                        >
                          {entry.outcome === 'success' ? '✓' : entry.outcome}
                        </span>
                        <span className="shrink-0 font-mono text-muted tabular-nums">{entry.durationMs}ms</span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

/** Danger alert strip pinned under the summary header: headline, raw detail, one action. */
function AlertStrip({
  title,
  detail,
  note,
  actionLabel,
  onAction,
}: {
  title: string;
  detail?: string;
  note?: string;
  actionLabel: string;
  onAction?: () => void;
}): JSX.Element {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-danger/30 bg-danger/10 px-3 py-2">
      <div className="min-w-0">
        <div className="text-sm font-medium text-danger">{title}</div>
        {detail && <div className="mt-0.5 font-mono text-xs break-words text-danger/80">{detail}</div>}
        {note && <div className="mt-0.5 text-xs text-muted">{note}</div>}
      </div>
      {onAction && (
        <button
          type="button"
          onClick={onAction}
          className="shrink-0 rounded-md border border-danger/40 px-2.5 py-1 text-xs font-medium text-danger transition-colors hover:bg-danger/10"
        >
          {actionLabel}
        </button>
      )}
    </div>
  );
}
