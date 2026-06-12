import type { Meta, StoryObj } from '@storybook/react-vite';
import { SummaryStat } from './SummaryStat';

const meta = {
  title: 'Pages/ClusterExplorer/SummaryStat',
  component: SummaryStat,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'centered',
    docs: {
      description: {
        component: 'Compact label/value stat for the tiering summary strip, with accent/muted/danger emphasis.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof SummaryStat>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: { label: 'tables', value: '2' },
};

export const Accent: Story = {
  args: { label: 'actions', value: '3', accent: true },
};

export const Danger: Story = {
  args: { label: 'nodes', value: '3/4', danger: true },
};

export const SummaryStrip: Story = {
  args: { label: 'tables', value: '2' },
  render: () => (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
      <SummaryStat label="tables" value="2" />
      <SummaryStat label="nodes" value="4/4" />
      <SummaryStat label="partitions" value="5" />
      <SummaryStat label="actions" value="2" accent />
      <SummaryStat label="moved today" value="1.5 MiB" accent />
      <SummaryStat label="reconciled" value="6/8/26, 12:00 PM" muted />
      <SummaryStat label="table errors" value="1" danger />
    </div>
  ),
};
