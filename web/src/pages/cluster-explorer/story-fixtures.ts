import { http, HttpResponse, type HttpHandler } from 'msw';
import type { Node, TableDetail, TableListItem, TablePart } from '@/api/types.gen';

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

export function clusterExplorerHandlers(options: { partial?: boolean; empty?: boolean } = {}): HttpHandler[] {
  const currentCollection = options.partial ? partialCollection : collection;
  const nodes = options.partial
    ? storyNodes.map(node =>
        node.nodeId === 'shard0-replica1'
          ? { ...node, reachable: false, lastError: 'dial tcp 127.0.0.1:9001: connect: connection refused' }
          : node
      )
    : storyNodes;

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
    http.get('/api/v1/tables/:database/:table', ({ params }) => {
      const key = `${params.database}.${params.table}`;
      const detail = storyTableDetails[key];

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
    http.get('/api/v1/tables/:database/:table/parts', ({ params }) => {
      const key = `${params.database}.${params.table}`;

      return HttpResponse.json({
        collection: currentCollection,
        database: params.database,
        table: params.table,
        items: storyParts[key] ?? [],
      });
    }),
  ];
}
