package server

import (
	"cmp"
	"slices"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
)

type tableAggregate struct {
	database             string
	table                string
	engine               string
	storagePolicy        string
	targetDisk           string
	partitionKey         string
	sortingKey           string
	primaryKey           string
	versionColumn        *string
	uuid                 string
	samplingKey          string
	isReplicated         bool
	nodesObserved        int
	shardsObserved       int
	replicasPerShard     int
	activePartitions     int
	activeParts          uint64
	rows                 uint64
	bytesOnDisk          uint64
	minPartition         *string
	maxPartition         *string
	lastModificationTime *time.Time
	partitionPlacements  map[string]int
	partitionOperations  map[string]int
	activeOperations     int
	conditions           []clusterstate.Condition
	nodes                []clusterstate.TableState
}

type partitionAggregate struct {
	database             string
	table                string
	partition            string
	partitionID          string
	targetDisk           string
	placement            string
	operations           []string
	disks                map[string]struct{}
	activeParts          uint64
	rows                 uint64
	bytesOnDisk          uint64
	lastModificationTime *time.Time
	placements           map[string]*partitionPlacementAggregate
	conditions           []clusterstate.Condition
}

type partitionPlacementAggregate struct {
	node                 chclient.Node
	disk                 string
	activeParts          uint64
	rows                 uint64
	bytesOnDisk          uint64
	lastModificationTime *time.Time
}

//nolint:gocognit // Aggregation intentionally keeps table rollup dimensions together.
func aggregateTables(items []clusterstate.TableState, conditions []clusterstate.Condition) []tableAggregate {
	// Aggregate in node-ID order so first-observed fields (engine, storage
	// policy, ...) are deterministic when replicas disagree; collection
	// results arrive in completion order otherwise.
	ordered := slices.Clone(items)
	slices.SortStableFunc(ordered, func(a, b clusterstate.TableState) int {
		return cmp.Compare(a.Node.ID, b.Node.ID)
	})

	grouped := make(map[string]*tableAggregate)
	for _, item := range ordered {
		key := item.Database + "\x00" + item.Table
		aggregate, ok := grouped[key]
		if !ok {
			aggregate = &tableAggregate{
				database:             item.Database,
				table:                item.Table,
				engine:               item.Engine,
				storagePolicy:        item.StoragePolicy,
				partitionKey:         item.PartitionKey,
				sortingKey:           item.SortingKey,
				primaryKey:           item.PrimaryKey,
				uuid:                 item.UUID,
				samplingKey:          item.SamplingKey,
				isReplicated:         item.IsReplicated,
				partitionPlacements:  map[string]int{"unknown": 0},
				partitionOperations:  map[string]int{},
				lastModificationTime: item.LastModificationTime,
			}
			grouped[key] = aggregate
		}
		aggregate.nodes = append(aggregate.nodes, item)
		aggregate.activeParts += item.ActiveParts
		aggregate.rows += item.Rows
		aggregate.bytesOnDisk += item.BytesOnDisk
		if item.ActivePartitions > aggregate.activePartitions {
			aggregate.activePartitions = item.ActivePartitions
		}
		if aggregate.minPartition == nil || stringLess(item.MinPartition, aggregate.minPartition) {
			aggregate.minPartition = item.MinPartition
		}
		if stringGreater(item.MaxPartition, aggregate.maxPartition) {
			aggregate.maxPartition = item.MaxPartition
		}
		if timeGreater(item.LastModificationTime, aggregate.lastModificationTime) {
			aggregate.lastModificationTime = item.LastModificationTime
		}
	}

	for _, aggregate := range grouped {
		nodes := make(map[string]struct{})
		shards := make(map[string]struct{})
		replicasByShard := make(map[string]map[string]struct{})
		for _, node := range aggregate.nodes {
			nodes[node.Node.ID] = struct{}{}
			shards[node.Node.Shard] = struct{}{}
			if _, ok := replicasByShard[node.Node.Shard]; !ok {
				replicasByShard[node.Node.Shard] = make(map[string]struct{})
			}
			replicasByShard[node.Node.Shard][node.Node.Replica] = struct{}{}
		}
		aggregate.nodesObserved = len(nodes)
		aggregate.shardsObserved = len(shards)
		for _, replicas := range replicasByShard {
			if len(replicas) > aggregate.replicasPerShard {
				aggregate.replicasPerShard = len(replicas)
			}
		}
		aggregate.partitionPlacements["unknown"] = aggregate.activePartitions
		aggregate.conditions = tableScopedConditions(conditions, aggregate.database, aggregate.table)
	}

	out := make([]tableAggregate, 0, len(grouped))
	for _, aggregate := range grouped {
		out = append(out, *aggregate)
	}

	return out
}

func aggregateTableDetail(items []clusterstate.TableState, conditions []clusterstate.Condition, watch clusterstate.Watch) (tableAggregate, bool) {
	for _, item := range aggregateTables(items, conditions) {
		if item.database == watch.Database && item.table == watch.Table {
			return item, true
		}
	}

	return tableAggregate{}, false
}

func aggregatePartitions(parts []clusterstate.Part, conditions []clusterstate.Condition) []partitionAggregate {
	ordered := slices.Clone(parts)
	slices.SortStableFunc(ordered, func(a, b clusterstate.Part) int {
		return cmp.Compare(a.Node.ID, b.Node.ID)
	})

	grouped := make(map[string]*partitionAggregate)
	for _, part := range ordered {
		if !part.Active {
			continue
		}
		key := part.Database + "\x00" + part.Table + "\x00" + part.PartitionID
		aggregate, ok := grouped[key]
		if !ok {
			aggregate = &partitionAggregate{
				database:    part.Database,
				table:       part.Table,
				partition:   part.Partition,
				partitionID: part.PartitionID,
				placement:   "unknown",
				disks:       make(map[string]struct{}),
				placements:  make(map[string]*partitionPlacementAggregate),
			}
			grouped[key] = aggregate
		}
		aggregate.disks[part.Disk] = struct{}{}
		aggregate.activeParts++
		aggregate.rows += part.Rows
		aggregate.bytesOnDisk += part.BytesOnDisk
		if timeGreater(&part.ModificationTime, aggregate.lastModificationTime) {
			aggregate.lastModificationTime = &part.ModificationTime
		}

		placementKey := part.Node.ID + "\x00" + part.Disk
		placement, ok := aggregate.placements[placementKey]
		if !ok {
			placement = &partitionPlacementAggregate{
				node: part.Node,
				disk: part.Disk,
			}
			aggregate.placements[placementKey] = placement
		}
		placement.activeParts++
		placement.rows += part.Rows
		placement.bytesOnDisk += part.BytesOnDisk
		if timeGreater(&part.ModificationTime, placement.lastModificationTime) {
			placement.lastModificationTime = &part.ModificationTime
		}
	}

	out := make([]partitionAggregate, 0, len(grouped))
	for _, aggregate := range grouped {
		aggregate.conditions = partitionScopedConditions(conditions, aggregate.database, aggregate.table, aggregate.partitionID)
		out = append(out, *aggregate)
	}

	return out
}

func tableScopedConditions(items []clusterstate.Condition, database string, table string) []clusterstate.Condition {
	out := make([]clusterstate.Condition, 0)
	for _, item := range items {
		if deref(item.Database) == database && deref(item.Table) == table && item.PartitionID == nil {
			out = append(out, item)
		}
	}

	return out
}

func partitionScopedConditions(items []clusterstate.Condition, database string, table string, partitionID string) []clusterstate.Condition {
	out := make([]clusterstate.Condition, 0)
	for _, item := range items {
		if deref(item.Database) == database && deref(item.Table) == table && deref(item.PartitionID) == partitionID {
			out = append(out, item)
		}
	}

	return out
}

func deref(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func stringLess(left *string, right *string) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}

	return *left < *right
}

func stringGreater(left *string, right *string) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}

	return *left > *right
}

func timeGreater(left *time.Time, right *time.Time) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}

	return left.After(*right)
}
