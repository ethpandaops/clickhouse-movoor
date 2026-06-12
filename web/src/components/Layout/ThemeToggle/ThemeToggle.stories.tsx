import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, waitFor, within } from 'storybook/test';
import { ThemeToggle } from './ThemeToggle';

const meta = {
  title: 'Components/Layout/ThemeToggle',
  component: ThemeToggle,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-md bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'centered',
    docs: {
      description: {
        component: 'A single button that cycles through light, dark, and system theme modes.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof ThemeToggle>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};

export const CyclesThroughModes: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // Storybook's theme global starts the provider in system mode.
    const button = canvas.getByRole('button', { name: /System theme/ });

    await userEvent.click(button);
    await waitFor(() => {
      expect(canvas.getByRole('button', { name: /Light theme — click for dark theme/ })).toBeInTheDocument();
    });

    await userEvent.click(button);
    await waitFor(() => {
      expect(canvas.getByRole('button', { name: /Dark theme — click for system theme/ })).toBeInTheDocument();
    });

    await userEvent.click(button);
    await waitFor(() => {
      expect(canvas.getByRole('button', { name: /System theme — click for light theme/ })).toBeInTheDocument();
    });
  },
};
