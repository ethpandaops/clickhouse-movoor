import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { WarningsBanner } from './WarningsBanner';

const collection = {
  collectedAt: '2026-06-08T12:00:00Z',
  partial: true,
  collectionDurationMs: 42,
  nodesExpected: 4,
  nodesResponded: 3,
  nodesFailed: 1,
  warnings: [
    {
      kind: 'reachability' as const,
      code: 'node_unreachable',
      message: 'dial tcp 127.0.0.1:9001: connect: connection refused',
      nodeId: 'shard0-replica1',
    },
  ],
};

const meta = {
  title: 'Pages/ClusterExplorer/WarningsBanner',
  component: WarningsBanner,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component: 'Partial-collection banner listing which nodes failed and why.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof WarningsBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const OneNodeDown: Story = {
  args: { collection },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(/Partial collection: 1 node failed/)).toBeInTheDocument();
    await expect(canvas.getByText(/shard0-replica1: dial tcp/)).toBeInTheDocument();
  },
};

export const MultipleWarnings: Story = {
  args: {
    collection: {
      ...collection,
      nodesResponded: 2,
      nodesFailed: 2,
      warnings: [
        ...collection.warnings,
        {
          kind: 'query_error' as const,
          code: 'parts_query_failed',
          message: 'system.parts query timed out',
          nodeId: 'shard1-replica1',
        },
      ],
    },
  },
};
