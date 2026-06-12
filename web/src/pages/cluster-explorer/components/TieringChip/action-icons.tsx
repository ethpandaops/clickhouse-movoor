import type { JSX } from 'react';

/**
 * Animated glyphs for the loud tiering actions. Both draw in currentColor so
 * the chip tone applies, loop subtly so in-flight work reads alive, and hold
 * a static legible shape under prefers-reduced-motion.
 */

/** "move" (tier): a partition rising off the stack toward the target disk. */
export function MoveIcon({ className }: { className?: string }): JSX.Element {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className={className}>
      <rect x="4" y="15.5" width="12" height="3" rx="1.5" />
      <path className="animate-move-rise motion-reduce:animate-none" d="M10 2.5 14.5 8H12v5H8V8H5.5Z" />
    </svg>
  );
}

/** "optimize": scattered parts squeezing into one compact part. */
export function OptimizeIcon({ className }: { className?: string }): JSX.Element {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className={className}>
      <rect x="8.5" y="5.5" width="3" height="9" rx="1.5" />
      <path className="animate-optimize-squeeze-l motion-reduce:animate-none" d="M2.5 6 7 10l-4.5 4Z" />
      <path className="animate-optimize-squeeze-r motion-reduce:animate-none" d="M17.5 6 13 10l4.5 4Z" />
    </svg>
  );
}

/** "merge": two parts dropping into one compacted part. */
export function MergeIcon({ className }: { className?: string }): JSX.Element {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className={className}>
      <rect x="4" y="15.5" width="12" height="3" rx="1.5" />
      <rect
        className="animate-merge-drop-l motion-reduce:animate-none"
        x="3.5"
        y="4.5"
        width="5.5"
        height="3"
        rx="1.5"
      />
      <rect
        className="animate-merge-drop-r motion-reduce:animate-none"
        x="11"
        y="4.5"
        width="5.5"
        height="3"
        rx="1.5"
      />
    </svg>
  );
}

/** "actions": pending actionable work, pulsing for attention. */
export function ActionsIcon({ className }: { className?: string }): JSX.Element {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className={className}>
      <path className="animate-actions-pulse motion-reduce:animate-none" d="M11 1 4 11h4.5L7.5 19 16 8.5h-4.2Z" />
    </svg>
  );
}

/** "running": a leg executing, bars working through the parts. */
export function RunningIcon({ className }: { className?: string }): JSX.Element {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className={className}>
      <rect
        className="origin-bottom animate-run-bounce [transform-box:fill-box] motion-reduce:animate-none"
        x="3"
        y="4"
        width="3.5"
        height="13"
        rx="1.75"
      />
      <rect
        className="origin-bottom animate-run-bounce [animation-delay:-0.3s] [transform-box:fill-box] motion-reduce:animate-none"
        x="8.25"
        y="4"
        width="3.5"
        height="13"
        rx="1.75"
      />
      <rect
        className="origin-bottom animate-run-bounce [animation-delay:-0.6s] [transform-box:fill-box] motion-reduce:animate-none"
        x="13.5"
        y="4"
        width="3.5"
        height="13"
        rx="1.75"
      />
    </svg>
  );
}
