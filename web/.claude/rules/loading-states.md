---
description: Loading state and skeleton patterns
globs: src/pages/**/*.tsx, src/components/**/*.tsx
---

# Loading States

## Shimmer/skeleton pattern

- Use `LoadingContainer` from `src/components/Layout/LoadingContainer/` as base
- Create page-specific skeletons: `[PageName]Skeleton` or `[ComponentName]Skeleton`
- Place in `pages/[section]/components/` alongside other page components
- Match skeleton layout to actual content structure
- Only use for significant data fetches
- Consider error states alongside loading
