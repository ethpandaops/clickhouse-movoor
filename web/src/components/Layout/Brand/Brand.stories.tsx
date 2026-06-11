import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { Brand } from './Brand';

const meta = {
  title: 'Components/Layout/Brand',
  component: Brand,
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
        component: 'Product brand block with the animated logo tile, mono wordmark, and tagline.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof Brand>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('clickhouse-movoor')).toBeInTheDocument();
    await expect(canvas.getByText('ClickHouse partition tiering')).toBeInTheDocument();
  },
};

export const TruncatesInNarrowContainer: Story = {
  render: () => (
    <div className="w-32 overflow-hidden rounded-md border border-border p-2">
      <Brand />
    </div>
  ),
};
