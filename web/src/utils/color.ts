/**
 * Colour Utilities
 *
 * Helper functions for working with CSS colors, including modern color formats like oklch.
 * Uses culori for robust color parsing and conversion.
 */

import { formatHex } from 'culori';

const HEX_COLOR_REGEX = /^#[0-9A-Fa-f]{6}$/;
const SEMANTIC_FALLBACK_TOKENS = ['var(--color-foreground)', 'var(--color-primary)'] as const;
const ZERO_HEX_CHANNEL = (0).toString(16).padStart(2, '0');
const DETERMINISTIC_HEX_FALLBACK = `#${ZERO_HEX_CHANNEL}${ZERO_HEX_CHANNEL}${ZERO_HEX_CHANNEL}`;

function resolveColorWithBrowser(color: string): string | null {
  if (!color || typeof document === 'undefined') return null;

  const temp = document.createElement('div');
  temp.style.color = color;
  document.body.appendChild(temp);
  const computedColor = window.getComputedStyle(temp).color;
  document.body.removeChild(temp);

  return formatHex(computedColor) ?? null;
}

function resolveFromCandidates(candidates: Array<string | undefined>): string | null {
  for (const candidate of candidates) {
    if (!candidate) continue;

    if (HEX_COLOR_REGEX.test(candidate)) {
      return candidate;
    }

    const resolved = resolveColorWithBrowser(candidate);
    if (resolved) {
      return resolved;
    }
  }

  return null;
}

function resolveThemeFallbackColor(fallback?: string): string | null {
  return resolveFromCandidates([fallback, ...SEMANTIC_FALLBACK_TOKENS]);
}

function resolveRootForegroundHex(): string | null {
  if (typeof document === 'undefined') return null;

  const rootStyles = window.getComputedStyle(document.documentElement);
  return resolveFromCandidates([rootStyles.getPropertyValue('--color-foreground').trim(), rootStyles.color]);
}

function resolveBrowserDefaultHex(): string | null {
  if (typeof document === 'undefined') return null;

  const temp = document.createElement('div');
  document.body.appendChild(temp);
  const defaultColor = window.getComputedStyle(temp).color;
  document.body.removeChild(temp);

  return formatHex(defaultColor) ?? null;
}

/**
 * Resolve any CSS color to hex format.
 *
 * Converts modern CSS colors (oklch, color-mix, etc.) to hex format that chart
 * libraries can understand. Uses culori for robust color parsing.
 *
 * @param color - Any valid CSS color string
 * @param fallback - Optional fallback color if resolution fails. Defaults to semantic theme tokens.
 * @returns Hex color string (e.g., '#06b6d4')
 */
export function resolveCssColorToHex(color: string, fallback?: string): string {
  // If already a valid 6-digit hex color, return as-is
  if (HEX_COLOR_REGEX.test(color)) {
    return color;
  }

  const resolvedColor = resolveColorWithBrowser(color);
  if (!resolvedColor) {
    const resolvedFallback = resolveThemeFallbackColor(fallback);
    if (resolvedFallback) {
      console.warn(
        `[resolveCssColorToHex] Failed to resolve CSS color "${color}". ` + `Falling back to ${resolvedFallback}.`
      );
      return resolvedFallback;
    }

    const rootForegroundHex = resolveRootForegroundHex();
    if (rootForegroundHex) {
      console.warn(
        `[resolveCssColorToHex] Failed to resolve CSS color "${color}" and semantic fallback tokens. ` +
          `Falling back to root foreground ${rootForegroundHex}.`
      );
      return rootForegroundHex;
    }

    const browserDefaultHex = resolveBrowserDefaultHex();
    if (browserDefaultHex) {
      console.warn(
        `[resolveCssColorToHex] Failed to resolve CSS color "${color}" and root foreground. ` +
          `Falling back to browser default ${browserDefaultHex}.`
      );
      return browserDefaultHex;
    }

    console.warn(
      `[resolveCssColorToHex] Failed to resolve CSS color "${color}" and all semantic fallback paths. ` +
        `Returning deterministic fallback ${DETERMINISTIC_HEX_FALLBACK}.`
    );
    return DETERMINISTIC_HEX_FALLBACK;
  }

  return resolvedColor;
}

/**
 * Convert a CSS color to rgba format with an alpha channel.
 *
 * Useful for canvas operations (like chart gradients) that require rgba format.
 *
 * @param color - Any CSS color (will be resolved to hex first)
 * @param alpha - Alpha/opacity value between 0 (transparent) and 1 (opaque)
 * @param fallback - Optional fallback color if resolution fails. Defaults to semantic theme tokens.
 * @returns RGBA color string (e.g., 'rgba(6, 182, 212, 0.5)')
 */
export function hexToRgba(color: string, alpha: number, fallback?: string): string {
  const clampedAlpha = Math.max(0, Math.min(1, alpha));
  const hex = resolveCssColorToHex(color, fallback);

  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);

  return `rgba(${r}, ${g}, ${b}, ${clampedAlpha})`;
}
