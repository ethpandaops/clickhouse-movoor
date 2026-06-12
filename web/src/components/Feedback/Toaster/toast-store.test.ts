import { afterEach, describe, expect, it, vi } from 'vitest';
import { clearToasts, dismissToast, getToasts, pushToast, subscribeToasts } from './toast-store';

afterEach(() => {
  clearToasts();
});

describe('toast-store', () => {
  it('pushes toasts with unique ids and notifies subscribers', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToasts(listener);

    const first = pushToast({ title: 'Action failed', detail: 'state token is stale' });
    const second = pushToast({ title: 'Action failed' });

    expect(first).not.toBe(second);
    expect(getToasts().map(toast => toast.title)).toEqual(['Action failed', 'Action failed']);
    expect(listener).toHaveBeenCalledTimes(2);
    unsubscribe();
  });

  it('dismisses by id and ignores unknown ids', () => {
    const id = pushToast({ title: 'Action failed' });
    const listener = vi.fn();
    const unsubscribe = subscribeToasts(listener);

    dismissToast(9999);
    expect(listener).not.toHaveBeenCalled();

    dismissToast(id);
    expect(getToasts()).toHaveLength(0);
    expect(listener).toHaveBeenCalledOnce();
    unsubscribe();
  });

  it('returns a stable snapshot reference between changes', () => {
    pushToast({ title: 'Action failed' });
    expect(getToasts()).toBe(getToasts());
  });

  it('stops notifying after unsubscribe', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToasts(listener);
    unsubscribe();
    pushToast({ title: 'Action failed' });
    expect(listener).not.toHaveBeenCalled();
  });
});
