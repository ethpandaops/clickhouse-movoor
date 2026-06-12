import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import { ProblemBanner } from './ProblemBanner';

const meta = {
  title: 'Pages/ClusterExplorer/ProblemBanner',
  component: ProblemBanner,
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
          'Page-level error banner shown when the table or node collection request fails. Plain-language headline first, raw backend detail second, Retry on hand.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof ProblemBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const WithDetailAndRetry: Story = {
  args: {
    title: "Couldn't reach the cluster",
    detail: 'no configured ClickHouse node responded',
    onRetry: fn(),
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Couldn't reach the cluster")).toBeInTheDocument();
    await expect(canvas.getByText('no configured ClickHouse node responded')).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Retry' }));
    await expect(args.onRetry).toHaveBeenCalledOnce();
  },
};

export const HeadlineOnly: Story = {
  args: { title: "Couldn't reach the cluster" },
};
