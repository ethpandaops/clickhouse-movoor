import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import { ExpandButton } from './ExpandButton';

const meta = {
  title: 'Pages/ClusterExplorer/ExpandButton',
  component: ExpandButton,
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
          'Chevron toggle for tree rows. Clicks do not bubble, so it can live inside rows that are themselves clickable.',
      },
    },
  },
  tags: ['autodocs'],
  args: { onClick: fn() },
} satisfies Meta<typeof ExpandButton>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Collapsed: Story = {
  args: {
    expanded: false,
    label: 'Expand movoor_dev.events_by_month_local',
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    const button = canvas.getByRole('button', { name: 'Expand movoor_dev.events_by_month_local' });
    await expect(button).toHaveAttribute('aria-expanded', 'false');
    await userEvent.click(button);
    await expect(args.onClick).toHaveBeenCalledOnce();
  },
};

export const Expanded: Story = {
  args: {
    expanded: true,
    label: 'Collapse movoor_dev.events_by_month_local',
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole('button')).toHaveAttribute('aria-expanded', 'true');
  },
};

export const InsideClickableRow: Story = {
  args: {
    expanded: false,
    label: 'Expand node shard0-replica0',
  },
  render: function Render(args) {
    return (
      <div
        data-testid="row"
        onClick={() => {
          throw new Error('row click must not fire when the chevron is clicked');
        }}
        className="flex items-center gap-2 rounded-md border border-border px-3 py-2"
      >
        <ExpandButton {...args} />
        <span className="text-sm text-foreground">shard0-replica0</span>
      </div>
    );
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: 'Expand node shard0-replica0' }));
    await expect(args.onClick).toHaveBeenCalledOnce();
  },
};
