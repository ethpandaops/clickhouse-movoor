package server

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
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

func (s *server) requireState(w http.ResponseWriter, r *http.Request) (StateReader, bool) {
	if s.state != nil {
		return s.state, true
	}

	s.writeProblem(w, r, problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusServiceUnavailable),
		Status:   http.StatusServiceUnavailable,
		Detail:   "cluster state collector is not configured",
		Instance: r.URL.RequestURI(),
	})

	return nil, false
}

func (s *server) requireWatch(w http.ResponseWriter, r *http.Request) (StateReader, clusterstate.Watch, bool) {
	state, ok := s.requireState(w, r)
	if !ok {
		return nil, clusterstate.Watch{}, false
	}

	watch := clusterstate.Watch{
		Database: r.PathValue("database"),
		Table:    r.PathValue("table"),
	}
	if slices.Contains(state.Watches(), watch) {
		return state, watch, true
	}

	s.writeProblem(w, r, problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusNotFound),
		Status:   http.StatusNotFound,
		Detail:   "table is not configured as a watch",
		Instance: r.URL.RequestURI(),
	})

	return nil, clusterstate.Watch{}, false
}

func (s *server) writeJSON(w http.ResponseWriter, r *http.Request, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.ErrorContext(r.Context(), "encode api response", "error", err)
	}
}

func (s *server) writeNoResponders(w http.ResponseWriter, r *http.Request, nodesExpected int, nodesResponded int) bool {
	if nodesExpected == 0 || nodesResponded > 0 {
		return false
	}

	s.writeProblem(w, r, problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusServiceUnavailable),
		Status:   http.StatusServiceUnavailable,
		Detail:   "no configured ClickHouse node responded",
		Instance: r.URL.RequestURI(),
	})

	return true
}

func collectionMeta[T any](result clusterstate.Result[T]) map[string]any {
	return map[string]any{
		"collectedAt":          result.CollectedAt,
		"partial":              result.Partial(),
		"collectionDurationMs": int(result.CollectionDuration.Milliseconds()),
		"nodesExpected":        result.NodesExpected,
		"nodesResponded":       result.NodesResponded,
		"nodesFailed":          result.NodesFailed,
		"warnings":             apiWarnings(result.Warnings),
	}
}

func apiWarnings(warnings []clusterstate.Warning) []map[string]any {
	items := make([]map[string]any, 0, len(warnings))
	for _, warning := range warnings {
		item := map[string]any{
			"kind":    warning.Kind,
			"code":    warning.Code,
			"message": warning.Message,
		}
		if warning.NodeID != "" {
			item["nodeId"] = warning.NodeID
		}
		items = append(items, item)
	}

	return items
}

func apiNode(item clusterstate.NodeStatus) map[string]any {
	node := apiNodeRef(item.Node)
	node["endpoint"] = "clickhouse://" + item.Node.Addr
	node["reachable"] = item.Reachable
	node["observedAt"] = item.ObservedAt
	if item.Version != "" {
		node["version"] = item.Version
	}
	if item.Timezone != "" {
		node["timezone"] = item.Timezone
	}
	if item.UptimeSeconds != 0 {
		node["uptimeSeconds"] = uint64String(item.UptimeSeconds)
	}
	if item.LastError == "" {
		node["lastError"] = nil
	} else {
		node["lastError"] = item.LastError
	}

	return node
}

func apiDisk(item clusterstate.Disk) map[string]any {
	disk := apiNodeRef(item.Node)
	disk["disk"] = item.Name
	disk["type"] = item.Type
	disk["objectStorageType"] = item.ObjectStorageType
	disk["isRemote"] = item.IsRemote
	disk["isBroken"] = item.IsBroken
	disk["path"] = nilIfEmpty(item.Path)
	disk["cachePath"] = nilIfEmpty(item.CachePath)
	disk["capacityKnown"] = item.CapacityKnown
	disk["freeSpaceBytes"] = nullableUInt64String(item.FreeSpaceBytes)
	disk["totalSpaceBytes"] = nullableUInt64String(item.TotalSpaceBytes)
	disk["unreservedSpaceBytes"] = nullableUInt64String(item.UnreservedSpaceBytes)
	disk["usedByActivePartsBytes"] = uint64String(item.UsedByActiveParts)

	return disk
}

func apiNodeRef(node chclient.Node) map[string]any {
	return map[string]any{
		"nodeId":  node.ID,
		"shard":   node.Shard,
		"replica": node.Replica,
	}
}

func apiTableListItem(item tableAggregate) map[string]any {
	table := apiTableBase(item)
	table["nodesObserved"] = item.nodesObserved
	table["shardsObserved"] = item.shardsObserved
	table["replicasPerShard"] = item.replicasPerShard
	table["activePartitions"] = item.activePartitions
	table["activeParts"] = uint64String(item.activeParts)
	table["rows"] = uint64String(item.rows)
	table["bytesOnDisk"] = uint64String(item.bytesOnDisk)
	table["partitionPlacements"] = fixedPlacementCounts(item.partitionPlacements)
	table["partitionOperations"] = fixedOperationCounts(item.partitionOperations)
	table["activeOperations"] = item.activeOperations
	table["conditions"] = apiConditions(item.conditions)
	table["links"] = map[string]string{
		"partEvents": "/api/v1/part-events?database=" + urlQueryEscape(item.database) + "&table=" + urlQueryEscape(item.table),
	}

	return table
}

func apiTableDetail(item tableAggregate) map[string]any {
	table := apiTableBase(item)
	table["uuid"] = item.uuid
	table["samplingKey"] = item.samplingKey
	table["isReplicated"] = item.isReplicated
	table["nodesObserved"] = item.nodesObserved
	table["activePartitions"] = item.activePartitions
	table["activeParts"] = uint64String(item.activeParts)
	table["rows"] = uint64String(item.rows)
	table["bytesOnDisk"] = uint64String(item.bytesOnDisk)
	table["minPartition"] = item.minPartition
	table["maxPartition"] = item.maxPartition
	table["lastModificationTime"] = item.lastModificationTime
	table["partitionPlacements"] = fixedPlacementCounts(item.partitionPlacements)
	table["partitionOperations"] = fixedOperationCounts(item.partitionOperations)
	table["nodes"] = apiNodeTableStates(item.nodes)
	table["conditions"] = apiConditions(item.conditions)

	return table
}

func apiTableBase(item tableAggregate) map[string]any {
	return map[string]any{
		"database":      item.database,
		"table":         item.table,
		"engine":        item.engine,
		"storagePolicy": item.storagePolicy,
		"targetDisk":    item.targetDisk,
		"partitionKey":  item.partitionKey,
		"sortingKey":    item.sortingKey,
		"primaryKey":    item.primaryKey,
		"versionColumn": item.versionColumn,
	}
}

func apiNodeTableStates(items []clusterstate.TableState) []map[string]any {
	states := make([]map[string]any, 0, len(items))
	for _, item := range items {
		state := map[string]any{
			"nodeId":      item.Node.ID,
			"engine":      item.Engine,
			"activeParts": uint64String(item.ActiveParts),
			"rows":        uint64String(item.Rows),
			"bytesOnDisk": uint64String(item.BytesOnDisk),
		}
		if item.Replica != nil {
			state["replica"] = map[string]any{
				"readonly":             item.Replica.Readonly,
				"sessionExpired":       item.Replica.SessionExpired,
				"queueSize":            uint64String(item.Replica.QueueSize),
				"absoluteDelaySeconds": uint64String(item.Replica.AbsoluteDelaySeconds),
				"totalReplicas":        uint64String(item.Replica.TotalReplicas),
				"activeReplicas":       uint64String(item.Replica.ActiveReplicas),
			}
		}
		states = append(states, state)
	}
	sortMapItems(states, "nodeId", "")

	return states
}

func apiColumn(column clusterstate.Column) map[string]any {
	return map[string]any{
		"name":              column.Name,
		"position":          column.Position,
		"type":              column.Type,
		"kind":              column.Kind,
		"defaultKind":       column.DefaultKind,
		"defaultExpression": column.DefaultExpression,
		"codecExpression":   column.CodecExpression,
		"ttlExpression":     column.TTLExpression,
		"comment":           column.Comment,
		"isInPartitionKey":  column.IsInPartitionKey,
		"isInSortingKey":    column.IsInSortingKey,
		"isInPrimaryKey":    column.IsInPrimaryKey,
		"isInSamplingKey":   column.IsInSamplingKey,
	}
}

func apiPartition(item partitionAggregate) map[string]any {
	placements := make([]map[string]any, 0, len(item.placements))
	for _, placement := range item.placements {
		nodePlacement := apiNodeRef(placement.node)
		nodePlacement["disk"] = placement.disk
		nodePlacement["activeParts"] = uint64String(placement.activeParts)
		nodePlacement["rows"] = uint64String(placement.rows)
		nodePlacement["bytesOnDisk"] = uint64String(placement.bytesOnDisk)
		nodePlacement["lastModificationTime"] = placement.lastModificationTime
		placements = append(placements, nodePlacement)
	}
	sortMapItems(placements, "nodeId", "disk")

	return map[string]any{
		"database":             item.database,
		"table":                item.table,
		"partition":            item.partition,
		"partitionId":          item.partitionID,
		"targetDisk":           item.targetDisk,
		"placement":            item.placement,
		"operations":           item.operations,
		"disks":                sortedStringSet(item.disks),
		"activeParts":          uint64String(item.activeParts),
		"rows":                 uint64String(item.rows),
		"bytesOnDisk":          uint64String(item.bytesOnDisk),
		"lastModificationTime": item.lastModificationTime,
		"placements":           placements,
		"conditions":           apiConditions(item.conditions),
	}
}

func apiPart(item clusterstate.Part) map[string]any {
	part := apiNodeRef(item.Node)
	part["database"] = item.Database
	part["table"] = item.Table
	part["partition"] = item.Partition
	part["partitionId"] = item.PartitionID
	part["partName"] = item.Name
	part["uuid"] = item.UUID
	part["active"] = item.Active
	part["disk"] = item.Disk
	part["path"] = item.Path
	part["partType"] = item.PartType
	part["rows"] = uint64String(item.Rows)
	part["marks"] = uint64String(item.Marks)
	part["bytesOnDisk"] = uint64String(item.BytesOnDisk)
	part["dataCompressedBytes"] = uint64String(item.DataCompressedBytes)
	part["dataUncompressedBytes"] = uint64String(item.DataUncompressedBytes)
	part["marksBytes"] = uint64String(item.MarksBytes)
	part["primaryKeyBytesInMemory"] = uint64String(item.PrimaryKeyBytesInMemory)
	part["primaryKeyBytesInMemoryAllocated"] = uint64String(item.PrimaryKeyBytesInMemoryAllocated)
	part["secondaryIndicesCompressedBytes"] = uint64String(item.SecondaryIndicesCompressedBytes)
	part["secondaryIndicesUncompressedBytes"] = uint64String(item.SecondaryIndicesUncompressedBytes)
	part["secondaryIndicesMarksBytes"] = uint64String(item.SecondaryIndicesMarksBytes)
	part["modificationTime"] = item.ModificationTime
	part["removeTime"] = item.RemoveTime
	part["refcount"] = uint64String(item.Refcount)
	part["minBlockNumber"] = int64String(item.MinBlockNumber)
	part["maxBlockNumber"] = int64String(item.MaxBlockNumber)
	part["level"] = uint64String(item.Level)
	part["dataVersion"] = uint64String(item.DataVersion)
	part["deleteTtlInfoMin"] = item.DeleteTTLInfoMin
	part["deleteTtlInfoMax"] = item.DeleteTTLInfoMax
	part["moveTtlInfo"] = []map[string]any{}
	part["recompressionTtlInfo"] = []map[string]any{}
	part["defaultCompressionCodec"] = item.DefaultCompressionCodec
	part["conditions"] = apiConditions(item.Conditions)

	return part
}

func apiDetachedPart(item clusterstate.DetachedPart) map[string]any {
	part := apiNodeRef(item.Node)
	part["database"] = item.Database
	part["table"] = item.Table
	part["partitionId"] = deref(item.PartitionID)
	part["partName"] = item.Name
	part["disk"] = item.Disk
	part["reason"] = deref(item.Reason)
	part["path"] = item.Path
	part["bytesOnDisk"] = uint64String(item.BytesOnDisk)
	part["rows"] = uint64String(item.Rows)
	part["minBlockNumber"] = nullableInt64String(item.MinBlockNumber)
	part["maxBlockNumber"] = nullableInt64String(item.MaxBlockNumber)
	part["level"] = nullableUInt64String(item.Level)
	part["modificationTime"] = item.ModificationTime
	part["conditions"] = apiConditions(item.Conditions)

	return part
}

func apiOperation(item clusterstate.Operation) map[string]any {
	return map[string]any{
		"operationId":    item.OperationID,
		"kind":           item.Kind,
		"nodeId":         item.NodeID,
		"database":       item.Database,
		"table":          item.Table,
		"partition":      item.Partition,
		"partitionId":    item.PartitionID,
		"attemptId":      item.AttemptID,
		"state":          item.State,
		"elapsedSeconds": item.ElapsedSeconds,
		"progress":       item.Progress,
		"sourceDisk":     item.SourceDisk,
		"targetDisk":     item.TargetDisk,
		"bytesTotal":     nullableUInt64String(item.BytesTotal),
		"bytesProcessed": nullableUInt64String(item.BytesProcessed),
		"latestMessage":  item.LatestMessage,
		"startedAt":      item.StartedAt,
	}
}

func apiMutation(item clusterstate.Mutation) map[string]any {
	blockNumbers := make([]map[string]any, 0, len(item.BlockNumbers))
	for _, block := range item.BlockNumbers {
		blockNumbers = append(blockNumbers, map[string]any{
			"partitionId": block.PartitionID,
			"number":      uint64String(block.Number),
		})
	}

	mutation := apiNodeRef(item.Node)
	mutation["operationId"] = "mutation|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" + item.MutationID
	mutation["kind"] = "mutation"
	mutation["database"] = item.Database
	mutation["table"] = item.Table
	mutation["mutationId"] = item.MutationID
	mutation["attemptId"] = item.MutationID
	mutation["command"] = item.Command
	mutation["createTime"] = item.CreateTime
	mutation["isDone"] = item.IsDone
	mutation["isKilled"] = item.IsKilled
	mutation["partsToDo"] = uint64String(item.PartsToDo)
	mutation["partsToDoNames"] = item.PartsToDoNames
	mutation["blockNumbers"] = blockNumbers
	mutation["latestFailedPart"] = item.LatestFailedPart
	mutation["latestFailTime"] = item.LatestFailTime
	mutation["latestFailReason"] = item.LatestFailReason
	mutation["conditions"] = apiConditions(item.Conditions)

	return mutation
}

func apiReplicationQueueItem(item clusterstate.ReplicationQueueItem) map[string]any {
	queueItem := apiNodeRef(item.Node)
	attemptID := strconv.FormatUint(item.Position, 10) + ":" + item.Type + ":" + deref(item.NewPartName)
	queueItem["operationId"] = "replication_queue|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" + attemptID
	queueItem["kind"] = "replication_queue"
	queueItem["database"] = item.Database
	queueItem["table"] = item.Table
	queueItem["replicaName"] = item.ReplicaName
	queueItem["position"] = uint64String(item.Position)
	queueItem["nodeName"] = item.NodeName
	queueItem["attemptId"] = attemptID
	queueItem["type"] = item.Type
	queueItem["createTime"] = item.CreateTime
	queueItem["requiredQuorum"] = uint64String(item.RequiredQuorum)
	queueItem["sourceReplica"] = item.SourceReplica
	queueItem["newPartName"] = item.NewPartName
	queueItem["partsToMerge"] = item.PartsToMerge
	queueItem["isDetach"] = item.IsDetach
	queueItem["isCurrentlyExecuting"] = item.IsCurrentlyExecuting
	queueItem["numTries"] = uint64String(item.NumTries)
	queueItem["lastAttemptTime"] = item.LastAttemptTime
	queueItem["lastPostponeTime"] = item.LastPostponeTime
	queueItem["numPostponed"] = uint64String(item.NumPostponed)
	queueItem["postponeReason"] = item.PostponeReason
	queueItem["lastException"] = item.LastException
	queueItem["conditions"] = apiConditions(item.Conditions)

	return queueItem
}

func apiPartEvent(item clusterstate.PartEvent) map[string]any {
	event := apiNodeRef(item.Node)
	eventID := "part_event|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" + item.PartitionID + "|" + item.PartName + "|" + item.EventType + "|" + item.EventTimeMicrostamp
	event["eventId"] = eventID
	event["database"] = item.Database
	event["table"] = item.Table
	event["partitionId"] = item.PartitionID
	event["partName"] = item.PartName
	event["eventType"] = item.EventType
	event["eventTime"] = item.EventTime
	event["durationMs"] = uint64String(item.DurationMs)
	event["rows"] = uint64String(item.Rows)
	event["bytesCompressed"] = uint64String(item.BytesCompressed)
	event["bytesUncompressed"] = uint64String(item.BytesUncompressed)
	event["readRows"] = uint64String(item.ReadRows)
	event["readBytes"] = uint64String(item.ReadBytes)
	event["mergedFrom"] = item.MergedFrom
	event["sourceDisk"] = item.SourceDisk
	event["targetDisk"] = item.TargetDisk
	event["error"] = int64String(item.Error)
	event["exception"] = item.Exception

	return event
}

func apiCondition(item clusterstate.Condition) map[string]any {
	return map[string]any{
		"conditionId": item.ConditionID,
		"severity":    item.Severity,
		"code":        item.Code,
		"message":     item.Message,
		"observedAt":  item.ObservedAt,
		"database":    item.Database,
		"table":       item.Table,
		"partition":   item.Partition,
		"partitionId": item.PartitionID,
		"nodeId":      item.NodeID,
		"evidence":    item.Evidence,
		"links":       item.Links,
	}
}

func apiConditions(items []clusterstate.Condition) []map[string]any {
	conditions := make([]map[string]any, 0, len(items))
	for _, item := range items {
		conditions = append(conditions, apiCondition(item))
	}

	return conditions
}

//nolint:gocognit // Aggregation intentionally keeps table rollup dimensions together.
func aggregateTables(items []clusterstate.TableState, conditions []clusterstate.Condition) []tableAggregate {
	grouped := make(map[string]*tableAggregate)
	for _, item := range items {
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
				partitionOperations:  operationKindCounts(),
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
	grouped := make(map[string]*partitionAggregate)
	for _, part := range parts {
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

func collectConditionsBestEffort(r *http.Request, state StateReader) []clusterstate.Condition {
	result := state.CollectConditions(r.Context())

	return result.Items
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

func partitionMatches(query map[string][]string, item partitionAggregate) bool {
	if firstQuery(query, "partitionId") != "" && item.partitionID != firstQuery(query, "partitionId") {
		return false
	}
	if firstQuery(query, "placement") != "" && item.placement != firstQuery(query, "placement") {
		return false
	}
	if firstQuery(query, "disk") != "" {
		if _, ok := item.disks[firstQuery(query, "disk")]; !ok {
			return false
		}
	}
	if firstQuery(query, "operation") != "" && !stringSliceContains(item.operations, firstQuery(query, "operation")) {
		return false
	}
	if firstQuery(query, "nodeId") != "" || firstQuery(query, "shard") != "" || firstQuery(query, "replica") != "" {
		for _, placement := range item.placements {
			if nodeMatches(query, placement.node) {
				return true
			}
		}

		return false
	}

	return true
}

func nodePartMatches(query map[string][]string, node chclient.Node) bool {
	return nodeMatches(query, node)
}

func nodeMatches(query map[string][]string, node chclient.Node) bool {
	if firstQuery(query, "nodeId") != "" && node.ID != firstQuery(query, "nodeId") {
		return false
	}
	if firstQuery(query, "shard") != "" && node.Shard != firstQuery(query, "shard") {
		return false
	}
	if firstQuery(query, "replica") != "" && node.Replica != firstQuery(query, "replica") {
		return false
	}

	return true
}

func parseOptionalBool(w http.ResponseWriter, r *http.Request, name string) (*bool, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, true
	}

	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		writeBadParameter(w, r, name, "must be a boolean")

		return nil, false
	}

	return &parsed, true
}

func parseOptionalUint64(w http.ResponseWriter, r *http.Request, name string) (*uint64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, true
	}

	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		writeBadParameter(w, r, name, "must be an unsigned integer")

		return nil, false
	}

	return &parsed, true
}

func parseOptionalTime(w http.ResponseWriter, r *http.Request, name string) (*time.Time, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, true
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeBadParameter(w, r, name, "must be an RFC3339 timestamp")

		return nil, false
	}

	return &parsed, true
}

func validateClosedQuery(w http.ResponseWriter, r *http.Request, name string, allowed map[string]struct{}) bool {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return true
	}
	if _, ok := allowed[raw]; ok {
		return true
	}

	writeBadParameter(w, r, name, "unsupported value")

	return false
}

func writeBadParameter(w http.ResponseWriter, r *http.Request, parameter string, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)

	_ = json.NewEncoder(w).Encode(problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusBadRequest),
		Status:   http.StatusBadRequest,
		Detail:   "request parameter validation failed",
		Instance: r.URL.RequestURI(),
		Errors: []problemError{
			{Parameter: parameter, Detail: detail},
		},
	})
}

func sortMapItems(items []map[string]any, primary string, secondary string) {
	sort.SliceStable(items, func(i, j int) bool {
		left := stringify(items[i][primary])
		right := stringify(items[j][primary])
		if left == right && secondary != "" {
			return stringify(items[i][secondary]) < stringify(items[j][secondary])
		}

		return left < right
	})
}

func uint64String(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}

func nullableUInt64String(value *uint64) any {
	if value == nil {
		return nil
	}

	return uint64String(*value)
}

func nullableInt64String(value *int64) any {
	if value == nil {
		return nil
	}

	return int64String(*value)
}

func fixedPlacementCounts(counts map[string]int) map[string]int {
	return fixedCounts(counts, []string{"on_target", "off_target", "split", "replica_divergent", "missing_replica", "unknown"})
}

func fixedOperationCounts(counts map[string]int) map[string]int {
	return fixedCounts(counts, []string{"moving", "merging", "mutating", "fetching"})
}

func operationKindCounts() map[string]int {
	return fixedCounts(nil, []string{"move", "merge", "mutation", "fetch", "replication_queue"})
}

func severityCounts() map[string]int {
	return fixedCounts(nil, []string{"critical", "warning", "info"})
}

func fixedCounts(counts map[string]int, keys []string) map[string]int {
	out := make(map[string]int, len(keys))
	for _, key := range keys {
		out[key] = 0
	}
	if counts != nil {
		maps.Copy(out, counts)
	}

	return out
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)

	return out
}

func firstQuery(query map[string][]string, key string) string {
	values := query[key]
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func stringSliceContains(values []string, target string) bool {
	return slices.Contains(values, target)
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(typed)
	}
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

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}
