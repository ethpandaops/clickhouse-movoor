import type { Meta, StoryObj } from '@storybook/react-vite';
import { http, HttpResponse } from 'msw';
import { expect, userEvent, waitFor, within } from 'storybook/test';
import { ClusterExplorerPage } from './ClusterExplorerPage';
import { clusterExplorerHandlers, largeClusterHandlers } from './story-fixtures';

const meta = {
  title: 'Pages/ClusterExplorer',
  component: ClusterExplorerPage,
  decorators: [
    // No min-width here: the page is responsive and the MobileViewport story
    // must be allowed to lay out at true device width.
    Story => (
      <div className="bg-background p-4">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
  },
  tags: ['autodocs'],
} satisfies Meta<typeof ClusterExplorerPage>;

export default meta;
type Story = StoryObj<typeof meta>;

const expandedTestTable = {
  defaultExpandedTables: ['movoor_dev.test_generic_network_month_local'],
  defaultExpandedNodes: ['movoor_dev.test_generic_network_month_local/shard0-replica0'],
  defaultExpandedPartitions: [
    'movoor_dev.test_generic_network_month_local/shard0-replica0/2a2b3c4d5e6f00112233445566778899',
  ],
};

export const Populated: Story = {
  args: expandedTestTable,
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers(),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);

    await waitFor(() => {
      expect(canvas.getAllByTitle('movoor_dev.test_generic_network_month_local').length).toBeGreaterThan(0);
    });
    await waitFor(() => {
      expect(canvas.getAllByText('shard0-replica0').length).toBeGreaterThan(0);
    });
    await waitFor(() => {
      expect(canvas.getAllByText('Tiering').length).toBeGreaterThan(0);
    });
    await waitFor(() => {
      expect(
        canvas.getByRole('button', { name: /Apply append to movoor_dev\.test_generic_network_month_local/ })
      ).toBeInTheDocument();
    });
    expect(canvasElement.textContent).toContain('append');
    expect(canvasElement.textContent).toContain('tier');
    await waitFor(() => {
      expect(canvasElement.textContent).toContain("('mainnet',202602)");
    });
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('all_2_2_0');
      expect(canvasElement.textContent).toContain('all_3_3_0');
    });
    const content = canvasElement.textContent ?? '';
    expect(content.indexOf("('mainnet',202601)")).toBeLessThan(content.indexOf("('mainnet',202602)"));
    expect(content.indexOf('all_2_2_0')).toBeLessThan(content.indexOf('all_3_3_0'));
    expect(canvas.getByText('all_2_2_0')).toBeInTheDocument();
  },
};

export const PartialCollection: Story = {
  args: {
    defaultExpandedTables: ['movoor_dev.test_generic_network_month_local'],
  },
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ partial: true }),
    },
  },
  play: async ({ canvasElement }) => {
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('Partial collection');
    });
  },
};

export const DegradedReplication: Story = {
  args: {
    defaultExpandedTables: ['movoor_dev.test_generic_network_month_local'],
  },
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ degraded: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('Partial collection');
    });
    await waitFor(() => {
      expect(canvas.getAllByText('unreachable').length).toBeGreaterThan(0);
    });
    // shard1-replica1 carries a deep replication queue and replica lag.
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('queue 128 / lag 861');
    });
  },
};

export const TieringPaused: Story = {
  args: expandedTestTable,
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ paused: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getByText('stopped')).toBeInTheDocument();
      expect(canvas.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
    });
    // Manual applies are blocked while the controller is paused.
    await waitFor(() => {
      const apply = canvas.getByRole('button', { name: 'Tiering is paused — resume to apply' });
      expect(apply).toBeDisabled();
    });
  },
};

export const PauseResumeRoundTrip: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ stateful: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const pause = await waitFor(() => canvas.getByRole('button', { name: 'Pause' }));
    await userEvent.click(pause);
    await waitFor(() => {
      expect(canvas.getByText('stopped')).toBeInTheDocument();
    });
    await userEvent.click(canvas.getByRole('button', { name: 'Resume' }));
    await waitFor(() => {
      expect(canvas.getByText('running')).toBeInTheDocument();
      expect(canvas.getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    });
  },
};

export const ApplyMoveFlow: Story = {
  args: expandedTestTable,
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers(),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const apply = await waitFor(() =>
      canvas.getByRole('button', { name: /Apply append to movoor_dev\.test_generic_network_month_local/ })
    );
    await expect(apply).toBeEnabled();
    await userEvent.click(apply);
    // The refetched plan still carries the same state token, so the row stays
    // disabled until the controller actually advances it.
    await waitFor(() => {
      expect(apply).toBeDisabled();
    });
  },
};

export const LiveActivityDrawer: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ busy: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const toggle = await waitFor(() => canvas.getByRole('button', { name: 'Expand live tiering activity' }));
    await userEvent.click(toggle);

    await waitFor(() => {
      expect(canvas.getByText('active now')).toBeInTheDocument();
      expect(canvas.getByText('recent actions')).toBeInTheDocument();
      expect(canvas.getByText('manual')).toBeInTheDocument();
      expect(canvas.getByText('failed')).toBeInTheDocument();
    });

    // Jumping to an in-flight leg expands its table and node and lands on the
    // partition row (identified by its partition id, only rendered in-tree).
    await userEvent.click(
      canvas.getByRole('button', { name: /test_generic_network_month_local \('mainnet',202602\)/ })
    );
    await waitFor(() => {
      expect(canvas.getByText('2a2b3c4d5e6f00112233445566778899')).toBeInTheDocument();
    });
  },
};

export const EnforceModeBusy: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ mode: 'enforce', busy: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getByText('enforce')).toBeInTheDocument();
      expect(canvas.getByText('2.5 MiB')).toBeInTheDocument();
    });
  },
};

export const TieringIssues: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ tieringIssues: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getByText('table errors')).toBeInTheDocument();
      expect(canvas.getByText(/stalled 1/)).toBeInTheDocument();
      expect(canvas.getByText(/misconfigured 1/)).toBeInTheDocument();
    });
  },
};

export const TieringUnavailable: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ tieringUnavailable: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getByText('Tiering controller unavailable')).toBeInTheDocument();
      expect(canvas.getByText('tiering controller is not configured')).toBeInTheDocument();
    });
    // The bar degrades instead of collapsing, and offers a retry.
    await expect(canvas.getByText('unavailable')).toBeInTheDocument();
    await expect(canvas.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
    // The table tree still renders from the healthy collection endpoints.
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('test_generic_network_month_local');
    });
  },
};

export const ApplyMoveFails: Story = {
  args: expandedTestTable,
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ applyFails: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const apply = await waitFor(() =>
      canvas.getByRole('button', { name: /Apply append to movoor_dev\.test_generic_network_month_local/ })
    );
    await userEvent.click(apply);
    // The 409 rejection surfaces in place and the button re-enables for retry.
    await waitFor(() => {
      expect(canvas.getByRole('img', { name: /Apply failed: state token is stale/ })).toBeInTheDocument();
    });
    await expect(apply).toBeEnabled();
  },
};

export const PauseFails: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ pauseFails: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const pause = await waitFor(() => canvas.getByRole('button', { name: 'Pause' }));
    await userEvent.click(pause);
    await waitFor(() => {
      expect(canvas.getByText("Couldn't pause tiering")).toBeInTheDocument();
      expect(canvas.getByText('controller is draining and cannot accept control changes')).toBeInTheDocument();
    });
    // Still running — and the alert is dismissible.
    await expect(canvas.getByText('running')).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => {
      expect(canvas.queryByText("Couldn't pause tiering")).toBeNull();
    });
  },
};

export const DrawerUnavailable: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ drawerFails: true }),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const toggle = await waitFor(() => canvas.getByRole('button', { name: 'Expand live tiering activity' }));
    await userEvent.click(toggle);
    // Failed drawer queries must not masquerade as "nothing in flight".
    await waitFor(() => {
      expect(canvas.getByText(/Couldn't fetch live operations/)).toBeInTheDocument();
      expect(canvas.getByText(/Couldn't fetch recent actions/)).toBeInTheDocument();
    });
    await expect(canvas.queryByText('nothing in flight')).toBeNull();
  },
};

export const Empty: Story = {
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers({ empty: true }),
    },
  },
  play: async ({ canvasElement }) => {
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('No watched tables');
    });
  },
};

export const RequestFailed: Story = {
  parameters: {
    msw: {
      handlers: [
        http.get('/api/v1/nodes', () =>
          HttpResponse.json({
            collection: {
              collectedAt: '2026-06-08T12:00:00Z',
              partial: false,
              collectionDurationMs: 1,
              nodesExpected: 0,
              nodesResponded: 0,
              nodesFailed: 0,
              warnings: [],
            },
            items: [],
          })
        ),
        http.get('/api/v1/tables', () =>
          HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Service Unavailable',
              status: 503,
              detail: 'no configured ClickHouse node responded',
            },
            { status: 503 }
          )
        ),
      ],
    },
  },
  play: async ({ canvasElement }) => {
    await waitFor(() => {
      expect(canvasElement.textContent).toContain('no configured ClickHouse node responded');
    });
  },
};

export const LargeCluster: Story = {
  parameters: {
    msw: {
      handlers: largeClusterHandlers(14),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getByText('synthetic_table_01_local')).toBeInTheDocument();
      expect(canvas.getByText('synthetic_table_14_local')).toBeInTheDocument();
    });
  },
};

export const MobileViewport: Story = {
  args: expandedTestTable,
  globals: {
    viewport: { value: 'mobile1', isRotated: false },
  },
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers(),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getAllByTitle('movoor_dev.test_generic_network_month_local').length).toBeGreaterThan(0);
    });
  },
};

export const DarkMode: Story = {
  args: expandedTestTable,
  globals: {
    themeMode: 'dark',
  },
  parameters: {
    msw: {
      handlers: clusterExplorerHandlers(),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => {
      expect(canvas.getAllByText('shard0-replica0').length).toBeGreaterThan(0);
    });
  },
};

export const Loading: Story = {
  tags: ['test-exclude'],
  parameters: {
    msw: {
      handlers: [
        http.get('/api/v1/nodes', () => new Promise(() => undefined)),
        http.get('/api/v1/tables', () => new Promise(() => undefined)),
      ],
    },
  },
};
