import type { CollectionMeta, Node, NodeTableState, TableDetail, TableListItem, TablePart } from '@/api/types.gen';
import { sumStrings, toBigInt } from '@/utils/format';

/** One node's slice of an expanded table: cluster identity, observed state, partitions. */
export interface NodeGroup {
  nodeId: string;
  node?: Node;
  state?: NodeTableState;
  partitions: PartitionGroup[];
}

/** All active parts of one partition on one node, rolled up for the partition row. */
export interface PartitionGroup {
  key: string;
  partition: string;
  partitionId: string;
  disks: string[];
  rows: string;
  bytesOnDisk: string;
  lastModificationTime?: string;
  parts: TablePart[];
}

const naturalCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

export function naturalCompare(a: string, b: string): number {
  return naturalCollator.compare(a, b);
}

export function tableKey(table: Pick<TableListItem, 'database' | 'table'>): string {
  return `${table.database}.${table.table}`;
}

export function nodeKey(tableKeyValue: string, nodeId: string): string {
  return `${tableKeyValue}/${nodeId}`;
}

export function partitionKey(tableKeyValue: string, nodeId: string, partitionId: string): string {
  return `${tableKeyValue}/${nodeId}/${partitionId}`;
}

/** Immutable Set toggle for the expand/collapse state maps. */
export function toggleSetMember(current: Set<string>, key: string): Set<string> {
  const next = new Set(current);
  if (next.has(key)) {
    next.delete(key);
  } else {
    next.add(key);
  }

  return next;
}

/**
 * Merge cluster nodes, per-table node state, and parts into one NodeGroup per
 * node. Nodes only seen via parts get a synthetic Node so the row still renders.
 */
export function buildNodeGroups(
  nodeById: Map<string, Node>,
  detail: TableDetail | undefined,
  parts: TablePart[]
): NodeGroup[] {
  const groups = new Map<string, NodeGroup>();

  for (const node of nodeById.values()) {
    groups.set(node.nodeId, { nodeId: node.nodeId, node, partitions: [] });
  }

  for (const state of detail?.nodes ?? []) {
    const existing = groups.get(state.nodeId);
    groups.set(state.nodeId, {
      nodeId: state.nodeId,
      node: existing?.node,
      state,
      partitions: existing?.partitions ?? [],
    });
  }

  for (const part of parts) {
    const existing = groups.get(part.nodeId);
    groups.set(part.nodeId, {
      nodeId: part.nodeId,
      node: existing?.node ?? {
        nodeId: part.nodeId,
        shard: part.shard,
        replica: part.replica,
        endpoint: '',
        reachable: true,
        observedAt: part.modificationTime,
        lastError: null,
      },
      state: existing?.state,
      partitions: existing?.partitions ?? [],
    });
  }

  for (const group of groups.values()) {
    group.partitions = buildPartitionGroups(group.nodeId, parts);
  }

  return [...groups.values()].sort(compareNodeGroups);
}

/** Group one node's parts by partition id, rolling up disks, rows, and bytes. */
export function buildPartitionGroups(nodeId: string, parts: TablePart[]): PartitionGroup[] {
  const grouped = new Map<string, TablePart[]>();

  for (const part of parts) {
    if (part.nodeId !== nodeId) {
      continue;
    }

    const current = grouped.get(part.partitionId) ?? [];
    current.push(part);
    grouped.set(part.partitionId, current);
  }

  return [...grouped.entries()]
    .map(([partitionId, partitionParts]) => {
      const sortedParts = [...partitionParts].sort(compareParts);
      const lastModificationTime = sortedParts
        .map(part => part.modificationTime)
        .sort()
        .at(-1);

      return {
        key: partitionId,
        partition: sortedParts[0]?.partition ?? partitionId,
        partitionId,
        disks: [...new Set(sortedParts.map(part => part.disk))].sort(),
        rows: sumStrings(sortedParts.map(part => part.rows)),
        bytesOnDisk: sumStrings(sortedParts.map(part => part.bytesOnDisk)),
        lastModificationTime,
        parts: sortedParts,
      };
    })
    .sort(comparePartitionGroups);
}

export function compareTables(a: TableListItem, b: TableListItem): number {
  return naturalCompare(a.database, b.database) || naturalCompare(a.table, b.table);
}

export function compareNodes(
  a: Pick<Node, 'nodeId' | 'shard' | 'replica'>,
  b: Pick<Node, 'nodeId' | 'shard' | 'replica'>
): number {
  return naturalCompare(a.shard, b.shard) || naturalCompare(a.replica, b.replica) || naturalCompare(a.nodeId, b.nodeId);
}

function compareNodeGroups(a: NodeGroup, b: NodeGroup): number {
  return compareNodes(
    { nodeId: a.nodeId, shard: a.node?.shard ?? '', replica: a.node?.replica ?? '' },
    { nodeId: b.nodeId, shard: b.node?.shard ?? '', replica: b.node?.replica ?? '' }
  );
}

function comparePartitionGroups(a: PartitionGroup, b: PartitionGroup): number {
  return naturalCompare(a.partition, b.partition) || naturalCompare(a.partitionId, b.partitionId);
}

/** Parts order by block range then level so merge lineage reads top-down. */
export function compareParts(a: TablePart, b: TablePart): number {
  return (
    compareOptionalBigInt(a.minBlockNumber, b.minBlockNumber) ||
    compareOptionalBigInt(a.maxBlockNumber, b.maxBlockNumber) ||
    compareOptionalBigInt(a.level, b.level) ||
    naturalCompare(a.partName, b.partName) ||
    naturalCompare(a.disk, b.disk) ||
    naturalCompare(a.path, b.path)
  );
}

function compareOptionalBigInt(a: string | undefined, b: string | undefined): number {
  if (a === undefined && b === undefined) {
    return 0;
  }
  if (a === undefined) {
    return 1;
  }
  if (b === undefined) {
    return -1;
  }

  const left = toBigInt(a);
  const right = toBigInt(b);
  if (left < right) {
    return -1;
  }
  if (left > right) {
    return 1;
  }

  return 0;
}

/** Prefer the partial collection so degraded snapshots win the banner. */
export function strongestCollection(collections: Array<CollectionMeta | undefined>): CollectionMeta | undefined {
  return collections
    .filter((collection): collection is CollectionMeta => collection !== undefined)
    .sort((a, b) => Number(b.partial) - Number(a.partial))[0];
}
