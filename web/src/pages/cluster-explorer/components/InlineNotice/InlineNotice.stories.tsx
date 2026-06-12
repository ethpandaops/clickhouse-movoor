import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import { InlineNotice } from './InlineNotice';

const meta = {
  title: 'Pages/ClusterExplorer/InlineNotice',
  component: InlineNotice,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component:
          'Quiet inline message for empty or error states inside an expanded tree level. Errors lead with a plain-language headline; the raw backend detail stays as a secondary line, with an optional Retry.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof InlineNotice>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
  args: { label: 'No node state reported for this table' },
};

export const ErrorWithDetailAndRetry: Story = {
  args: {
    label: "Couldn't load this table's node data",
    detail: 'request failed: context deadline exceeded',
    tone: 'danger',
    onRetry: fn(),
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Couldn't load this table's node data")).toBeInTheDocument();
    await expect(canvas.getByText('request failed: context deadline exceeded')).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Retry' }));
    await expect(args.onRetry).toHaveBeenCalledOnce();
  },
};

export const IndentedUnderNode: Story = {
  args: { label: 'No active partitions reported on this node', indent: true },
};
