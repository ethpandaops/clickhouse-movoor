/** Collection snapshot freshness, keyed to the explorer's 15s poll cadence. */
export type CollectionFreshness = 'fresh' | 'stale' | 'dead';

export const COLLECTION_POLL_MS = 15_000;

/** Fresh under ~2.5 polls, stale under ~8, dead beyond that. */
export function collectionFreshness(ageMs: number): CollectionFreshness {
  if (ageMs < 40_000) {
    return 'fresh';
  }
  if (ageMs < 120_000) {
    return 'stale';
  }
  return 'dead';
}

/** Compact age display: "8s", "2m", "1h". Never negative. */
export function formatAge(ageMs: number): string {
  const seconds = Math.max(0, Math.floor(ageMs / 1000));
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m`;
  }
  return `${Math.floor(minutes / 60)}h`;
}
