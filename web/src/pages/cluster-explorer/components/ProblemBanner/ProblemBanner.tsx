import type { JSX } from 'react';

/**
 * Page-level error banner. Leads with a plain-language headline; the raw
 * backend detail stays visible for operators, with an optional Retry.
 */
export function ProblemBanner({
  title,
  detail,
  onRetry,
}: {
  title: string;
  detail?: string;
  onRetry?: () => void;
}): JSX.Element {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-md border border-danger/30 bg-danger/10 px-3 py-2">
      <div className="min-w-0">
        <div className="text-sm font-medium text-danger">{title}</div>
        {detail && <div className="mt-0.5 font-mono text-xs break-words text-danger/80">{detail}</div>}
      </div>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="shrink-0 rounded-md border border-danger/40 px-2.5 py-1 text-xs font-medium text-danger transition-colors hover:bg-danger/10"
        >
          Retry
        </button>
      )}
    </div>
  );
}
