import type { JSX } from 'react';
import clsx from 'clsx';
import type { BadgeTone } from '../Badge';

/** Quiet inline message for empty/error states inside an expanded tree level. */
export function InlineNotice({
  label,
  detail,
  tone = 'muted',
  indent = false,
  onRetry,
}: {
  label: string;
  /** Secondary line, e.g. the raw backend error under a plain-language headline. */
  detail?: string;
  tone?: BadgeTone;
  indent?: boolean;
  /** Renders a Retry button wired to this callback. */
  onRetry?: () => void;
}): JSX.Element {
  return (
    <div className={clsx('px-3 py-3 text-sm', indent ? 'pl-20' : 'pl-10')}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className={tone === 'danger' ? 'font-medium text-danger' : 'text-muted'}>{label}</span>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            className={clsx(
              'rounded-md border px-2 py-0.5 text-xs font-medium transition-colors',
              tone === 'danger'
                ? 'border-danger/40 text-danger hover:bg-danger/10'
                : 'border-border text-muted hover:bg-primary/10 hover:text-primary'
            )}
          >
            Retry
          </button>
        )}
      </div>
      {detail && <div className="mt-0.5 font-mono text-xs break-words text-muted">{detail}</div>}
    </div>
  );
}
