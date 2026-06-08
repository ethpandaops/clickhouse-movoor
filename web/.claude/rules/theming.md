---
description: Theming and color token usage
globs: src/**/*.tsx, src/**/*.ts, src/index.css
---

# Theming

Two-tier color architecture defined in `src/index.css`:

- **Tier 1:** Primitive scales (neutral) with 50-950 shades
- **Tier 2:** Semantic tokens that reference Tier 1

## Semantic tokens

- Brand: `primary`, `secondary`, `accent`
- UI: `background`, `surface`, `foreground`, `muted`, `border`
- State: `success`, `warning`, `danger`

## Usage

Always use semantic tokens — never primitive scales directly (`bg-neutral-500`).

```tsx
className="bg-primary text-foreground border-border"
className="hover:bg-accent text-muted"
```

Programmatic access:

```tsx
import { useThemeColors } from '@/hooks/useThemeColors';
const colors = useThemeColors(); // { primary, background, ... }
```

## Modifying theme

Edit semantic mappings in `src/index.css` at `@layer base` (`:root` for light, `html.dark` for dark).
