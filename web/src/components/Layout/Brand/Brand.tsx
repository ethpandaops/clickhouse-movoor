import type { JSX } from 'react';

/**
 * Product brand block: animated logo tile, mono wordmark, and tagline.
 * Purely presentational so it can live inside any link or header container.
 */
export function Brand(): JSX.Element {
  return (
    <span className="flex min-w-0 items-center gap-2.5">
      <img src="/favicon.svg" alt="" className="size-7 shrink-0" />
      <span className="flex min-w-0 flex-col">
        <span className="truncate font-mono text-sm/5 font-semibold tracking-tight text-foreground">
          clickhouse-movoor
        </span>
        <span className="truncate text-[10px]/3 text-muted">ClickHouse partition tiering</span>
      </span>
    </span>
  );
}
