import { describe, expect, it } from 'vitest';
import type { Node, TablePart } from '@/api/types.gen';
import {
  buildNodeGroups,
  buildPartitionGroups,
  compareParts,
  nodeKey,
  partitionKey,
  strongestCollection,
  tableKey,
  toggleSetMember,
} from './explorer-model';

function makePart(overrides: Partial<TablePart>): TablePart {
  return {
    nodeId: 'shard0-replica0',
    shard: '0',
    replica: '0',
    database: 'movoor_dev',
    table: 'events',
    partition: '202601',
    partitionId: '202601',
    partName: '202601_1_1_0',
    active: true,
    disk: 'default',
    path: '/var/lib/clickhouse/store/202601/202601_1_1_0',
    partType: 'Wide',
    rows: '100',
    bytesOnDisk: '1024',
    modificationTime: '2026-06-08T11:40:00Z',
    minBlockNumber: '1',
    maxBlockNumber: '1',
    level: '0',
    conditions: [],
    ...overrides,
  };
}

function makeNode(overrides: Partial<Node>): Node {
  return {
    nodeId: 'shard0-replica0',
    shard: '0',
    replica: '0',
    endpoint: 'clickhouse://localhost:9000',
    reachable: true,
    observedAt: '2026-06-08T12:00:00Z',
    lastError: null,
    ...overrides,
  };
}

describe('keys', () => {
  it('compose hierarchically', () => {
    const table = tableKey({ database: 'db', table: 't' });
    expect(table).toBe('db.t');
    expect(nodeKey(table, 'n1')).toBe('db.t/n1');
    expect(partitionKey(table, 'n1', 'p1')).toBe('db.t/n1/p1');
  });
});

describe('toggleSetMember', () => {
  it('adds missing members and removes present ones without mutating', () => {
    const original = new Set(['a']);
    const added = toggleSetMember(original, 'b');
    expect([...added].sort()).toEqual(['a', 'b']);
    const removed = toggleSetMember(added, 'a');
    expect([...removed]).toEqual(['b']);
    expect([...original]).toEqual(['a']);
  });
});

describe('buildPartitionGroups', () => {
  it('groups parts by partition id, rolling up disks, rows, and bytes', () => {
    const parts = [
      makePart({ partName: '202601_1_1_0', disk: 'default', rows: '100', bytesOnDisk: '1000' }),
      makePart({
        partName: '202601_2_2_0',
        disk: 's3_cache',
        rows: '50',
        bytesOnDisk: '500',
        minBlockNumber: '2',
        maxBlockNumber: '2',
      }),
      makePart({ nodeId: 'shard1-replica0', partName: '202601_9_9_0' }),
    ];

    const groups = buildPartitionGroups('shard0-replica0', parts);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.disks).toEqual(['default', 's3_cache']);
    expect(groups[0]?.rows).toBe('150');
    expect(groups[0]?.bytesOnDisk).toBe('1500');
    expect(groups[0]?.parts.map(part => part.partName)).toEqual(['202601_1_1_0', '202601_2_2_0']);
  });

  it('sorts partitions naturally by partition value', () => {
    const parts = [
      makePart({ partition: '202610', partitionId: '202610', partName: '202610_1_1_0' }),
      makePart({ partition: '202602', partitionId: '202602', partName: '202602_1_1_0' }),
    ];

    const groups = buildPartitionGroups('shard0-replica0', parts);
    expect(groups.map(group => group.partition)).toEqual(['202602', '202610']);
  });
});

describe('compareParts', () => {
  it('orders by block range before level and name', () => {
    const early = makePart({ minBlockNumber: '2', maxBlockNumber: '2', partName: 'all_2_2_0' });
    const late = makePart({ minBlockNumber: '10', maxBlockNumber: '10', partName: 'all_10_10_0' });
    expect(compareParts(early, late)).toBeLessThan(0);
    expect(compareParts(late, early)).toBeGreaterThan(0);
  });

  it('sorts parts missing block numbers last', () => {
    const known = makePart({});
    const unknown = makePart({ minBlockNumber: undefined, maxBlockNumber: undefined });
    expect(compareParts(known, unknown)).toBeLessThan(0);
  });
});

describe('buildNodeGroups', () => {
  it('synthesises a node for parts from nodes the cluster list missed', () => {
    const nodeById = new Map([['shard0-replica0', makeNode({})]]);
    const parts = [makePart({ nodeId: 'shard9-replica9', shard: '9', replica: '9' })];

    const groups = buildNodeGroups(nodeById, undefined, parts);
    const synthetic = groups.find(group => group.nodeId === 'shard9-replica9');
    expect(synthetic?.node?.endpoint).toBe('');
    expect(synthetic?.partitions).toHaveLength(1);
  });

  it('sorts groups by shard then replica', () => {
    const nodeById = new Map([
      ['b', makeNode({ nodeId: 'b', shard: '1', replica: '0' })],
      ['a', makeNode({ nodeId: 'a', shard: '0', replica: '1' })],
      ['c', makeNode({ nodeId: 'c', shard: '0', replica: '0' })],
    ]);

    const groups = buildNodeGroups(nodeById, undefined, []);
    expect(groups.map(group => group.nodeId)).toEqual(['c', 'a', 'b']);
  });
});

describe('strongestCollection', () => {
  const fresh = {
    collectedAt: '2026-06-08T12:00:00Z',
    partial: false,
    collectionDurationMs: 1,
    nodesExpected: 2,
    nodesResponded: 2,
    nodesFailed: 0,
    warnings: [],
  };

  it('prefers partial collections so degraded snapshots surface', () => {
    const partial = { ...fresh, partial: true, nodesFailed: 1 };
    expect(strongestCollection([fresh, partial])).toBe(partial);
    expect(strongestCollection([partial, fresh])).toBe(partial);
  });

  it('skips undefined entries', () => {
    expect(strongestCollection([undefined, fresh])).toBe(fresh);
    expect(strongestCollection([undefined])).toBeUndefined();
  });
});
