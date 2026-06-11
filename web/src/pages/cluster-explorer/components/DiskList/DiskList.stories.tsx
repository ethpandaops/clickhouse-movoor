import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { DiskList } from './DiskList';

const meta = {
  title: 'Pages/ClusterExplorer/DiskList',
  component: DiskList,
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
        component: 'Disk badges for a partition or part. The tiering target disk is tinted to stand out.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof DiskList>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SplitAcrossDisks: Story = {
  args: {
    disks: ['default', 's3_cache'],
    targetDisk: 's3_cache',
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('default')).toBeInTheDocument();
    await expect(canvas.getByText('s3_cache')).toBeInTheDocument();
  },
};

export const OnTargetOnly: Story = {
  args: {
    disks: ['s3_cache'],
    targetDisk: 's3_cache',
  },
};

export const NoTargetConfigured: Story = {
  args: {
    disks: ['default', 'fast_nvme'],
  },
};

export const Empty: Story = {
  args: { disks: [] },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('no disk')).toBeInTheDocument();
  },
};
