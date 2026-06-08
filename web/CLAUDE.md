# clickhouse-movoor

## Commands

```bash
pnpm dev                    # Dev server proxying to local backend (localhost:8080)
pnpm lint
pnpm test
pnpm build
pnpm typecheck              # Type check without emitting
pnpm format                 # Prettier format src/**/*.{ts,tsx}
pnpm generate:api           # Generate API client from OpenAPI spec
pnpm storybook
```

## Libraries

- pnpm v11, node v24, vite v8, react v19, typescript v6
- tailwindcss v4, heroicons v2
- @tanstack/react-query v5, @tanstack/react-router v1, @tanstack/router-plugin v1
- zod v4, clsx
- storybook v10, vitest v4

## Repository Structure

```bash
src/
  routes/                             # Route definitions using TanStack Router
    __root.tsx                        # Root layout with sidebar, providers, navigation
    index.tsx                         # "/" - Landing page route
    [section].tsx                     # Layout routes for sections
    [section]/                        # Section-specific routes
      [page-name].tsx                 # Page route
      [page-name]/                    # Nested page routes
        index.tsx                     # Default nested route
        $param.tsx                    # Dynamic parameter route

  pages/                              # Page components (actual UI implementation)
    IndexPage.tsx                     # Landing page component
    [section]/                        # Section-specific pages
      [page-name]/                    # Individual page folder
        IndexPage.tsx                 # Main page component
        [OtherPage].tsx               # Additional page components (e.g., DetailPage)
        index.ts                      # Barrel export
        components/                   # Page-specific components
          [ComponentName]/
            [ComponentName].tsx
            [ComponentName].test.tsx
            index.ts
        hooks/                        # Page-specific hooks (optional)
          use[HookName].ts
        contexts/                     # Page-specific contexts (optional)
          [ContextName].ts
        providers/                    # Page-specific providers (optional)
          [ProviderName].tsx

  components/                         # Core, app-wide reusable UI components
    [Category]/                       # Component category folder
      [ComponentName]/                # Individual component folder
        [ComponentName].tsx           # Component implementation
        [ComponentName].test.tsx      # Vitest tests
        [ComponentName].types.ts      # TypeScript types (optional)
        [ComponentName].stories.tsx   # Storybook stories
        index.ts                      # Barrel export

  providers/                          # React Context Providers
    [ProviderName]/                   # e.g., NetworkProvider, ThemeProvider
      [ProviderName].tsx
      index.ts

  contexts/                           # React Context definitions
    [ContextName]/                    # e.g., NetworkContext, ThemeContext
      [ContextName].ts
      [ContextName].types.ts
      index.ts

  hooks/                              # Custom React hooks
    use[HookName]/                    # e.g., useNetwork, useConfig, useBeaconClock
      use[HookName].ts
      use[HookName].test.ts           # Vitest tests
      use[HookName].types.ts          # Optional: for complex hooks
      index.ts

  api/                                # Generated API client (do not edit)
    @tanstack/
      react-query.gen.ts              # TanStack Query hooks - USE THIS for all API calls
    [other generated files]           # Auto-generated client, types, schemas, etc.

  utils/                              # Utility functions and helpers
    [util-name].ts                    # Utility functions
    [util-name].test.ts               # Vitest tests
    index.ts                          # Barrel export

  main.tsx                            # Application entry point
  index.css                           # Global styles and Tailwind config
  routeTree.gen.ts                    # Generated route tree (auto-generated)
  vite-env.d.ts                       # Vite environment types
```

## Architecture Patterns

### Component & State Philosophy

**Core/Shared** (`src/components/`, `src/hooks/`, `src/contexts/`):

- App-wide reusable building blocks
- Generic and configurable
- Work in any context without page logic
- Compose from other core items when logical
- Core components include Storybook stories

**Page-Scoped** (`src/pages/[section]/components|hooks|contexts|providers/`):

- Used within specific page/section only
- Compose/extend core items for page needs
- Contain page-specific business logic
- Specialized for page requirements
- Keep complex page state isolated

**Placement rule:** Used across pages → Core. Page-specific → Page-scoped.

### Core Component Categories

Current categories in `src/components/`:

- **Elements**: Basic UI building blocks (Avatar, Badge, Button, ButtonGroup, Icons)
- **Feedback**: User feedback (Alert)
- **Forms**: Form controls (Checkbox, CheckboxGroup, Input, RadioGroup, SelectMenu, Toggle)
- **Layout**: Structure and layout (Card, Container, Divider, Header, ListContainer, LoadingContainer, Sidebar, ThemeToggle)
- **Lists**: Tables and lists (Table)
- **Navigation**: Navigation elements (ProgressBar, ProgressSteps)
- **Overlays**: Modals and overlays (ConfigGate, FatalError)

## Development Guidelines

### Quick Reference

- **New page**: Route in `src/routes/[section]/`, page in `src/pages/[section]/[page-name]/`
- **Feature image**: `public/images/[section]/[page-name].png` for social sharing
- **Skeleton component**: `src/pages/[section]/[page-name]/components/[PageName]Skeleton/` using `LoadingContainer`
- **Core component**: `src/components/[Category]/[ComponentName]/` - reusable, generic
- **Page-scoped component**: `src/pages/[section]/[page-name]/components/[ComponentName]/` - page-specific
- **Core hook**: `src/hooks/use[HookName]/` - app-wide logic
- **Page-scoped hook**: `src/pages/[section]/[page-name]/hooks/use[HookName].ts` - page-specific logic
- **Utility function**: `src/utils/[util-name].ts` - helper functions
- **Core context**: `src/contexts/[ContextName]/` with provider in `src/providers/[ProviderName]/`
- **Page-scoped context**: `src/pages/[section]/[page-name]/contexts/[ContextName].ts` with local provider

### Best Practices

- Storybook stories for all core components
- Keep core components generic and reusable
- Compose core components in page-scoped components
- Use `pnpm storybook` with Playwright MCP for iteration
- Write Vitest tests for all components, hooks, and utilities
- Use JSDoc style comments for functions, components, hooks, and complex types
- Use Tailwind v4 classes
- Use semantic color tokens from `src/index.css` theme
- Use TanStack Router for navigation
- Use `@/api/@tanstack/react-query.gen.ts` hooks for API calls
- Use path aliases over relative imports
- Run `pnpm lint` and `pnpm build` before committing
- Use `clsx` for conditional classes
- React `useMemo`/`memo` should only be used for genuinely expensive calculations

### Naming Conventions

- **Routes** (`.tsx`): lowercase - `index.tsx`, `slots.tsx`, `slots/$slot.tsx`
- **Pages** (`.tsx`, `.ts`): PascalCase - `UsersPage.tsx`, `UserTable.tsx`
- **Components** (`.tsx`, `.ts`): PascalCase - `NetworkSelector.tsx`, `SelectMenu.tsx`
- **Providers** (`.tsx`): PascalCase - `NetworkProvider.tsx` (in `src/providers/`)
- **Context** (`.ts`): PascalCase - `NetworkContext.ts` (in `src/contexts/`)
- **Hooks** (`.ts`): camelCase starting with `use` - `useNetwork.ts`, `useConfig.ts`
- **Utils** (`.ts`): kebab-case - `api-config.ts`, `auth-service.ts`
- **Tests** (`.test.ts(x)`): Match source file name - `useNetwork.test.ts`, `colour.test.ts`
- **Barrel exports**: Always `.ts` - `index.ts`

### Testing

- **Required for**: All hooks, utilities, and core components
- **Framework**: Vitest for hooks and utilities, Storybook interactions for components
- **Location**: Co-located with source files (`.test.ts` or `.test.tsx` for Vitest, `.stories.tsx` for Storybook)
- **Naming**: Test files match source files with `.test.ts(x)` or `.stories.tsx` extension

## Additional Rules

Detailed standards for specific topics are in `.claude/rules/`:

- [Forms](.claude/rules/forms.md) — Zod search param validation, react-hook-form patterns
- [Loading States](.claude/rules/loading-states.md) — Skeleton/shimmer patterns
- [SEO](.claude/rules/seo.md) — Head meta hierarchy, feature images, route implementation
- [Storybook](.claude/rules/storybook.md) — Decorator pattern, story title convention
- [Theming](.claude/rules/theming.md) — Two-tier color architecture, semantic tokens
```
