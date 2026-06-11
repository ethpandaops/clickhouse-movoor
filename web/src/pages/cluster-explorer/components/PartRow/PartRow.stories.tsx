import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import type { TablePart } from '@/api/types.gen';
import { PartRow } from './PartRow';

function makePart(overrides: Partial<TablePart>): TablePart {
  return {
    nodeId: 'shard0-replica0',
    shard: '0',
    replica: '0',
    database: 'movoor_dev',
    table: 'test_generic_network_month_local',
    partition: "('mainnet',202601)",
    partitionId: '1a2b3c4d5e6f00112233445566778899',
    partName: 'all_1_1_0',
    active: true,
    disk: 's3_cache',
    path: '/var/lib/clickhouse/store/1a2b3c4d5e6f00112233445566778899/all_1_1_0',
    partType: 'Wide',
    rows: '1200',
    bytesOnDisk: '1572864',
    modificationTime: '2026-06-08T11:40:00Z',
    minBlockNumber: '1',
    maxBlockNumber: '1',
    level: '0',
    conditions: [],
    ...overrides,
  };
}

const meta = {
  title: 'Pages/ClusterExplorer/PartRow',
  component: PartRow,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
    docs: {
      description: {
        component: 'Leaf tree row: one active data part with its disk, block range, merge level, and size.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof PartRow>;

export default meta;
type Story = StoryObj<typeof meta>;

export const OnTargetDisk: Story = {
  args: {
    part: makePart({}),
    targetDisk: 's3_cache',
    trail: [false, false, true],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('all_1_1_0')).toBeInTheDocument();
    // The disk name renders in the disk badge and again in the mobile meta
    // line — assert presence, not uniqueness.
    expect(canvas.getAllByText('s3_cache').length).toBeGreaterThan(0);
  },
};

export const StrandedOnHotDisk: Story = {
  args: {
    part: makePart({
      partName: 'all_2_2_0',
      disk: 'default',
      path: '/var/lib/clickhouse/store/2a2b3c4d5e6f00112233445566778899/all_2_2_0',
      minBlockNumber: '2',
      maxBlockNumber: '2',
      conditions: [{ severity: 'warning', code: 'part_disk_split', message: 'partition has parts on multiple disks' }],
    }),
    targetDisk: 's3_cache',
    trail: [false, true, false],
  },
};

export const MergedHighLevelPart: Story = {
  args: {
    part: makePart({
      partName: 'all_1_24_3',
      minBlockNumber: '1',
      maxBlockNumber: '24',
      level: '3',
      rows: '288000',
      bytesOnDisk: '377487360',
      partType: 'Compact',
    }),
    targetDisk: 's3_cache',
    trail: [true, true, true],
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('all_1_24_3')).toBeInTheDocument();
  },
};
