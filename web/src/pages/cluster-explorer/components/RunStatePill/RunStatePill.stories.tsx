import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { RunStatePill } from './RunStatePill';

const meta = {
  title: 'Pages/ClusterExplorer/RunStatePill',
  component: RunStatePill,
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
        component: 'Controller pause-state pill with a live dot. Surfaces the pause reason when one is recorded.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof RunStatePill>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Running: Story = {
  args: { state: 'running' },
};

export const StoppedByOperator: Story = {
  args: { state: 'stopped', reason: 'operator' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('stopped')).toBeInTheDocument();
    await expect(canvas.getByText('· operator')).toBeInTheDocument();
  },
};

export const Stopping: Story = {
  args: { state: 'stopping', reason: 'draining in-flight legs' },
};
