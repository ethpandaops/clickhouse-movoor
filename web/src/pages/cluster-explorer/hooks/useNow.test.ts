import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useNow } from './useNow';

describe('useNow', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-06-08T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns the current time and ticks on the interval', () => {
    const { result } = renderHook(() => useNow(1000));
    const start = result.current;

    act(() => {
      vi.advanceTimersByTime(3000);
    });

    expect(result.current - start).toBe(3000);
  });

  it('cleans up its interval on unmount', () => {
    const clearSpy = vi.spyOn(window, 'clearInterval');
    const { unmount } = renderHook(() => useNow(1000));
    unmount();
    expect(clearSpy).toHaveBeenCalled();
    clearSpy.mockRestore();
  });
});
