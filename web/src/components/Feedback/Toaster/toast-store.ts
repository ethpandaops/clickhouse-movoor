/**
 * Minimal module-level toast store so non-React code (e.g. the global
 * MutationCache backstop) can raise notifications without a context provider.
 * Error toasts persist until dismissed — they are a backstop, not chrome.
 */

export interface ToastMessage {
  id: number;
  title: string;
  detail?: string;
}

let toasts: readonly ToastMessage[] = [];
let nextId = 1;
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) {
    listener();
  }
}

export function pushToast(input: { title: string; detail?: string }): number {
  const toast: ToastMessage = { id: nextId, title: input.title, detail: input.detail };
  nextId += 1;
  toasts = [...toasts, toast];
  emit();
  return toast.id;
}

export function dismissToast(id: number): void {
  const next = toasts.filter(toast => toast.id !== id);
  if (next.length !== toasts.length) {
    toasts = next;
    emit();
  }
}

export function clearToasts(): void {
  if (toasts.length > 0) {
    toasts = [];
    emit();
  }
}

export function getToasts(): readonly ToastMessage[] {
  return toasts;
}

export function subscribeToasts(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
