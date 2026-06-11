import type { TieringPartition, TieringPlanResponse } from '@/api/types.gen';
import type { BadgeTone } from './components/Badge';
import { nodeKey, partitionKey, tableKey } from './explorer-model';

/** Per-table / per-node tiering counters for the rollup chips. */
export interface TieringRollup {
  total: number;
  actionable: number;
  tiered: number;
  split: number;
  held: number;
}

/** Plan items indexed for O(1) lookups while rendering the tree. */
export interface TieringIndex {
  byTable: Map<string, TieringRollup>;
  byNode: Map<string, TieringRollup>;
  byPartition: Map<string, TieringPartition>;
}

export function isActionableDecision(decision: string): boolean {
  return decision === 'tier' || decision === 'append' || decision === 'consolidate' || decision === 'optimize';
}

export function buildTieringIndex(plan: TieringPlanResponse): TieringIndex {
  const byTable = new Map<string, TieringRollup>();
  const byNode = new Map<string, TieringRollup>();
  const byPartition = new Map<string, TieringPartition>();

  for (const partition of plan.items) {
    const tableKeyValue = tableKey(partition);
    const nodeKeyValue = nodeKey(tableKeyValue, partition.nodeId);
    const partitionKeyValue = partitionKey(tableKeyValue, partition.nodeId, partition.partitionId);

    byPartition.set(partitionKeyValue, partition);
    addTieringRollup(byTable, tableKeyValue, partition);
    addTieringRollup(byNode, nodeKeyValue, partition);
  }

  return { byTable, byNode, byPartition };
}

function addTieringRollup(map: Map<string, TieringRollup>, key: string, partition: TieringPartition): void {
  const rollup = map.get(key) ?? { total: 0, actionable: 0, tiered: 0, split: 0, held: 0 };
  rollup.total += 1;
  if (isActionableDecision(partition.decision)) {
    rollup.actionable += 1;
  }
  if (partition.status === 'tiered') {
    rollup.tiered += 1;
  }
  if (partition.status === 'split') {
    rollup.split += 1;
  }
  if (partition.decision === 'hold') {
    rollup.held += 1;
  }
  map.set(key, rollup);
}

/** Status/decision histograms for the summary-bar badges. */
export function tieringCounts(items: TieringPartition[]): {
  status: Record<string, number>;
  decision: Record<string, number>;
} {
  const status: Record<string, number> = {};
  const decision: Record<string, number> = {};

  for (const item of items) {
    status[item.status] = (status[item.status] ?? 0) + 1;
    decision[item.decision] = (decision[item.decision] ?? 0) + 1;
  }

  return { status, decision };
}

export function tableErrorCount(plan: TieringPlanResponse): number {
  return plan.tables.filter(table => table.lastError).length;
}

export function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'ready':
      return 'success';
    case 'split':
    case 'stalled':
      return 'warning';
    case 'misconfigured':
      return 'danger';
    case 'tiered':
      return 'info';
    default:
      return 'muted';
  }
}

export function decisionTone(decision: string): BadgeTone {
  switch (decision) {
    case 'tier':
    case 'append':
    case 'consolidate':
    case 'optimize':
      return 'warning';
    case 'keep':
      return 'muted';
    case 'hold':
      return 'info';
    default:
      return 'muted';
  }
}

/** Badge tone for a live cluster operation kind. */
export function operationTone(kind: string): BadgeTone {
  switch (kind) {
    case 'merge':
    case 'move':
      return 'warning';
    case 'mutation':
      return 'danger';
    case 'fetch':
      return 'info';
    default:
      return 'muted';
  }
}
