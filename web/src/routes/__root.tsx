import { type JSX } from 'react';
import { createRootRoute, HeadContent, Link, Outlet } from '@tanstack/react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ThemeProvider } from '@/providers/ThemeProvider';
import { Brand } from '@/components/Layout/Brand';
import { ThemeToggle } from '@/components/Layout/ThemeToggle';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      retryDelay: attemptIndex => Math.min(1000 * 2 ** attemptIndex, 30000),
    },
  },
});

function RootComponent(): JSX.Element {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <HeadContent />
        <div className="min-h-dvh bg-background text-foreground">
          <header className="sticky top-0 z-40 border-b border-border bg-surface/80 backdrop-blur-sm">
            <div className="mx-auto flex h-14 max-w-[1800px] items-center justify-between gap-4 px-4 sm:px-6">
              <Link
                to="/"
                className="flex min-w-0 items-center rounded-md focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              >
                <Brand />
              </Link>
              <ThemeToggle />
            </div>
          </header>
          <main className="px-4 py-6 sm:px-6">
            <Outlet />
          </main>
        </div>
      </ThemeProvider>
    </QueryClientProvider>
  );
}

function RootErrorComponent({ error }: { error: Error }): JSX.Element {
  return (
    <div className="flex min-h-dvh items-center justify-center bg-background p-6">
      <div className="w-full max-w-md text-center">
        <h1 className="text-2xl font-bold text-foreground">Something went wrong</h1>
        <p className="mt-2 text-sm text-muted">{error.message}</p>
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="mt-6 rounded-md bg-primary px-4 py-2 text-sm font-semibold text-on-primary transition-colors hover:bg-primary/90"
        >
          Reload page
        </button>
      </div>
    </div>
  );
}

export const Route = createRootRoute({
  component: RootComponent,
  errorComponent: RootErrorComponent,
  head: () => ({
    meta: [
      { title: import.meta.env.VITE_BASE_TITLE },
      { charSet: 'utf-8' },
      {
        name: 'viewport',
        content: 'width=device-width, initial-scale=1.0, maximum-scale=5.0, interactive-widget=resizes-content',
      },
      { name: 'description', content: 'clickhouse-movoor' },
    ],
  }),
});
