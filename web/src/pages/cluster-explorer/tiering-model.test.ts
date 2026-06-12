import { describe, expect, it } from 'vitest';
import type { TieringPlanResponse } from '@/api/types.gen';
import {
  buildTieringIndex,
  decisionTone,
  isActionableDecision,
  operationTone,
  statusTone,
  tableErrorCount,
  tieringCounts,
} from './tiering-model';

type PlanItem = TieringPlanResponse['items'][number];

function makeItem(overrides: Partial<PlanItem>): PlanItem {
  return {
    nodeId: 'shard0-replica0',
    shard: '0',
    replica: '0',
    database: 'movoor_dev',
    table: 'events',
    partition: '202601',
    partitionId: '202601',
    status: 'ready',
    decision: 'tier',
    reason: 'cold and sealed',
    rows: '100',
    bytesOnDisk: '1024',
    activeParts: '1',
    disks: [{ disk: 'default', parts: '1' }],
    targetDisk: 's3_cache',
    policy: {
      mode: 'plan',
      ageBasis: 'partitionTime',
      quietFor: '24h0m0s',
      tierFrozenAfter: '720h0m0s',
      targetDisk: 's3_cache',
      optimizeToParts: '1',
      skipOptimize: false,
      optimizeOn: 'hot',
      optimizeSkipAboveBytes: '0',
      resplitStrategy: 'auto',
      resplitQuietFor: '168h0m0s',
      fragmentAbovePartCount: '6',
    },
    conditions: [],
    stateToken: 'token',
    reconciledAt: '2026-06-08T12:00:00Z',
    effectiveMode: 'plan',
    ...overrides,
  };
}

describe('isActionableDecision', () => {
  it('flags only the four mutating decisions', () => {
    for (const decision of ['tier', 'append', 'consolidate', 'optimize']) {
      expect(isActionableDecision(decision)).toBe(true);
    }
    for (const decision of ['none', 'keep', 'hold']) {
      expect(isActionableDecision(decision)).toBe(false);
    }
  });
});

describe('buildTieringIndex', () => {
  it('indexes partitions and rolls counters up to node and table', () => {
    const plan: TieringPlanResponse = {
      tables: [],
      items: [
        makeItem({ partitionId: 'p1', decision: 'tier', status: 'ready' }),
        makeItem({ partitionId: 'p2', decision: 'none', status: 'tiered' }),
        makeItem({ partitionId: 'p3', decision: 'hold', status: 'split' }),
        makeItem({ partitionId: 'p4', nodeId: 'shard1-replica0', decision: 'none', status: 'tiered' }),
      ],
    };

    const index = buildTieringIndex(plan);
    expect(index.byPartition.get('movoor_dev.events/shard0-replica0/p1')?.decision).toBe('tier');

    const tableRollup = index.byTable.get('movoor_dev.events');
    expect(tableRollup).toEqual({ total: 4, actionable: 1, tiered: 2, split: 1, held: 1 });

    const nodeRollup = index.byNode.get('movoor_dev.events/shard0-replica0');
    expect(nodeRollup).toEqual({ total: 3, actionable: 1, tiered: 1, split: 1, held: 1 });
  });
});

describe('tieringCounts', () => {
  it('builds status and decision histograms', () => {
    const counts = tieringCounts([
      makeItem({ status: 'ready', decision: 'tier' }),
      makeItem({ status: 'ready', decision: 'hold' }),
      makeItem({ status: 'tiered', decision: 'none' }),
    ]);

    expect(counts.status).toEqual({ ready: 2, tiered: 1 });
    expect(counts.decision).toEqual({ tier: 1, hold: 1, none: 1 });
  });
});

describe('tableErrorCount', () => {
  it('counts only tables with a lastError', () => {
    const table = {
      nodeId: 'shard0-replica0',
      database: 'movoor_dev',
      table: 'events',
      reconciledAt: '2026-06-08T12:00:00Z',
      tickDurationMs: 10,
      generation: 'gen',
      lastError: null,
      partitions: 1,
      actionable: 0,
      conditions: [],
    };

    expect(tableErrorCount({ tables: [table, { ...table, lastError: 'boom' }], items: [] })).toBe(1);
  });
});

describe('tones', () => {
  it('maps statuses to badge tones', () => {
    expect(statusTone('ready')).toBe('success');
    expect(statusTone('stalled')).toBe('warning');
    expect(statusTone('misconfigured')).toBe('danger');
    expect(statusTone('tiered')).toBe('info');
    expect(statusTone('hot')).toBe('muted');
  });

  it('maps decisions to badge tones', () => {
    expect(decisionTone('tier')).toBe('warning');
    expect(decisionTone('keep')).toBe('muted');
    expect(decisionTone('hold')).toBe('info');
    expect(decisionTone('none')).toBe('muted');
  });

  it('maps operation kinds to badge tones', () => {
    expect(operationTone('move')).toBe('warning');
    expect(operationTone('mutation')).toBe('danger');
    expect(operationTone('fetch')).toBe('info');
    expect(operationTone('replication_queue')).toBe('muted');
  });
});
