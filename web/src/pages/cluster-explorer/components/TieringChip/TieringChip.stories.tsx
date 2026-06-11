import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { TieringChip } from './TieringChip';
import { fallbackTieringVisual, tieringVisuals } from './tiering-visuals';

const meta = {
  title: 'Pages/ClusterExplorer/TieringChip',
  component: TieringChip,
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
          'Tiering state chip. Actionable states render as loud filled chips; passive states recede to a quiet icon + label so a mostly-tiered table reads calm.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof TieringChip>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Actionable: Story = {
  args: {
    label: 'tier',
    visual: tieringVisuals.tier,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // The API verb "tier" displays as the plainer "move".
    await expect(canvas.getByText('move')).toBeInTheDocument();
  },
};

export const Quiet: Story = {
  args: {
    label: 'tiered',
    visual: tieringVisuals.tiered,
  },
};

export const AllStates: Story = {
  args: { label: 'tier', visual: tieringVisuals.tier },
  render: () => (
    <div className="flex max-w-md flex-wrap items-center gap-3">
      {Object.entries(tieringVisuals).map(([label, visual]) => (
        <TieringChip key={label} label={label} visual={visual} />
      ))}
      <TieringChip label="unknown" visual={fallbackTieringVisual} />
    </div>
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('move')).toBeInTheDocument();
    await expect(canvas.getByText('misconfigured')).toBeInTheDocument();
    await expect(canvas.getByText('tiered')).toBeInTheDocument();
  },
};
