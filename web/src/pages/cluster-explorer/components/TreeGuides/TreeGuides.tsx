import type { JSX } from 'react';
import clsx from 'clsx';

/** Guide column positions per tree depth, aligned to chevron centers at each breakpoint. */
const guideColClass = ['left-[24px]', 'left-[40px] md:left-[44px]', 'left-[56px] md:left-[64px]'] as const;

/** Horizontal elbow widths bridging a guide column to the row's own control. */
const guideTickClass = ['w-1 md:w-2', 'w-1 md:w-2', 'w-1 md:w-3'] as const;

const guideLineClass = 'pointer-events-none absolute bg-border/70';

export interface TreeGuidesProps {
  /**
   * isLast flags for the ancestor chain ending at this row, one per depth.
   * ASCII-tree semantics: pass-through trunks render only while an ancestor
   * still has siblings below; the row's own column renders ├ (more siblings)
   * or └ (last child, line stops and turns right).
   */
  trail: boolean[];
}

/** ASCII-style tree guides connecting nested rows to their ancestors. */
export function TreeGuides({ trail }: TreeGuidesProps): JSX.Element {
  const own = trail.length - 1;

  return (
    <>
      {trail
        .slice(0, -1)
        .map((ancestorIsLast, depth) =>
          ancestorIsLast ? null : (
            <span key={depth} aria-hidden className={clsx(guideLineClass, 'inset-y-0 w-px', guideColClass[depth])} />
          )
        )}
      <span
        aria-hidden
        className={clsx(guideLineClass, 'top-0 w-px', trail[own] ? 'h-1/2' : 'h-full', guideColClass[own])}
      />
      <span aria-hidden className={clsx(guideLineClass, 'top-1/2 h-px', guideColClass[own], guideTickClass[own])} />
    </>
  );
}

/** Trunk segment dropping from an expanded row's chevron toward its children. */
export function TreeTrunkStart({ depth }: { depth: number }): JSX.Element {
  return <span aria-hidden className={clsx(guideLineClass, 'top-1/2 bottom-0 w-px', guideColClass[depth])} />;
}
