import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import {
  storyHistory,
  storyInFlightLegs,
  storyOperations,
  storyTieringPlan,
  storyTieringPlanWithIssues,
  storyTieringStatus,
} from '../../story-fixtures';
import { TieringSummaryBar } from './TieringSummaryBar';

const collection = {
  collectedAt: '2026-06-08T12:00:00Z',
  partial: false,
  collectionDurationMs: 42,
  nodesExpected: 4,
  nodesResponded: 4,
  nodesFailed: 0,
  warnings: [],
};

const meta = {
  title: 'Pages/ClusterExplorer/TieringSummaryBar',
  component: TieringSummaryBar,
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
        component:
          'Controller status strip: mode and pause state, headline stats, status/decision badge histogram, pause/resume control, and the collapsible live-activity drawer.',
      },
    },
  },
  tags: ['autodocs'],
  args: {
    plan: storyTieringPlan,
    status: storyTieringStatus,
    watchedTables: 2,
    nodeCount: 4,
    collection,
    activityOpen: false,
    operations: [],
    recent: [],
    loading: false,
    error: undefined,
    mutating: false,
    onPause: fn(),
    onResume: fn(),
    onToggleActivity: fn(),
    onJumpToPartition: fn(),
    onRetryTiering: fn(),
    onDismissControlError: fn(),
    controlError: null,
  },
} satisfies Meta<typeof TieringSummaryBar>;

export default meta;
type Story = StoryObj<typeof meta>;

export const PlanModeRunning: Story = {
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('plan')).toBeInTheDocument();
    await expect(canvas.getByText('running')).toBeInTheDocument();
    await expect(canvas.getByText(/tier 1/)).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Pause' }));
    await expect(args.onPause).toHaveBeenCalledOnce();
  },
};

export const PausedByOperator: Story = {
  args: {
    status: { ...storyTieringStatus, pauseState: 'stopped', pauseReason: 'operator' },
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('stopped')).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Resume' }));
    await expect(args.onResume).toHaveBeenCalledOnce();
    await expect(args.onPause).not.toHaveBeenCalled();
  },
};

export const EnforceMode: Story = {
  args: {
    status: { ...storyTieringStatus, mode: 'enforce', inFlight: storyInFlightLegs, bytesInFlight: '2621440' },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('enforce')).toBeInTheDocument();
    await expect(canvas.getByText('2.5 MiB')).toBeInTheDocument();
  },
};

export const ControllerOff: Story = {
  args: {
    status: { ...storyTieringStatus, mode: 'off' },
    plan: { tables: [], items: [] },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('off')).toBeInTheDocument();
    await expect(canvas.getByText('empty')).toBeInTheDocument();
  },
};

export const WithTableErrors: Story = {
  args: {
    plan: storyTieringPlanWithIssues,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('table errors')).toBeInTheDocument();
    await expect(canvas.getByText(/stalled 1/)).toBeInTheDocument();
    await expect(canvas.getByText(/misconfigured 1/)).toBeInTheDocument();
  },
};

export const PartialCollection: Story = {
  args: {
    collection: { ...collection, partial: true, nodesResponded: 3, nodesFailed: 1 },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('3/4')).toBeInTheDocument();
  },
};

export const ActivityDrawerOpen: Story = {
  args: {
    activityOpen: true,
    status: { ...storyTieringStatus, inFlight: storyInFlightLegs, bytesInFlight: '2621440' },
    operations: storyOperations,
    recent: storyHistory,
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('active now')).toBeInTheDocument();
    await expect(canvas.getByText('recent actions')).toBeInTheDocument();
    // Supervised legs are labelled manual; failed history rows show the outcome.
    await expect(canvas.getByText('manual')).toBeInTheDocument();
    await expect(canvas.getByText('failed')).toBeInTheDocument();
    await expect(canvas.getByText('62%')).toBeInTheDocument();

    // Both the in-flight leg and its live operation row reference the same
    // partition; the legs list renders first.
    const jumpTargets = canvas.getAllByRole('button', { name: /events_by_month_local 202601/ });
    await userEvent.click(jumpTargets[0]!);
    await expect(args.onJumpToPartition).toHaveBeenCalledWith(
      'movoor_dev',
      'events_by_month_local',
      'shard1-replica0',
      '202601'
    );
  },
};

export const DrawerEmpty: Story = {
  args: {
    activityOpen: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('nothing in flight')).toBeInTheDocument();
    await expect(canvas.getByText('none yet')).toBeInTheDocument();
  },
};

export const Loading: Story = {
  args: {
    plan: { tables: [], items: [] },
    status: undefined,
    collection: undefined,
    loading: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getAllByText('loading').length).toBeGreaterThan(0);
  },
};

export const ControllerUnavailable: Story = {
  args: {
    error: { detail: 'tiering controller is not configured' },
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('Tiering controller unavailable')).toBeInTheDocument();
    await expect(canvas.getByText('tiering controller is not configured')).toBeInTheDocument();
    // The bar degrades in place: last-known state stays visible (marked
    // stale) and the pause control survives the outage.
    await expect(canvas.getByText('stale')).toBeInTheDocument();
    await expect(canvas.getByText('Showing last-known controller state.')).toBeInTheDocument();
    await expect(canvas.getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Retry' }));
    await expect(args.onRetryTiering).toHaveBeenCalledOnce();
  },
};

export const ControllerNeverLoaded: Story = {
  args: {
    error: { detail: 'tiering controller is not configured' },
    status: undefined,
    plan: { tables: [], items: [] },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('unavailable')).toBeInTheDocument();
    // Without a last-known state there is no pause control and no fake
    // "empty" claim on the badge rail.
    await expect(canvas.queryByRole('button', { name: 'Pause' })).toBeNull();
    await expect(canvas.queryByText('empty')).toBeNull();
  },
};

export const PauseFailedAlert: Story = {
  args: {
    controlError: { action: 'pause', message: 'controller is draining and cannot accept control changes' },
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Couldn't pause tiering")).toBeInTheDocument();
    await expect(canvas.getByText('controller is draining and cannot accept control changes')).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Dismiss' }));
    await expect(args.onDismissControlError).toHaveBeenCalledOnce();
  },
};

export const DrawerQueriesFailing: Story = {
  args: {
    activityOpen: true,
    operationsError: { detail: 'system.moves query timed out' },
    historyError: { detail: 'history store query failed' },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // A failed query must not render as "nothing in flight" calm.
    await expect(
      canvas.getByText(/Couldn't fetch live operations — system\.moves query timed out/)
    ).toBeInTheDocument();
    await expect(canvas.getByText(/Couldn't fetch recent actions — history store query failed/)).toBeInTheDocument();
    await expect(canvas.queryByText('nothing in flight')).toBeNull();
    await expect(canvas.queryByText('none yet')).toBeNull();
  },
};

export const CollectionFreshness: Story = {
  args: {
    collectionUpdatedAt: Date.now() - 8_000,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText(/updated \d+s ago/)).toBeInTheDocument();
  },
};

export const MutationInFlight: Story = {
  args: {
    mutating: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole('button', { name: 'Pause' })).toBeDisabled();
  },
};
