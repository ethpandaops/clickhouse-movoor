import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { ConditionBadges } from './ConditionBadges';

const meta = {
  title: 'Pages/ClusterExplorer/ConditionBadges',
  component: ConditionBadges,
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
        component: 'Compact "N conditions" badge, toned by the most severe condition attached to a row.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof ConditionBadges>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SingleWarning: Story = {
  args: {
    conditions: [
      {
        severity: 'warning',
        code: 'partition_split_disks',
        message: 'one partition has active parts on multiple disks',
      },
    ],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('1')).toBeInTheDocument();
  },
};

export const CriticalDominates: Story = {
  args: {
    conditions: [
      { severity: 'info', code: 'fyi', message: 'informational note' },
      { severity: 'warning', code: 'split', message: 'split partition' },
      { severity: 'critical', code: 'replica_down', message: 'replica session expired' },
    ],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('3')).toBeInTheDocument();
  },
};

export const InfoOnly: Story = {
  args: {
    conditions: [{ severity: 'info', code: 'fyi', message: 'informational note' }],
  },
};

export const NoConditions: Story = {
  args: { conditions: [] },
  play: async ({ canvasElement }) => {
    await expect(canvasElement.querySelector('span')).toBeNull();
  },
};
