import type { JSX } from 'react';
import type { CollectionMeta } from '@/api/types.gen';

/** Partial-collection banner listing per-node collection warnings. */
export function WarningsBanner({ collection }: { collection: CollectionMeta }): JSX.Element {
  return (
    <div className="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm text-warning">
      Partial collection: {collection.nodesFailed} node{collection.nodesFailed === 1 ? '' : 's'} failed.
      {collection.warnings.length > 0 && (
        <span className="ml-2 text-xs">
          {collection.warnings.map(warning => `${warning.nodeId ?? 'cluster'}: ${warning.message}`).join(' · ')}
        </span>
      )}
    </div>
  );
}
