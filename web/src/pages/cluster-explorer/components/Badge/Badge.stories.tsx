import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { Badge } from './Badge';
import type { BadgeTone } from './badge-tones';

const meta = {
  title: 'Pages/ClusterExplorer/Badge',
  component: Badge,
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
        component: 'Small tonal status chip used on every explorer row: shard layout, disks, health, conditions.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof Badge>;

export default meta;
type Story = StoryObj<typeof meta>;

const tones: BadgeTone[] = ['danger', 'info', 'muted', 'success', 'warning'];

export const Info: Story = {
  args: { tone: 'info', children: '2x2' },
};

export const AllTones: Story = {
  args: { tone: 'info', children: 'tone' },
  render: () => (
    <div className="flex flex-wrap gap-2">
      {tones.map(tone => (
        <Badge key={tone} tone={tone}>
          {tone}
        </Badge>
      ))}
    </div>
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    for (const tone of tones) {
      await expect(canvas.getByText(tone)).toBeInTheDocument();
    }
  },
};
