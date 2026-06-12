import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { http, HttpResponse } from 'msw';
import { expect, fn, userEvent, waitFor, within } from 'storybook/test';
import { nodeKey, tableKey } from '../../explorer-model';
import { buildTieringIndex } from '../../tiering-model';
import { clusterExplorerHandlers, storyNodes, storyTables, storyTieringPlan } from '../../story-fixtures';
import { TableSection } from './TableSection';

const table = storyTables[0]!;
const tableKeyValue = tableKey(table);
const nodeById = new Map(storyNodes.map(node => [node.nodeId, node]));
const tieringIndex = buildTieringIndex(storyTieringPlan);

const meta = {
  title: 'Pages/ClusterExplorer/TableSection',
  component: TableSection,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
    msw: {
      handlers: clusterExplorerHandlers(),
    },
    docs: {
      description: {
        component:
          'Top-level table row owning the detail and parts queries for its subtree. Data is fetched only while the row is expanded.',
      },
    },
  },
  tags: ['autodocs'],
  args: {
    table,
    nodeById,
    tieringIndex,
    expandedNodes: new Set<string>(),
    expandedPartitions: new Set<string>(),
    awaitingRefresh: new Map<string, string>(),
    inFlightKeys: new Set<string>(),
    applyErrors: new Map<string, string>(),
    tieringPaused: false,
    flashKey: null,
    onToggleTable: fn(),
    onToggleNode: fn(),
    onTogglePartition: fn(),
    onApplyTiering: fn(),
    onRetryTiering: fn(),
  },
} satisfies Meta<typeof TableSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Collapsed: Story = {
  args: { expanded: false },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('test_generic_network_month_local')).toBeInTheDocument();
    await expect(canvas.getByText('1 action')).toBeInTheDocument();
    await expect(canvas.queryByText('shard0-replica0')).toBeNull();
  },
};

export const ExpandsAndFetches: Story = {
  args: { expanded: false },
  render: function Render(args) {
    const [expanded, setExpanded] = useState(args.expanded);
    return <TableSection {...args} expanded={expanded} onToggleTable={() => setExpanded(open => !open)} />;
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: /Expand movoor_dev/ }));
    await waitFor(() => {
      expect(canvas.getByText('shard0-replica0')).toBeInTheDocument();
      expect(canvas.getByText('shard1-replica1')).toBeInTheDocument();
    });
  },
};

export const ExpandedWithNodeOpen: Story = {
  args: {
    expanded: true,
    expandedNodes: new Set([nodeKey(tableKeyValue, 'shard0-replica0')]),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getByText("('mainnet',202601)")).toBeInTheDocument();
      expect(canvas.getByText("('mainnet',202602)")).toBeInTheDocument();
    });
  },
};

export const SubtreeRequestFails: Story = {
  args: { expanded: true },
  parameters: {
    msw: {
      handlers: [
        // First match wins in MSW, so the failing parts handler must precede
        // the defaults.
        http.get('/api/v1/tables/:database/:table/parts', () =>
          HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Service Unavailable',
              status: 503,
              detail: 'parts collection timed out',
            },
            { status: 503 }
          )
        ),
        ...clusterExplorerHandlers(),
      ],
    },
  },
  play: async ({ canvasElement }) => {
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('parts collection timed out');
    });
  },
};
