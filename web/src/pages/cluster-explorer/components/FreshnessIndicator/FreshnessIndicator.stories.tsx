import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { FreshnessIndicator } from './FreshnessIndicator';

const meta = {
  title: 'Pages/ClusterExplorer/FreshnessIndicator',
  component: FreshnessIndicator,
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
        component:
          'Live snapshot-age indicator: green while polling normally, amber as data ages past ~2.5 poll intervals, red when ageing badly or while refreshes are actively failing.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof FreshnessIndicator>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Fresh: Story = {
  args: { updatedAt: Date.now() - 8_000 },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(/updated \d+s ago/)).toBeInTheDocument();
  },
};

export const Stale: Story = {
  args: { updatedAt: Date.now() - 70_000 },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(/updated 1m ago/)).toBeInTheDocument();
  },
};

export const Dead: Story = {
  args: { updatedAt: Date.now() - 10 * 60_000 },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(/stale · updated 10m ago/)).toBeInTheDocument();
  },
};

export const RefreshFailing: Story = {
  args: { updatedAt: Date.now() - 30_000, failing: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(/refresh failing · last data \d+s ago/)).toBeInTheDocument();
  },
};

export const NeverCollected: Story = {
  args: { updatedAt: undefined },
  play: async ({ canvasElement }) => {
    await expect(canvasElement.textContent).not.toContain('updated');
  },
};
