import { useSyncExternalStore, type JSX } from 'react';
import { XMarkIcon } from '@heroicons/react/20/solid';
import { dismissToast, getToasts, subscribeToasts } from './toast-store';

/**
 * Renders the toast-store stack bottom-right. This is the backstop channel
 * for failures with no inline home (e.g. unhandled mutation errors) — toasts
 * here persist until dismissed so an operator cannot miss them.
 */
export function Toaster(): JSX.Element | null {
  const toasts = useSyncExternalStore(subscribeToasts, getToasts);

  if (toasts.length === 0) {
    return null;
  }

  return (
    <div className="fixed right-4 bottom-4 z-50 flex w-full max-w-sm flex-col gap-2">
      {toasts.map(toast => (
        <div
          key={toast.id}
          role="alert"
          className="flex items-start gap-2 rounded-md border border-danger/30 bg-surface px-3 py-2 shadow-lg"
        >
          <div className="min-w-0 grow">
            <div className="text-sm font-medium text-danger">{toast.title}</div>
            {toast.detail && <div className="mt-0.5 text-xs break-words text-muted">{toast.detail}</div>}
          </div>
          <button
            type="button"
            aria-label="Dismiss notification"
            onClick={() => dismissToast(toast.id)}
            className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted transition-colors hover:bg-danger/10 hover:text-danger focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary"
          >
            <XMarkIcon className="size-4" />
          </button>
        </div>
      ))}
    </div>
  );
}
