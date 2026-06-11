import { http, HttpResponse, type HttpHandler } from 'msw';
import type {
  Node,
  Operation,
  TableDetail,
  TableListItem,
  TablePart,
  TieringHistoryEntry,
  TieringInFlightLeg,
  TieringMode,
  TieringPlanResponse,
  TieringStatus,
} from '@/api/types.gen';

const collectedAt = '2026-06-08T12:00:00Z';

const collection = {
  collectedAt,
  partial: false,
  collectionDurationMs: 42,
  nodesExpected: 4,
  nodesResponded: 4,
  nodesFailed: 0,
  warnings: [],
};

const partialCollection = {
  ...collection,
  partial: true,
  nodesResponded: 3,
  nodesFailed: 1,
  warnings: [
    {
      kind: 'reachability' as const,
      code: 'node_unreachable',
      message: 'dial tcp 127.0.0.1:9001: connect: connection refused',
      nodeId: 'shard0-replica1',
    },
  ],
};

export const storyNodes: Node[] = [
  {
    nodeId: 'shard0-replica0',
    shard: '0',
    replica: '0',
    endpoint: 'clickhouse://localhost:9000',
    reachable: true,
    observedAt: collectedAt,
    version: '25.8.11.66',
    timezone: 'UTC',
    uptimeSeconds: '3842',
    lastError: null,
  },
  {
    nodeId: 'shard0-replica1',
    shard: '0',
    replica: '1',
    endpoint: 'clickhouse://localhost:9001',
    reachable: true,
    observedAt: collectedAt,
    version: '25.8.11.66',
    timezone: 'UTC',
    uptimeSeconds: '3839',
    lastError: null,
  },
  {
    nodeId: 'shard1-replica0',
    shard: '1',
    replica: '0',
    endpoint: 'clickhouse://localhost:9002',
    reachable: true,
    observedAt: collectedAt,
    version: '25.8.11.66',
    timezone: 'UTC',
    uptimeSeconds: '3828',
    lastError: null,
  },
  {
    nodeId: 'shard1-replica1',
    shard: '1',
    replica: '1',
    endpoint: 'clickhouse://localhost:9003',
    reachable: true,
    observedAt: collectedAt,
    version: '25.8.11.66',
    timezone: 'UTC',
    uptimeSeconds: '3825',
    lastError: null,
  },
];

export const storyTables: TableListItem[] = [
  {
    database: 'movoor_dev',
    table: 'test_generic_network_month_local',
    engine: 'ReplicatedReplacingMergeTree',
    storagePolicy: 'movoor_tiered',
    targetDisk: 's3_cache',
    partitionKey: '(network_id, toYYYYMM(event_time))',
    sortingKey: 'network_id, event_time, record_id',
    primaryKey: 'network_id, event_time, record_id',
    versionColumn: null,
    nodesObserved: 4,
    shardsObserved: 2,
    replicasPerShard: 2,
    activePartitions: 6,
    activeParts: '24',
    rows: '14400',
    bytesOnDisk: '18874368',
    partitionPlacements: {
      on_target: 2,
      off_target: 2,
      split: 1,
      replica_divergent: 0,
      missing_replica: 0,
      unknown: 1,
    },
    partitionOperations: {
      moving: 0,
      merging: 1,
      mutating: 0,
      fetching: 0,
    },
    activeOperations: 1,
    conditions: [
      {
        severity: 'warning',
        code: 'partition_split_disks',
        message: 'one partition has active parts on multiple disks',
      },
    ],
    links: {
      partEvents: '/api/v1/part-events?database=movoor_dev&table=test_generic_network_month_local',
    },
  },
  {
    database: 'movoor_dev',
    table: 'events_by_month_local',
    engine: 'MergeTree',
    storagePolicy: 'default_and_s3_cache',
    targetDisk: 's3_cache',
    partitionKey: 'toYYYYMM(event_date)',
    sortingKey: 'event_date, entity_id',
    primaryKey: 'event_date',
    versionColumn: null,
    nodesObserved: 4,
    shardsObserved: 2,
    replicasPerShard: 2,
    activePartitions: 4,
    activeParts: '16',
    rows: '6400',
    bytesOnDisk: '7340032',
    partitionPlacements: {
      on_target: 1,
      off_target: 2,
      split: 0,
      replica_divergent: 0,
      missing_replica: 0,
      unknown: 1,
    },
    partitionOperations: {
      moving: 0,
      merging: 0,
      mutating: 0,
      fetching: 0,
    },
    activeOperations: 0,
    conditions: [],
    links: {
      partEvents: '/api/v1/part-events?database=movoor_dev&table=events_by_month_local',
    },
  },
];

export const storyTableDetails: Record<string, TableDetail> = {
  'movoor_dev.test_generic_network_month_local': {
    ...storyTables[0],
    uuid: '00000000-0000-0000-0000-000000000111',
    samplingKey: '',
    isReplicated: true,
    minPartition: "('mainnet','2026-01-08')",
    maxPartition: "('mainnet','2026-01-13')",
    lastModificationTime: '2026-06-08T11:42:00Z',
    nodes: storyNodes.map(node => ({
      nodeId: node.nodeId,
      engine: 'ReplicatedReplacingMergeTree',
      activeParts: node.nodeId === 'shard0-replica0' ? '6' : '5',
      rows: node.nodeId === 'shard0-replica0' ? '3600' : '3200',
      bytesOnDisk: node.nodeId === 'shard0-replica0' ? '4718592' : '4194304',
      replica: {
        readonly: false,
        sessionExpired: false,
        queueSize: node.nodeId === 'shard1-replica1' ? '2' : '0',
        absoluteDelaySeconds: node.nodeId === 'shard1-replica1' ? '7' : '0',
        totalReplicas: '2',
        activeReplicas: '2',
      },
    })),
  },
  'movoor_dev.events_by_month_local': {
    ...storyTables[1],
    uuid: '00000000-0000-0000-0000-000000000222',
    samplingKey: '',
    isReplicated: false,
    minPartition: '202601',
    maxPartition: '202604',
    lastModificationTime: '2026-06-08T10:02:00Z',
    nodes: storyNodes.map(node => ({
      nodeId: node.nodeId,
      engine: 'MergeTree',
      activeParts: '4',
      rows: '1600',
      bytesOnDisk: '1835008',
    })),
  },
};

export const storyParts: Record<string, TablePart[]> = {
  'movoor_dev.test_generic_network_month_local': [
    part(
      'shard0-replica0',
      '0',
      '0',
      "('mainnet',202602)",
      '2a2b3c4d5e6f00112233445566778899',
      'all_3_3_0',
      's3_cache',
      '600',
      '786432'
    ),
    part(
      'shard0-replica0',
      '0',
      '0',
      "('mainnet',202601)",
      '1a2b3c4d5e6f00112233445566778899',
      'all_1_1_0',
      's3_cache',
      '1200',
      '1572864'
    ),
    part(
      'shard0-replica0',
      '0',
      '0',
      "('mainnet',202602)",
      '2a2b3c4d5e6f00112233445566778899',
      'all_2_2_0',
      'default',
      '600',
      '786432'
    ),
    part(
      'shard0-replica1',
      '0',
      '1',
      "('mainnet',202601)",
      '1a2b3c4d5e6f00112233445566778899',
      'all_1_1_0',
      's3_cache',
      '1200',
      '1572864'
    ),
    part(
      'shard1-replica0',
      '1',
      '0',
      "('mainnet',202603)",
      '3a2b3c4d5e6f00112233445566778899',
      'all_4_4_0',
      'default',
      '900',
      '1179648'
    ),
    part(
      'shard1-replica1',
      '1',
      '1',
      "('mainnet',202603)",
      '3a2b3c4d5e6f00112233445566778899',
      'all_4_4_0',
      'default',
      '900',
      '1179648'
    ),
  ],
  'movoor_dev.events_by_month_local': [
    part('shard0-replica0', '0', '0', '202601', '202601', '202601_1_1_0', 'default', '1600', '1835008'),
    part('shard0-replica1', '0', '1', '202601', '202601', '202601_1_1_0', 'default', '1600', '1835008'),
    part('shard1-replica0', '1', '0', '202602', '202602', '202602_2_2_0', 's3_cache', '1600', '1835008'),
    part('shard1-replica1', '1', '1', '202602', '202602', '202602_2_2_0', 's3_cache', '1600', '1835008'),
  ],
};

export const storyTieringStatus: TieringStatus = {
  mode: 'plan',
  pauseState: 'running',
  inFlight: [],
  maxConcurrentPartitions: 1,
  maxMovesPerCycle: 4,
  maxBytesInFlight: '536870912000',
  bytesInFlight: '0',
  maxBytesPerDay: '2199023255552',
  bytesMovedToday: '1572864',
  updatedAt: collectedAt,
};

export const storyInFlightLegs: TieringInFlightLeg[] = [
  {
    nodeId: 'shard1-replica0',
    database: 'movoor_dev',
    table: 'events_by_month_local',
    partition: '202601',
    partitionId: '202601',
    action: 'tier',
    bytes: '1835008',
    startedAt: '2026-06-08T11:58:30Z',
    source: 'dispatch',
  },
  {
    nodeId: 'shard0-replica0',
    database: 'movoor_dev',
    table: 'test_generic_network_month_local',
    partition: "('mainnet',202602)",
    partitionId: '2a2b3c4d5e6f00112233445566778899',
    action: 'append',
    bytes: '786432',
    startedAt: '2026-06-08T11:59:45Z',
    source: 'supervised',
  },
];

export const storyOperations: Operation[] = [
  {
    operationId: 'op-move-1',
    kind: 'move',
    nodeId: 'shard1-replica0',
    database: 'movoor_dev',
    table: 'events_by_month_local',
    partition: '202601',
    partitionId: '202601',
    attemptId: 'attempt-move-1',
    state: 'moving part',
    elapsedSeconds: 42.5,
    progress: 0.62,
    sourceDisk: 'default',
    targetDisk: 's3_cache',
    bytesTotal: '1835008',
    bytesProcessed: '1137704',
    startedAt: '2026-06-08T11:59:20Z',
  },
  {
    operationId: 'op-merge-1',
    kind: 'merge',
    nodeId: 'shard0-replica0',
    database: 'movoor_dev',
    table: 'test_generic_network_month_local',
    partition: "('mainnet',202602)",
    partitionId: '2a2b3c4d5e6f00112233445566778899',
    attemptId: 'attempt-merge-1',
    state: 'merging parts',
    elapsedSeconds: 3.1,
    startedAt: '2026-06-08T11:59:58Z',
  },
];

export const storyHistory: TieringHistoryEntry[] = [
  {
    time: '2026-06-08T11:57:02Z',
    nodeId: 'shard0-replica0',
    database: 'movoor_dev',
    table: 'test_generic_network_month_local',
    partition: "('mainnet',202601)",
    partitionId: '1a2b3c4d5e6f00112233445566778899',
    action: 'tier',
    outcome: 'success',
    durationMs: 8421,
    bytes: '1572864',
    attemptId: 'attempt-history-1',
  },
  {
    time: '2026-06-08T11:52:40Z',
    nodeId: 'shard1-replica1',
    database: 'movoor_dev',
    table: 'events_by_month_local',
    partition: '202602',
    partitionId: '202602',
    action: 'optimize',
    outcome: 'failed',
    durationMs: 1204,
    bytes: '0',
    error: 'OPTIMIZE cancelled: too many merges queued',
    attemptId: 'attempt-history-2',
  },
];

export const storyTieringPlan: TieringPlanResponse = {
  tables: [
    {
      nodeId: 'shard0-replica0',
      database: 'movoor_dev',
      table: 'test_generic_network_month_local',
      reconciledAt: collectedAt,
      tickDurationMs: 22,
      generation: '00000000-0000-0000-0000-000000000111:tuple-v1',
      lastError: null,
      partitions: 3,
      actionable: 2,
      conditions: [],
    },
    {
      nodeId: 'shard1-replica0',
      database: 'movoor_dev',
      table: 'events_by_month_local',
      reconciledAt: collectedAt,
      tickDurationMs: 19,
      generation: '00000000-0000-0000-0000-000000000222:month-v1',
      lastError: null,
      partitions: 2,
      actionable: 1,
      conditions: [],
    },
  ],
  items: [
    tieringPartition(
      'shard0-replica0',
      '0',
      '0',
      'test_generic_network_month_local',
      "('mainnet',202601)",
      '1a2b3c4d5e6f00112233445566778899',
      'tiered',
      'none',
      'all active parts are already on the target disk',
      '1200',
      '1572864',
      '1',
      [{ disk: 's3_cache', parts: '1' }]
    ),
    tieringPartition(
      'shard0-replica0',
      '0',
      '0',
      'test_generic_network_month_local',
      "('mainnet',202602)",
      '2a2b3c4d5e6f00112233445566778899',
      'split',
      'append',
      'split partition is under fragmentation ceiling',
      '1200',
      '1572864',
      '2',
      [
        { disk: 'default', parts: '1' },
        { disk: 's3_cache', parts: '1' },
      ]
    ),
    tieringPartition(
      'shard1-replica0',
      '1',
      '0',
      'events_by_month_local',
      '202601',
      '202601',
      'ready',
      'tier',
      'partition is cold, sealed, and still on hot storage',
      '1600',
      '1835008',
      '1',
      [{ disk: 'default', parts: '1' }]
    ),
    {
      ...tieringPartition(
        'shard1-replica0',
        '1',
        '0',
        'test_generic_network_month_local',
        "('mainnet',202603)",
        '3a2b3c4d5e6f00112233445566778899',
        'hot',
        'hold',
        'waiting for newer partition insert evidence',
        '900',
        '1179648',
        '1',
        [{ disk: 'default', parts: '1' }]
      ),
      hold: {
        gate: 'successor-activity',
        window: '24h0m0s',
        lastInsertAt: '2026-06-08T09:00:00Z',
        releasesAt: '2026-06-09T09:00:00Z',
      },
    },
  ],
};

/** Plan variant where reconcilers are failing: stalled/misconfigured rows and a table error. */
export const storyTieringPlanWithIssues: TieringPlanResponse = {
  tables: [
    { ...storyTieringPlan.tables[0]!, lastError: 'storage policy movoor_tiered missing on shard1-replica0' },
    storyTieringPlan.tables[1]!,
  ],
  items: [
    storyTieringPlan.items[0]!,
    {
      ...storyTieringPlan.items[1]!,
      status: 'stalled',
      decision: 'none',
      reason: 'move failed: insufficient space on target disk',
      hold: { gate: 'stalled', retryAt: '2026-06-08T13:00:00Z', failures: 2 },
      conditions: [
        {
          severity: 'warning',
          code: 'move_failed',
          message: 'last move attempt failed',
          observedAt: collectedAt,
        },
      ],
    },
    {
      ...storyTieringPlan.items[2]!,
      status: 'misconfigured',
      decision: 'none',
      reason: 'target disk s3_cache is not part of storage policy default',
      conditions: [
        {
          severity: 'critical',
          code: 'target_disk_missing',
          message: 'target disk is not in the table storage policy',
          observedAt: collectedAt,
        },
      ],
    },
  ],
};

export interface ClusterExplorerHandlerOptions {
  /** One node failed collection: partial banner + unreachable node. */
  partial?: boolean;
  /** No watched tables and an empty plan. */
  empty?: boolean;
  /** Controller pause state: stopped by operator. */
  paused?: boolean;
  /** Tiering mode override (default "plan"). */
  mode?: TieringMode;
  /** In-flight legs, live operations, and recent history for the drawer. */
  busy?: boolean;
  /** Replication degradation: readonly + lagging replica on top of partial. */
  degraded?: boolean;
  /** Reconciler failures: stalled/misconfigured rows plus a table error. */
  tieringIssues?: boolean;
  /** Tiering endpoints answer 503 while collection works. */
  tieringUnavailable?: boolean;
  /** Pause/resume and apply mutate in-closure state across refetches. */
  stateful?: boolean;
  /** Apply rejects with a 409 stale-token conflict. */
  applyFails?: boolean;
  /** Pause rejects with a 503 while the controller drains. */
  pauseFails?: boolean;
  /** Drawer endpoints (operations + history) answer 500. */
  drawerFails?: boolean;
}

/** MSW handler set for the cluster explorer API, shaped by scenario options. */
export function clusterExplorerHandlers(options: ClusterExplorerHandlerOptions = {}): HttpHandler[] {
  const partial = options.partial === true || options.degraded === true;
  const currentCollection = partial ? partialCollection : collection;
  const nodes = partial
    ? storyNodes.map(node =>
        node.nodeId === 'shard0-replica1'
          ? { ...node, reachable: false, lastError: 'dial tcp 127.0.0.1:9001: connect: connection refused' }
          : node
      )
    : storyNodes;

  const plan = options.tieringIssues ? storyTieringPlanWithIssues : storyTieringPlan;
  let paused = options.paused === true;

  const statusBody = (): TieringStatus => ({
    ...storyTieringStatus,
    mode: options.mode ?? storyTieringStatus.mode,
    pauseState: paused ? 'stopped' : 'running',
    ...(paused ? { pauseReason: 'operator' } : {}),
    inFlight: options.busy ? storyInFlightLegs : [],
    bytesInFlight: options.busy ? '2621440' : '0',
  });

  const tableDetails: Record<string, TableDetail> = options.degraded
    ? Object.fromEntries(
        Object.entries(storyTableDetails).map(([key, detail]) => [
          key,
          {
            ...detail,
            nodes: detail.nodes.map(state =>
              state.nodeId === 'shard1-replica1' && state.replica
                ? {
                    ...state,
                    replica: {
                      ...state.replica,
                      readonly: true,
                      sessionExpired: true,
                      queueSize: '128',
                      absoluteDelaySeconds: '861',
                      activeReplicas: '1',
                    },
                  }
                : state
            ),
          },
        ])
      )
    : storyTableDetails;

  const tieringHandlers: HttpHandler[] = options.tieringUnavailable
    ? [
        http.get('/api/v1/tiering/status', () => problem503()),
        http.get('/api/v1/tiering/plan', () => problem503()),
        http.get('/api/v1/tiering/history', () => problem503()),
        http.get('/api/v1/operations', () => problem503()),
      ]
    : [
        http.get('/api/v1/tiering/status', () => HttpResponse.json(statusBody())),
        http.get('/api/v1/tiering/plan', () => HttpResponse.json(options.empty ? { tables: [], items: [] } : plan)),
        http.get('/api/v1/tiering/history', () =>
          options.drawerFails
            ? problem(500, 'Internal Server Error', 'history store query failed')
            : HttpResponse.json({ items: options.busy ? storyHistory : [] })
        ),
        http.get('/api/v1/operations', () =>
          options.drawerFails
            ? problem(500, 'Internal Server Error', 'system.moves query timed out')
            : HttpResponse.json({
                collection: currentCollection,
                items: options.busy ? storyOperations : [],
              })
        ),
        http.post('/api/v1/tiering/pause', () => {
          if (options.pauseFails) {
            return problem(503, 'Service Unavailable', 'controller is draining and cannot accept control changes');
          }
          if (options.stateful) {
            paused = true;
          }
          return HttpResponse.json(
            { ...statusBody(), pauseState: 'stopped', pauseReason: 'operator' },
            { status: 202 }
          );
        }),
        http.post('/api/v1/tiering/resume', () => {
          if (options.stateful) {
            paused = false;
          }
          return HttpResponse.json({ ...statusBody(), pauseState: 'running' }, { status: 202 });
        }),
        http.post('/api/v1/tiering/tables/:database/:table/partitions/:partitionId/apply', ({ params }) =>
          options.applyFails
            ? problem(409, 'Conflict', 'state token is stale: the partition advanced since this plan row was rendered')
            : HttpResponse.json(
                {
                  item: {
                    time: collectedAt,
                    nodeId: 'shard0-replica0',
                    database: params.database,
                    table: params.table,
                    partition: '',
                    partitionId: params.partitionId,
                    action: 'tier',
                    outcome: 'success',
                    durationMs: 1000,
                    bytes: '1572864',
                    attemptId: 'storybook-attempt',
                  },
                },
                { status: 202 }
              )
        ),
        http.post('/api/v1/tiering/tables/:database/:table/partitions/:partitionId/retry', ({ params }) =>
          HttpResponse.json(
            {
              item: {
                time: collectedAt,
                nodeId: 'shard0-replica0',
                database: params.database,
                table: params.table,
                partition: '',
                partitionId: params.partitionId,
                action: 'tier',
                outcome: 'success',
                durationMs: 1000,
                bytes: '1572864',
                attemptId: 'storybook-retry',
              },
            },
            { status: 202 }
          )
        ),
      ];

  return [
    http.get('/api/v1/nodes', () =>
      HttpResponse.json({
        collection: currentCollection,
        items: nodes,
      })
    ),
    http.get('/api/v1/tables', () =>
      HttpResponse.json({
        collection: currentCollection,
        items: options.empty ? [] : storyTables,
      })
    ),
    ...tieringHandlers,
    http.get('/api/v1/tables/:database/:table/parts', ({ params }) => {
      const key = `${params.database}.${params.table}`;

      return HttpResponse.json({
        collection: currentCollection,
        database: params.database,
        table: params.table,
        items: storyParts[key] ?? [],
      });
    }),
    http.get('/api/v1/tables/:database/:table', ({ params }) => {
      const key = `${params.database}.${params.table}`;
      const detail = tableDetails[key];

      if (!detail) {
        return HttpResponse.json(
          { type: 'about:blank', title: 'Not Found', status: 404, detail: 'not found' },
          { status: 404 }
        );
      }

      return HttpResponse.json({
        collection: currentCollection,
        item: detail,
      });
    }),
  ];
}

/** Handlers for a synthetic many-table cluster to eyeball density and scrolling. */
export function largeClusterHandlers(tableCount: number): HttpHandler[] {
  const tables: TableListItem[] = Array.from({ length: tableCount }, (_, index) => ({
    ...storyTables[index % 2]!,
    table: `synthetic_table_${String(index + 1).padStart(2, '0')}_local`,
    activePartitions: 4 + (index % 9),
    activeParts: String(16 + index * 3),
    rows: String(6400 * (index + 1)),
    bytesOnDisk: String(7340032n * BigInt(index + 1)),
    conditions: index % 5 === 0 ? storyTables[0]!.conditions : [],
  }));

  const items: TieringPlanResponse['items'] = tables.flatMap((table, index) =>
    index % 3 === 0
      ? [
          tieringPartition(
            'shard0-replica0',
            '0',
            '0',
            table.table,
            `2026${String((index % 4) + 1).padStart(2, '0')}`,
            `2026${String((index % 4) + 1).padStart(2, '0')}`,
            'ready',
            'tier',
            'partition is cold, sealed, and still on hot storage',
            table.rows,
            table.bytesOnDisk,
            '1',
            [{ disk: 'default', parts: '1' }]
          ),
        ]
      : []
  );

  return [
    http.get('/api/v1/nodes', () => HttpResponse.json({ collection, items: storyNodes })),
    http.get('/api/v1/tables', () => HttpResponse.json({ collection, items: tables })),
    http.get('/api/v1/tiering/status', () => HttpResponse.json(storyTieringStatus)),
    http.get('/api/v1/tiering/plan', () => HttpResponse.json({ tables: [], items })),
    http.get('/api/v1/tiering/history', () => HttpResponse.json({ items: [] })),
    http.get('/api/v1/operations', () => HttpResponse.json({ collection, items: [] })),
  ];
}

function problem(status: number, title: string, detail: string): Response {
  return HttpResponse.json({ type: 'about:blank', title, status, detail }, { status });
}

function problem503(): Response {
  return problem(503, 'Service Unavailable', 'tiering controller is not configured');
}

function part(
  nodeId: string,
  shard: string,
  replica: string,
  partitionValue: string,
  partitionId: string,
  partName: string,
  disk: string,
  rows: string,
  bytesOnDisk: string
): TablePart {
  return {
    nodeId,
    shard,
    replica,
    database: 'movoor_dev',
    table: partName.startsWith('2026') ? 'events_by_month_local' : 'test_generic_network_month_local',
    partition: partitionValue,
    partitionId,
    partName,
    active: true,
    disk,
    path: `/var/lib/clickhouse/store/${partitionId}/${partName}`,
    partType: 'Wide',
    rows,
    bytesOnDisk,
    modificationTime: '2026-06-08T11:40:00Z',
    minBlockNumber: partName.split('_')[1] ?? '1',
    maxBlockNumber: partName.split('_')[2] ?? '1',
    level: partName.split('_')[3] ?? '0',
    conditions:
      partitionId === '2a2b3c4d5e6f00112233445566778899'
        ? [{ severity: 'warning', code: 'part_disk_split', message: 'partition has parts on multiple disks' }]
        : [],
  };
}

function tieringPartition(
  nodeId: string,
  shard: string,
  replica: string,
  table: string,
  partition: string,
  partitionId: string,
  status: TieringPlanResponse['items'][number]['status'],
  decision: TieringPlanResponse['items'][number]['decision'],
  reason: string,
  rows: string,
  bytesOnDisk: string,
  activeParts: string,
  disks: TieringPlanResponse['items'][number]['disks']
): TieringPlanResponse['items'][number] {
  return {
    nodeId,
    shard,
    replica,
    database: 'movoor_dev',
    table,
    partition,
    partitionId,
    status,
    decision,
    reason,
    rows,
    bytesOnDisk,
    activeParts,
    disks,
    targetDisk: 's3_cache',
    hotVolume: 'hot',
    policy: {
      mode: 'plan',
      ageBasis: table === 'events_by_month_local' ? 'partitionTime' : 'frontier',
      olderThan: '840h0m0s',
      field: table === 'events_by_month_local' ? undefined : 'block_number',
      keepLast: table === 'events_by_month_local' ? undefined : '250',
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
    stateToken: `${nodeId}-${partitionId}-token`,
    reconciledAt: collectedAt,
    effectiveMode: 'plan',
  };
}
