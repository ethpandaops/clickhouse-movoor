import { type JSX } from 'react';
import { createFileRoute } from '@tanstack/react-router';

function HomePage(): JSX.Element {
  return (
    <div className="mx-auto max-w-2xl animate-fade-in">
      <h1 className="text-2xl font-bold text-foreground">clickhouse-movoor</h1>
      <p className="mt-2 text-sm text-muted">
        Skeleton web app. Edit <code className="font-mono text-foreground">web/src/routes/index.tsx</code> to get
        started, or add new files under <code className="font-mono text-foreground">web/src/routes/</code>.
      </p>
      <a
        href="/api/v1/healthz"
        className="mt-6 inline-flex rounded-md bg-primary px-4 py-2 text-sm font-semibold text-on-primary transition-colors hover:bg-primary/90"
      >
        Check API health
      </a>
    </div>
  );
}

export const Route = createFileRoute('/')({
  component: HomePage,
  head: () => ({
    meta: [{ title: import.meta.env.VITE_BASE_TITLE }, { name: 'description', content: 'clickhouse-movoor' }],
  }),
});
