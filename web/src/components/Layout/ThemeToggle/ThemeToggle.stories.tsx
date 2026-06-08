import type { Meta, StoryObj } from '@storybook/react-vite';
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
