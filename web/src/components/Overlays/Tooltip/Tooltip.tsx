import { useState, type JSX, type ReactNode } from 'react';
import {
  autoUpdate,
  flip,
  FloatingPortal,
  offset,
  shift,
  useDismiss,
  useFloating,
  useFocus,
  useHover,
  useInteractions,
  useRole,
  type Placement,
} from '@floating-ui/react';

interface TooltipProps {
  /** Tooltip body. Plain text or simple read-only nodes — never interactive content. */
  content: ReactNode;
  /** Trigger element(s); wrapped in an inline-flex span that anchors the tooltip. */
  children: ReactNode;
  placement?: Placement;
  /** Extra classes for the wrapper span (e.g. min-w-0 truncation contexts). */
  className?: string;
}

/**
 * Hover/focus tooltip on the canonical floating-ui recipe: useHover +
 * useFocus + useDismiss + useRole('tooltip') with offset/flip/shift
 * middleware and autoUpdate anchoring, rendered through a portal so it
 * escapes overflow/stacking contexts.
 *
 * The trigger is wrapped in a span rather than cloned onto the child, which
 * keeps ref handling trivial at the cost of an extra element; keyboard focus
 * opens the tooltip only when the wrapped child is itself focusable.
 */
export function Tooltip({ content, children, placement = 'top', className }: TooltipProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const { refs, floatingStyles, context } = useFloating({
    open,
    onOpenChange: setOpen,
    placement,
    whileElementsMounted: autoUpdate,
    middleware: [offset(6), flip(), shift({ padding: 8 })],
  });
  const hover = useHover(context, { move: false, delay: { open: 250, close: 0 } });
  const focus = useFocus(context);
  const dismiss = useDismiss(context);
  const role = useRole(context, { role: 'tooltip' });
  const { getReferenceProps, getFloatingProps } = useInteractions([hover, focus, dismiss, role]);

  return (
    <>
      <span ref={refs.setReference} className={className ?? 'inline-flex min-w-0'} {...getReferenceProps()}>
        {children}
      </span>
      {open && content != null && (
        <FloatingPortal>
          <div
            ref={refs.setFloating}
            style={floatingStyles}
            {...getFloatingProps()}
            className="z-50 max-w-xs rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs/5 text-foreground shadow-lg"
          >
            {content}
          </div>
        </FloatingPortal>
      )}
    </>
  );
}
