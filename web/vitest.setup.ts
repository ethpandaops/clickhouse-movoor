import '@testing-library/jest-dom';
import { beforeAll, vi } from 'vitest';

function createStorageMock(): Storage {
  let store = new Map<string, string>();

  return {
    get length() {
      return store.size;
    },
    clear() {
      store = new Map<string, string>();
    },
    getItem(key: string) {
      return store.get(key) ?? null;
    },
    key(index: number) {
      return Array.from(store.keys())[index] ?? null;
    },
    removeItem(key: string) {
      store.delete(key);
    },
    setItem(key: string, value: string) {
      store.set(key, String(value));
    },
  };
}

// Suppress console warnings during tests to keep output clean
beforeAll(() => {
  const originalWarn = console.warn;
  vi.spyOn(console, 'warn').mockImplementation((...args) => {
    const message = args[0]?.toString() || '';
    if (message.includes('Failed to parse') || message.includes('Validation error')) {
      return;
    }
    originalWarn(...args);
  });

  // Mock DOM dimensions (jsdom doesn't compute actual dimensions)
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    value: 800,
  });

  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    value: 600,
  });

  // Mock scrollIntoView (not implemented in jsdom)
  Element.prototype.scrollIntoView = vi.fn();

  // Mock ResizeObserver
  const ResizeObserverMock = vi.fn(function (this: ResizeObserver) {
    this.observe = vi.fn();
    this.unobserve = vi.fn();
    this.disconnect = vi.fn();
  });

  vi.stubGlobal('ResizeObserver', ResizeObserverMock);

  if (
    typeof globalThis.localStorage === 'undefined' ||
    typeof globalThis.localStorage.getItem !== 'function' ||
    typeof globalThis.localStorage.setItem !== 'function' ||
    typeof globalThis.localStorage.removeItem !== 'function' ||
    typeof globalThis.localStorage.clear !== 'function'
  ) {
    vi.stubGlobal('localStorage', createStorageMock());
  }
});
