import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import { buildNodeGroups, nodeKey, partitionKey, tableKey } from '../../explorer-model';
import { buildTieringIndex } from '../../tiering-model';
import { storyNodes, storyParts, storyTableDetails, storyTieringPlan } from '../../story-fixtures';
import { NodeSection } from './NodeSection';

const tableKeyValue = tableKey({ database: 'movoor_dev', table: 'test_generic_network_month_local' });
const nodeById = new Map(storyNodes.map(node => [node.nodeId, node]));
const groups = buildNodeGroups(nodeById, storyTableDetails[tableKeyValue], storyParts[tableKeyValue] ?? []);
const tieringIndex = buildTieringIndex(storyTieringPlan);
const primaryGroup = groups.find(group => group.nodeId === 'shard0-replica0');
const laggingGroup = groups.find(group => group.nodeId === 'shard1-replica1');

const meta = {
  title: 'Pages/ClusterExplorer/NodeSection',
  component: NodeSection,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
    docs: {
      description: {
        component:
          'Node row under an expanded table: reachability/observation health, replication queue and lag, partition children with tiering rollups.',
      },
    },
  },
  tags: ['autodocs'],
  args: {
    tableKeyValue,
    tieringIndex,
    awaitingRefresh: new Map<string, string>(),
    applyErrors: new Map<string, string>(),
    tieringPaused: false,
    flashKey: null,
    expandedPartitions: new Set<string>(),
    onToggleNode: fn(),
    onTogglePartition: fn(),
    onApplyTiering: fn(),
    onRetryTiering: fn(),
  },
} satisfies Meta<typeof NodeSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ObservedWithPartitions: Story = {
  args: {
    group: primaryGroup!,
    expanded: true,
    expandedPartitions: new Set([partitionKey(tableKeyValue, 'shard0-replica0', '2a2b3c4d5e6f00112233445566778899')]),
    isLast: false,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('shard0-replica0')).toBeInTheDocument();
    await expect(canvas.getAllByText('observed').length).toBeGreaterThan(0);
    await expect(canvas.getByText("('mainnet',202601)")).toBeInTheDocument();
    await expect(canvas.getByText('all_2_2_0')).toBeInTheDocument();
  },
};

export const ReplicationLagging: Story = {
  args: {
    group: laggingGroup!,
    expanded: false,
    isLast: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(/queue 2 \/ lag 7\s?s/)).toBeInTheDocument();
  },
};

export const Unreachable: Story = {
  args: {
    group: {
      nodeId: 'shard0-replica1',
      node: {
        ...storyNodes[1]!,
        reachable: false,
        lastError: 'dial tcp 127.0.0.1:9001: connect: connection refused',
      },
      state: undefined,
      partitions: [],
    },
    expanded: false,
    isLast: false,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getAllByText('unreachable').length).toBeGreaterThan(0);
    await expect(canvas.getAllByText('not observed').length).toBeGreaterThan(0);
  },
};

export const MissingState: Story = {
  args: {
    group: {
      nodeId: 'shard1-replica0',
      node: storyNodes[2],
      state: undefined,
      partitions: [],
    },
    expanded: false,
    isLast: false,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getAllByText('missing state').length).toBeGreaterThan(0);
  },
};

export const ExpandsToEmptyPartitions: Story = {
  args: {
    group: {
      nodeId: 'shard1-replica0',
      node: storyNodes[2],
      state: storyTableDetails[tableKeyValue]?.nodes[2],
      partitions: [],
    },
    expanded: false,
    isLast: true,
  },
  render: function Render(args) {
    const [expanded, setExpanded] = useState(args.expanded);
    return <NodeSection {...args} expanded={expanded} onToggleNode={() => setExpanded(open => !open)} />;
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: /Expand node/ }));
    await expect(canvas.getByText('No active partitions reported on this node')).toBeInTheDocument();
  },
};

export const NodeRowKey: Story = {
  args: {
    group: primaryGroup!,
    expanded: false,
    isLast: false,
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: /Expand node shard0-replica0/ }));
    await expect(args.onToggleNode).toHaveBeenCalledOnce();
    // Sanity-check the composed key used by the page-level expansion sets.
    await expect(nodeKey(tableKeyValue, 'shard0-replica0')).toBe(
      'movoor_dev.test_generic_network_month_local/shard0-replica0'
    );
  },
};
