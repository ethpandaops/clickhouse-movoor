import type { Meta, StoryObj } from '@storybook/react-vite';
import { http, HttpResponse } from 'msw';
import { expect, waitFor, within } from 'storybook/test';
import { ClusterExplorerPage } from './ClusterExplorerPage';
import { clusterExplorerHandlers } from './story-fixtures';

const meta = {
  title: 'Pages/ClusterExplorer',
  component: ClusterExplorerPage,
  decorators: [
    Story => (
      <div className="min-w-[600px] bg-background p-4">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
  },
  tags: ['autodocs'],
} satisfies Meta<typeof ClusterExplorerPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Populated: Story = {
  args: {
    defaultExpandedTables: ['movoor_dev.test_generic_network_month_local'],
    defaultExpandedNodes: ['movoor_dev.test_generic_network_month_local/shard0-replica0'],
    defaultExpandedPartitions: [
      'movoor_dev.test_generic_network_month_local/shard0-replica0/2a2b3c4d5e6f00112233445566778899',
    ],
  },
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers(),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);

    await waitFor(() => {
      expect(canvas.getByTitle('movoor_dev.test_generic_network_month_local')).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(canvas.getByText('shard0-replica0')).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(canvasElement.textContent).toContain("('mainnet',202602)");
    });
    const content = canvasElement.textContent ?? '';
    expect(content.indexOf("('mainnet',202601)")).toBeLessThan(content.indexOf("('mainnet',202602)"));
    expect(content.indexOf('all_2_2_0')).toBeLessThan(content.indexOf('all_3_3_0'));
    expect(canvas.getByText('all_2_2_0')).toBeInTheDocument();
  },
};

export const PartialCollection: Story = {
  args: {
    defaultExpandedTables: ['movoor_dev.test_generic_network_month_local'],
  },
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ partial: true }),
    },
  },
  play: async ({ canvasElement }) => {
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('Partial collection');
    });
  },
};

export const Empty: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ empty: true }),
    },
  },
  play: async ({ canvasElement }) => {
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('No watched tables');
    });
  },
};

export const RequestFailed: Story = {
  parameters: {
    msw: {
      handlers: [
        http.get('/api/v1/nodes', () =>
          HttpResponse.json({
            collection: {
              collectedAt: '2026-06-08T12:00:00Z',
              partial: false,
              collectionDurationMs: 1,
              nodesExpected: 0,
              nodesResponded: 0,
              nodesFailed: 0,
              warnings: [],
            },
            items: [],
          })
        ),
        http.get('/api/v1/tables', () =>
          HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Service Unavailable',
              status: 503,
              detail: 'no configured ClickHouse node responded',
            },
            { status: 503 }
          )
        ),
      ],
    },
  },
  play: async ({ canvasElement }) => {
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('no configured ClickHouse node responded');
    });
  },
};

export const Loading: Story = {
  tags: ['test-exclude'],
  parameters: {
    msw: {
      handlers: [
        http.get('/api/v1/nodes', () => new Promise(() => undefined)),
        http.get('/api/v1/tables', () => new Promise(() => undefined)),
      ],
    },
  },
};
