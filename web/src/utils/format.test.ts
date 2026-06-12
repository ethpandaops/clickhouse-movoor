import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  errorMessage,
  formatBytes,
  formatCount,
  formatInteger,
  formatRelative,
  formatTimeOnly,
  formatTimestamp,
  sumStrings,
  toBigInt,
} from './format';

describe('toBigInt', () => {
  it('parses valid UInt64 strings', () => {
    expect(toBigInt('18446744073709551615')).toBe(18446744073709551615n);
  });

  it('treats undefined and malformed input as zero', () => {
    expect(toBigInt(undefined)).toBe(0n);
    expect(toBigInt('not-a-number')).toBe(0n);
    expect(toBigInt('')).toBe(0n);
  });
});

describe('sumStrings', () => {
  it('sums values beyond Number.MAX_SAFE_INTEGER without precision loss', () => {
    expect(sumStrings(['9007199254740993', '9007199254740993'])).toBe('18014398509481986');
  });

  it('returns zero for an empty list', () => {
    expect(sumStrings([])).toBe('0');
  });
});

describe('formatInteger', () => {
  it('passes the placeholder dash through', () => {
    expect(formatInteger('-')).toBe('-');
  });

  it('groups digits using the runtime locale', () => {
    expect(formatInteger('14400')).toBe(new Intl.NumberFormat().format(14400));
  });

  it('falls back to plain digits past MAX_SAFE_INTEGER', () => {
    expect(formatInteger('18446744073709551615')).toBe('18446744073709551615');
  });
});

describe('formatCount', () => {
  it('pluralises everything except exactly one', () => {
    expect(formatCount('1', 'part')).toBe('1 part');
    expect(formatCount('0', 'part')).toBe('0 parts');
    expect(formatCount('2', 'row')).toBe('2 rows');
  });
});

describe('formatBytes', () => {
  it('picks the right binary unit', () => {
    expect(formatBytes('512')).toBe('512 B');
    expect(formatBytes('1024')).toBe('1 KiB');
    expect(formatBytes('18874368')).toBe('18 MiB');
  });

  it('caps at PiB for absurd sizes', () => {
    expect(formatBytes('1180591620717411303424')).toContain('PiB');
  });
});

describe('formatTimestamp', () => {
  it('renders "never" for missing values and echoes unparseable ones', () => {
    expect(formatTimestamp(undefined)).toBe('never');
    expect(formatTimestamp('')).toBe('never');
    expect(formatTimestamp('not-a-date')).toBe('not-a-date');
  });

  it('formats valid timestamps', () => {
    expect(formatTimestamp('2026-06-08T12:00:00Z')).not.toBe('2026-06-08T12:00:00Z');
  });
});

describe('formatTimeOnly', () => {
  it('echoes unparseable values', () => {
    expect(formatTimeOnly('soon')).toBe('soon');
  });
});

describe('formatRelative', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-06-08T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('scales the unit with distance', () => {
    expect(formatRelative('2026-06-08T12:00:30Z')).toBe(
      new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(30, 'second')
    );
    expect(formatRelative('2026-06-08T11:46:00Z')).toBe(
      new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(-14, 'minute')
    );
    expect(formatRelative('2026-06-08T15:00:00Z')).toBe(
      new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(3, 'hour')
    );
    expect(formatRelative('2026-06-12T12:00:00Z')).toBe(
      new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(4, 'day')
    );
  });

  it('echoes unparseable values', () => {
    expect(formatRelative('whenever')).toBe('whenever');
  });
});

describe('errorMessage', () => {
  it('prefers Error messages', () => {
    expect(errorMessage(new Error('boom'))).toBe('boom');
  });

  it('reads RFC 9457 problem detail', () => {
    expect(errorMessage({ detail: 'no configured ClickHouse node responded' })).toBe(
      'no configured ClickHouse node responded'
    );
  });

  it('falls back for unknown shapes', () => {
    expect(errorMessage(42)).toBe('Request failed');
    expect(errorMessage({ detail: 7 })).toBe('Request failed');
  });
});
