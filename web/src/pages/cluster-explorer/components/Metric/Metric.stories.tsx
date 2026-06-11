import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { Metric } from './Metric';

const meta = {
  title: 'Pages/ClusterExplorer/Metric',
  component: Metric,
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
        component: 'Right-aligned tabular figure cell. Integer mode applies locale digit grouping safely past 2^53.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof Metric>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Integer: Story = {
  args: { value: '14400' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(new Intl.NumberFormat().format(14400))).toBeInTheDocument();
  },
};

export const HugeInteger: Story = {
  args: { value: '18446744073709551615' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('18446744073709551615')).toBeInTheDocument();
  },
};

export const Text: Story = {
  args: { value: '18 MiB', format: 'text' },
};

export const Placeholder: Story = {
  args: { value: '-' },
};
