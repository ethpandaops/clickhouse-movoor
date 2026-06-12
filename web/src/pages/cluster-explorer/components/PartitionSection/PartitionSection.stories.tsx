import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import { buildPartitionGroups } from '../../explorer-model';
import { storyParts, storyTieringPlan } from '../../story-fixtures';
import { PartitionSection } from './PartitionSection';

const testTableParts = storyParts['movoor_dev.test_generic_network_month_local'] ?? [];
const partitions = buildPartitionGroups('shard0-replica0', testTableParts);
const tieredPartition = partitions[0];
const splitPartition = partitions[1];

const meta = {
  title: 'Pages/ClusterExplorer/PartitionSection',
  component: PartitionSection,
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
          'Partition row with its tiering verdict cell, expanding into per-part leaf rows. Split partitions show every disk that still holds active parts.',
      },
    },
  },
  tags: ['autodocs'],
  args: {
    onTogglePartition: fn(),
    onApplyTiering: fn(),
    onRetryTiering: fn(),
    awaitingToken: undefined,
    inFlight: false,
    tieringPaused: false,
    flash: false,
  },
} satisfies Meta<typeof PartitionSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SplitWithApplyAction: Story = {
  args: {
    partition: splitPartition,
    expanded: false,
    tieringPartition: storyTieringPlan.items[1],
    trail: [false, false],
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("('mainnet',202602)")).toBeInTheDocument();
    const apply = canvas.getByRole('button', { name: /Apply append/ });
    await userEvent.click(apply);
    await expect(args.onApplyTiering).toHaveBeenCalledOnce();
    await expect(args.onTogglePartition).not.toHaveBeenCalled();
  },
};

export const ExpandsIntoParts: Story = {
  args: {
    partition: splitPartition,
    expanded: false,
    tieringPartition: storyTieringPlan.items[1],
    trail: [false, true],
  },
  render: function Render(args) {
    const [expanded, setExpanded] = useState(args.expanded);
    return <PartitionSection {...args} expanded={expanded} onTogglePartition={() => setExpanded(open => !open)} />;
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.queryByText('all_2_2_0')).toBeNull();
    await userEvent.click(canvas.getByRole('button', { name: /Expand partition/ }));
    await expect(canvas.getByText('all_2_2_0')).toBeInTheDocument();
    await expect(canvas.getByText('all_3_3_0')).toBeInTheDocument();
  },
};

export const TieredAndQuiet: Story = {
  args: {
    partition: tieredPartition,
    expanded: true,
    tieringPartition: storyTieringPlan.items[0],
    trail: [false, true],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('tiered')).toBeInTheDocument();
    await expect(canvas.getByText('all_1_1_0')).toBeInTheDocument();
  },
};

export const FlashedByDeepLink: Story = {
  args: {
    partition: splitPartition,
    expanded: false,
    tieringPartition: storyTieringPlan.items[1],
    trail: [false, false],
    flash: true,
  },
};

export const NoTieringVerdict: Story = {
  args: {
    partition: tieredPartition,
    expanded: false,
    tieringPartition: undefined,
    trail: [true, true],
  },
};
