import type { Meta, StoryObj } from '@storybook/react-vite';
import { NodeRowsSkeleton, TableRowsSkeleton } from './ClusterExplorerSkeleton';

const meta = {
  title: 'Pages/ClusterExplorer/ClusterExplorerSkeleton',
  component: TableRowsSkeleton,
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
          'Shimmer placeholders matching the explorer row grid: table-level rows for the initial load, node-level rows while an expanded table fetches detail and parts.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof TableRowsSkeleton>;

export default meta;
type Story = StoryObj<typeof meta>;

export const TableRows: Story = {};

export const NodeRows: Story = {
  render: () => <NodeRowsSkeleton count={4} />,
};

export const NodeRowsMinimum: Story = {
  render: () => <NodeRowsSkeleton count={0} />,
};
