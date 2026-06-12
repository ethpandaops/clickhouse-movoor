import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, waitFor, within } from 'storybook/test';
import { TieringRollupCell } from './TieringRollupCell';

const meta = {
  title: 'Pages/ClusterExplorer/TieringRollupCell',
  component: TieringRollupCell,
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
          'Table/node tiering rollup. The most pressing bucket wins the chip (actions > split > hold > tiered); the tooltip carries the full breakdown.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof TieringRollupCell>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ActionsDominate: Story = {
  args: {
    rollup: { total: 6, actionable: 2, tiered: 3, split: 1, held: 0 },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('2 actions')).toBeInTheDocument();
    await userEvent.hover(canvas.getByText('2 actions'));
    await waitFor(() => {
      expect(document.body.textContent).toContain('6 partitions: 2 actionable · 3 tiered · 1 split · 0 held');
    });
  },
};

export const SplitWithoutActions: Story = {
  args: {
    rollup: { total: 3, actionable: 0, tiered: 2, split: 1, held: 0 },
  },
};

export const HoldsOnly: Story = {
  args: {
    rollup: { total: 2, actionable: 0, tiered: 0, split: 0, held: 2 },
  },
};

export const FullyTiered: Story = {
  args: {
    rollup: { total: 4, actionable: 0, tiered: 4, split: 0, held: 0 },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('4 tiered')).toBeInTheDocument();
  },
};

export const NotInPlan: Story = {
  args: { rollup: undefined },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('-')).toBeInTheDocument();
  },
};
