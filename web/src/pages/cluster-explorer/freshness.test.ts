import { describe, expect, it } from 'vitest';
import { collectionFreshness, formatAge } from './freshness';

describe('collectionFreshness', () => {
  it('is fresh inside ~2.5 poll intervals', () => {
    expect(collectionFreshness(0)).toBe('fresh');
    expect(collectionFreshness(39_999)).toBe('fresh');
  });

  it('is stale between 40s and 2m', () => {
    expect(collectionFreshness(40_000)).toBe('stale');
    expect(collectionFreshness(119_999)).toBe('stale');
  });

  it('is dead from 2m', () => {
    expect(collectionFreshness(120_000)).toBe('dead');
    expect(collectionFreshness(3_600_000)).toBe('dead');
  });
});

describe('formatAge', () => {
  it('scales units with age and never goes negative', () => {
    expect(formatAge(-500)).toBe('0s');
    expect(formatAge(8_000)).toBe('8s');
    expect(formatAge(59_999)).toBe('59s');
    expect(formatAge(60_000)).toBe('1m');
    expect(formatAge(150_000)).toBe('2m');
    expect(formatAge(3_600_000)).toBe('1h');
    expect(formatAge(7_500_000)).toBe('2h');
  });
});
