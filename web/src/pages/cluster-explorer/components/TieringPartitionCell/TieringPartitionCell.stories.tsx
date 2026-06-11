import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, waitFor, within } from 'storybook/test';
import type { TieringPartition } from '@/api/types.gen';
import { TieringPartitionCell } from './TieringPartitionCell';

function makePartition(overrides: Partial<TieringPartition>): TieringPartition {
  return {
    nodeId: 'shard0-replica0',
    shard: '0',
    replica: '0',
    database: 'movoor_dev',
    table: 'events_by_month_local',
    partition: '202601',
    partitionId: '202601',
    status: 'ready',
    decision: 'tier',
    reason: 'partition is cold, sealed, and still on hot storage',
    rows: '1600',
    bytesOnDisk: '1835008',
    activeParts: '1',
    disks: [{ disk: 'default', parts: '1' }],
    targetDisk: 's3_cache',
    hotVolume: 'hot',
    policy: {
      mode: 'plan',
      ageBasis: 'partitionTime',
      olderThan: '840h0m0s',
      quietFor: '24h0m0s',
      tierFrozenAfter: '720h0m0s',
      targetDisk: 's3_cache',
      hotVolume: 'hot',
      optimizeToParts: '1',
      skipOptimize: false,
      optimizeOn: 'hot',
      optimizeSkipAboveBytes: '322122547200',
      resplitStrategy: 'auto',
      resplitQuietFor: '168h0m0s',
      fragmentAbovePartCount: '6',
    },
    conditions: [],
    stateToken: 'token-1',
    reconciledAt: '2026-06-08T12:00:00Z',
    effectiveMode: 'plan',
    ...overrides,
  };
}

const meta = {
  title: 'Pages/ClusterExplorer/TieringPartitionCell',
  component: TieringPartitionCell,
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
          'Partition tiering verdict: actionable decisions render an apply button (with the reason in a tooltip); passive verdicts render a quiet chip whose tooltip explains the gate holding it back.',
      },
    },
  },
  tags: ['autodocs'],
  args: { onApply: fn(), onRetry: fn(), paused: false, awaitingToken: undefined },
} satisfies Meta<typeof TieringPartitionCell>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ActionableMove: Story = {
  args: {
    partition: makePartition({}),
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    const button = canvas.getByRole('button', {
      name: 'Apply tier to movoor_dev.events_by_month_local 202601',
    });
    await expect(button).toBeEnabled();
    await expect(button).toHaveTextContent('move');
    await userEvent.click(button);
    await expect(args.onApply).toHaveBeenCalledOnce();
  },
};

export const Paused: Story = {
  args: {
    partition: makePartition({}),
    paused: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const button = canvas.getByRole('button', { name: 'Tiering is paused — resume to apply' });
    await expect(button).toBeDisabled();
  },
};

export const AwaitingRefresh: Story = {
  args: {
    partition: makePartition({ stateToken: 'token-clicked' }),
    awaitingToken: 'token-clicked',
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // The plan row still carries the token captured at click time, so the
    // button stays disabled until the refetch lands.
    await expect(canvas.getByRole('button')).toBeDisabled();
  },
};

export const HeldWithGateDetail: Story = {
  args: {
    partition: makePartition({
      status: 'hot',
      decision: 'hold',
      reason: 'waiting for newer partition insert evidence',
      hold: {
        gate: 'successor-activity',
        window: '24h0m0s',
        lastInsertAt: '2026-06-08T09:00:00Z',
        releasesAt: '2026-06-09T09:00:00Z',
      },
    }),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.hover(canvas.getByText('hold'));
    await waitFor(() => {
      expect(document.body.textContent).toContain('waiting for newer partition insert evidence');
      expect(document.body.textContent).toContain('gate: successor-activity');
    });
  },
};

export const AlreadyTiered: Story = {
  args: {
    partition: makePartition({
      status: 'tiered',
      decision: 'none',
      reason: 'all active parts are already on the target disk',
      disks: [{ disk: 's3_cache', parts: '1' }],
    }),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('tiered')).toBeInTheDocument();
    await expect(canvas.queryByRole('button')).toBeNull();
  },
};

export const StalledWithRetry: Story = {
  args: {
    partition: makePartition({
      status: 'stalled',
      decision: 'none',
      reason: 'move failed: insufficient space on target disk',
      hold: {
        gate: 'stalled',
        retryAt: '2026-06-08T13:00:00Z',
        failures: 2,
      },
      conditions: [
        {
          severity: 'warning',
          code: 'move_failed',
          message: 'last move attempt failed',
          observedAt: '2026-06-08T12:00:00Z',
        },
      ],
    }),
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    const retry = canvas.getByRole('button', { name: 'Retry movoor_dev.events_by_month_local 202601' });
    await expect(retry).toBeEnabled();
    await userEvent.click(retry);
    await expect(args.onRetry).toHaveBeenCalledOnce();
    await expect(args.onApply).not.toHaveBeenCalled();
  },
};

export const ApplyFailed: Story = {
  args: {
    partition: makePartition({}),
    applyError: 'state token is stale: the partition advanced since this plan row was rendered',
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    // The rejection is visible in place and the button re-enables for retry.
    await expect(canvas.getByRole('img', { name: /Apply failed: state token is stale/ })).toBeInTheDocument();
    const button = canvas.getByRole('button');
    await expect(button).toBeEnabled();
    await userEvent.click(button);
    await expect(args.onApply).toHaveBeenCalledOnce();
  },
};

export const NotInPlan: Story = {
  args: { partition: undefined },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('-')).toBeInTheDocument();
  },
};
