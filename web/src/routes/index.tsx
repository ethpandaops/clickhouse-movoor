import { type JSX } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { ClusterExplorerPage } from '@/pages/cluster-explorer';

function HomePage(): JSX.Element {
  return <ClusterExplorerPage />;
}

export const Route = createFileRoute('/')({
  component: HomePage,
  head: () => ({
    meta: [{ title: import.meta.env.VITE_BASE_TITLE }, { name: 'description', content: 'clickhouse-movoor' }],
  }),
});
