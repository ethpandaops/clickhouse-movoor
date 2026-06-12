# clickhouse-movoor web

Operator UI for clickhouse-movoor. The app is a React/Vite single-page UI
embedded into the Go binary and backed by the OpenAPI-generated client in
`src/api/`.

## Commands

```bash
pnpm dev                    # Dev server proxying /api to localhost:8080
pnpm lint
pnpm test
pnpm build
pnpm typecheck              # Type check without emitting
pnpm format                 # Prettier format src/**/*.{ts,tsx}
pnpm generate:api           # Generate API client from ../api/openapi.yaml
pnpm storybook
```

## Libraries

- pnpm v11, node v24, vite v8, react v19, typescript v6
- tailwindcss v4, heroicons v2
- @tanstack/react-query v5, @tanstack/react-router v1
- @hey-api/openapi-ts, zod v4
- storybook v10, vitest v4, MSW for Storybook fixtures

## Structure

```bash
src/
  routes/
    __root.tsx              # app shell, providers, header
    index.tsx               # "/" renders the cluster explorer
  pages/
    cluster-explorer/       # operator UI and page-scoped components
  components/
    Feedback/               # ErrorBoundary, Toaster
    Layout/                 # Brand, ThemeToggle
    Overlays/               # Tooltip
  contexts/
    ThemeContext/
  hooks/
    useTheme/
    useThemeColors/
  utils/
    api-config.ts           # generated-client runtime config
    color.ts
    format.ts
  api/                      # generated API client; do not edit by hand
  routeTree.gen.ts          # generated TanStack route tree
  index.css                 # Tailwind v4 and semantic color tokens
```

## Conventions

- `api/openapi.yaml` is the source of truth for the typed API client. Run
  `pnpm generate:api` after changing API schemas or paths.
- Colors must use semantic tokens from `src/index.css`; custom ESLint rules
  enforce this.
- Use page-scoped components under `pages/cluster-explorer/components` unless a
  component is genuinely shared across the app.
- Keep Storybook fixtures close to the component or page they exercise.
- Use path aliases (`@/...`) for source imports.
- React `useMemo`/`memo` should only be used for genuinely expensive
  calculations or stable context values.

## Additional Rules

Detailed standards for specific topics are in `.claude/rules/`:

- [Forms](.claude/rules/forms.md)
- [Loading States](.claude/rules/loading-states.md)
- [SEO](.claude/rules/seo.md)
- [Storybook](.claude/rules/storybook.md)
- [Theming](.claude/rules/theming.md)
