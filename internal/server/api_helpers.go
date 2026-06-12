package server

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
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
	s.writeJSONStatus(w, r, http.StatusOK, body)
}

func (s *server) writeJSONStatus(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		// A vanished client — closed tab, page refresh, proxy timeout — is the
		// client's business, not a server fault worth an error-level log.
		if r.Context().Err() != nil || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
			s.log.DebugContext(r.Context(), "client went away during api response", "error", err)

			return
		}
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

func collectionMeta[T any](result clusterstate.Result[T]) collectionResponse {
	return collectionResponse{
		CollectedAt:          result.CollectedAt,
		Partial:              result.Partial(),
		CollectionDurationMs: int(result.CollectionDuration.Milliseconds()),
		NodesExpected:        result.NodesExpected,
		NodesResponded:       result.NodesResponded,
		NodesFailed:          result.NodesFailed,
		Warnings:             apiWarnings(result.Warnings),
	}
}

func apiWarnings(warnings []clusterstate.Warning) []warningResponse {
	items := make([]warningResponse, 0, len(warnings))
	for _, warning := range warnings {
		items = append(items, warningResponse{
			Kind:    warning.Kind,
			Code:    warning.Code,
			Message: warning.Message,
			NodeID:  warning.NodeID,
		})
	}

	return items
}

func apiNode(item clusterstate.NodeStatus) nodeResponse {
	node := nodeResponse{
		nodeRef:    apiNodeRef(item.Node),
		Endpoint:   apiNodeEndpoint(item.Node.Addr),
		Reachable:  item.Reachable,
		ObservedAt: item.ObservedAt,
		Version:    item.Version,
		Timezone:   item.Timezone,
		LastError:  nilIfEmpty(item.LastError),
	}
	if item.UptimeSeconds != 0 {
		node.UptimeSeconds = uint64String(item.UptimeSeconds)
	}

	return node
}

func apiNodeEndpoint(addr string) string {
	if at := strings.LastIndex(addr, "@"); at >= 0 {
		addr = addr[at+1:]
	}

	return "clickhouse://" + addr
}

func apiDisk(item clusterstate.Disk) diskResponse {
	return diskResponse{
		nodeRef:                apiNodeRef(item.Node),
		Disk:                   item.Name,
		Type:                   item.Type,
		ObjectStorageType:      item.ObjectStorageType,
		IsRemote:               item.IsRemote,
		IsBroken:               item.IsBroken,
		Path:                   nilIfEmpty(item.Path),
		CachePath:              nilIfEmpty(item.CachePath),
		CapacityKnown:          item.CapacityKnown,
		FreeSpaceBytes:         nullableUInt64String(item.FreeSpaceBytes),
		TotalSpaceBytes:        nullableUInt64String(item.TotalSpaceBytes),
		UnreservedSpaceBytes:   nullableUInt64String(item.UnreservedSpaceBytes),
		UsedByActivePartsBytes: uint64String(item.UsedByActiveParts),
	}
}

func apiNodeRef(node chclient.Node) nodeRef {
	return nodeRef{
		NodeID:  node.ID,
		Shard:   node.Shard,
		Replica: node.Replica,
	}
}

func apiTableListItem(item tableAggregate) tableSummaryResponse {
	return tableSummaryResponse{
		tableBase:           apiTableBase(item),
		NodesObserved:       item.nodesObserved,
		ShardsObserved:      item.shardsObserved,
		ReplicasPerShard:    item.replicasPerShard,
		ActivePartitions:    item.activePartitions,
		ActiveParts:         uint64String(item.activeParts),
		Rows:                uint64String(item.rows),
		BytesOnDisk:         uint64String(item.bytesOnDisk),
		PartitionPlacements: fixedPlacementCounts(item.partitionPlacements),
		PartitionOperations: fixedOperationCounts(item.partitionOperations),
		ActiveOperations:    item.activeOperations,
		Conditions:          apiConditions(item.conditions),
		Links: map[string]string{
			"partEvents": "/api/v1/part-events?database=" + url.QueryEscape(item.database) + "&table=" + url.QueryEscape(item.table),
		},
	}
}

func apiTableDetail(item tableAggregate) tableDetailResponse {
	return tableDetailResponse{
		tableBase:            apiTableBase(item),
		UUID:                 item.uuid,
		SamplingKey:          item.samplingKey,
		IsReplicated:         item.isReplicated,
		NodesObserved:        item.nodesObserved,
		ActivePartitions:     item.activePartitions,
		ActiveParts:          uint64String(item.activeParts),
		Rows:                 uint64String(item.rows),
		BytesOnDisk:          uint64String(item.bytesOnDisk),
		MinPartition:         item.minPartition,
		MaxPartition:         item.maxPartition,
		LastModificationTime: item.lastModificationTime,
		PartitionPlacements:  fixedPlacementCounts(item.partitionPlacements),
		PartitionOperations:  fixedOperationCounts(item.partitionOperations),
		Nodes:                apiNodeTableStates(item.nodes),
		Conditions:           apiConditions(item.conditions),
	}
}

func apiTableBase(item tableAggregate) tableBase {
	return tableBase{
		Database:      item.database,
		Table:         item.table,
		Engine:        item.engine,
		StoragePolicy: item.storagePolicy,
		TargetDisk:    item.targetDisk,
		PartitionKey:  item.partitionKey,
		SortingKey:    item.sortingKey,
		PrimaryKey:    item.primaryKey,
		VersionColumn: item.versionColumn,
	}
}

func apiNodeTableStates(items []clusterstate.TableState) []nodeTableStateResponse {
	states := make([]nodeTableStateResponse, 0, len(items))
	for _, item := range items {
		state := nodeTableStateResponse{
			NodeID:      item.Node.ID,
			Engine:      item.Engine,
			ActiveParts: uint64String(item.ActiveParts),
			Rows:        uint64String(item.Rows),
			BytesOnDisk: uint64String(item.BytesOnDisk),
		}
		if item.Replica != nil {
			state.Replica = &replicaStateResponse{
				Readonly:             item.Replica.Readonly,
				SessionExpired:       item.Replica.SessionExpired,
				QueueSize:            uint64String(item.Replica.QueueSize),
				AbsoluteDelaySeconds: uint64String(item.Replica.AbsoluteDelaySeconds),
				TotalReplicas:        uint64String(item.Replica.TotalReplicas),
				ActiveReplicas:       uint64String(item.Replica.ActiveReplicas),
			}
		}
		states = append(states, state)
	}
	slices.SortStableFunc(states, func(a, b nodeTableStateResponse) int {
		return cmp.Compare(a.NodeID, b.NodeID)
	})

	return states
}

func apiColumn(column clusterstate.Column) columnResponse {
	return columnResponse{
		Name:              column.Name,
		Position:          column.Position,
		Type:              column.Type,
		Kind:              column.Kind,
		DefaultKind:       column.DefaultKind,
		DefaultExpression: column.DefaultExpression,
		CodecExpression:   column.CodecExpression,
		TTLExpression:     column.TTLExpression,
		Comment:           column.Comment,
		IsInPartitionKey:  column.IsInPartitionKey,
		IsInSortingKey:    column.IsInSortingKey,
		IsInPrimaryKey:    column.IsInPrimaryKey,
		IsInSamplingKey:   column.IsInSamplingKey,
	}
}

func apiPartition(item partitionAggregate) partitionResponse {
	placements := make([]partitionPlacementResponse, 0, len(item.placements))
	for _, placement := range item.placements {
		placements = append(placements, partitionPlacementResponse{
			nodeRef:              apiNodeRef(placement.node),
			Disk:                 placement.disk,
			ActiveParts:          uint64String(placement.activeParts),
			Rows:                 uint64String(placement.rows),
			BytesOnDisk:          uint64String(placement.bytesOnDisk),
			LastModificationTime: placement.lastModificationTime,
		})
	}
	slices.SortStableFunc(placements, func(a, b partitionPlacementResponse) int {
		return cmp.Or(cmp.Compare(a.NodeID, b.NodeID), cmp.Compare(a.Disk, b.Disk))
	})

	return partitionResponse{
		Database:             item.database,
		Table:                item.table,
		Partition:            item.partition,
		PartitionID:          item.partitionID,
		TargetDisk:           item.targetDisk,
		Placement:            item.placement,
		Operations:           item.operations,
		Disks:                slices.Sorted(maps.Keys(item.disks)),
		ActiveParts:          uint64String(item.activeParts),
		Rows:                 uint64String(item.rows),
		BytesOnDisk:          uint64String(item.bytesOnDisk),
		LastModificationTime: item.lastModificationTime,
		Placements:           placements,
		Conditions:           apiConditions(item.conditions),
	}
}

func apiPart(item clusterstate.Part) partResponse {
	return partResponse{
		nodeRef:                           apiNodeRef(item.Node),
		Database:                          item.Database,
		Table:                             item.Table,
		Partition:                         item.Partition,
		PartitionID:                       item.PartitionID,
		PartName:                          item.Name,
		UUID:                              item.UUID,
		Active:                            item.Active,
		Disk:                              item.Disk,
		Path:                              item.Path,
		PartType:                          item.PartType,
		Rows:                              uint64String(item.Rows),
		Marks:                             uint64String(item.Marks),
		BytesOnDisk:                       uint64String(item.BytesOnDisk),
		DataCompressedBytes:               uint64String(item.DataCompressedBytes),
		DataUncompressedBytes:             uint64String(item.DataUncompressedBytes),
		MarksBytes:                        uint64String(item.MarksBytes),
		PrimaryKeyBytesInMemory:           uint64String(item.PrimaryKeyBytesInMemory),
		PrimaryKeyBytesInMemoryAllocated:  uint64String(item.PrimaryKeyBytesInMemoryAllocated),
		SecondaryIndicesCompressedBytes:   uint64String(item.SecondaryIndicesCompressedBytes),
		SecondaryIndicesUncompressedBytes: uint64String(item.SecondaryIndicesUncompressedBytes),
		SecondaryIndicesMarksBytes:        uint64String(item.SecondaryIndicesMarksBytes),
		ModificationTime:                  item.ModificationTime,
		RemoveTime:                        item.RemoveTime,
		Refcount:                          uint64String(item.Refcount),
		MinBlockNumber:                    int64String(item.MinBlockNumber),
		MaxBlockNumber:                    int64String(item.MaxBlockNumber),
		Level:                             uint64String(item.Level),
		DataVersion:                       uint64String(item.DataVersion),
		DeleteTTLInfoMin:                  item.DeleteTTLInfoMin,
		DeleteTTLInfoMax:                  item.DeleteTTLInfoMax,
		MoveTTLInfo:                       []map[string]any{},
		RecompressionTTLInfo:              []map[string]any{},
		DefaultCompressionCodec:           item.DefaultCompressionCodec,
		Conditions:                        apiConditions(item.Conditions),
	}
}

func apiDetachedPart(item clusterstate.DetachedPart) detachedPartResponse {
	return detachedPartResponse{
		nodeRef:          apiNodeRef(item.Node),
		Database:         item.Database,
		Table:            item.Table,
		PartitionID:      deref(item.PartitionID),
		PartName:         item.Name,
		Disk:             item.Disk,
		Reason:           deref(item.Reason),
		Path:             item.Path,
		BytesOnDisk:      uint64String(item.BytesOnDisk),
		Rows:             uint64String(item.Rows),
		MinBlockNumber:   nullableInt64String(item.MinBlockNumber),
		MaxBlockNumber:   nullableInt64String(item.MaxBlockNumber),
		Level:            nullableUInt64String(item.Level),
		ModificationTime: item.ModificationTime,
		Conditions:       apiConditions(item.Conditions),
	}
}

func apiOperation(item clusterstate.Operation) operationResponse {
	return operationResponse{
		OperationID:    item.OperationID,
		Kind:           item.Kind,
		NodeID:         item.NodeID,
		Database:       item.Database,
		Table:          item.Table,
		Partition:      item.Partition,
		PartitionID:    item.PartitionID,
		AttemptID:      item.AttemptID,
		State:          item.State,
		ElapsedSeconds: item.ElapsedSeconds,
		Progress:       item.Progress,
		SourceDisk:     item.SourceDisk,
		TargetDisk:     item.TargetDisk,
		BytesTotal:     nullableUInt64String(item.BytesTotal),
		BytesProcessed: nullableUInt64String(item.BytesProcessed),
		LatestMessage:  item.LatestMessage,
		StartedAt:      item.StartedAt,
	}
}

func apiMutation(item clusterstate.Mutation) mutationResponse {
	blockNumbers := make([]mutationBlockNumberResponse, 0, len(item.BlockNumbers))
	for _, block := range item.BlockNumbers {
		blockNumbers = append(blockNumbers, mutationBlockNumberResponse{
			PartitionID: block.PartitionID,
			Number:      uint64String(block.Number),
		})
	}

	return mutationResponse{
		nodeRef:          apiNodeRef(item.Node),
		OperationID:      "mutation|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" + item.MutationID,
		Kind:             "mutation",
		Database:         item.Database,
		Table:            item.Table,
		MutationID:       item.MutationID,
		AttemptID:        item.MutationID,
		Command:          item.Command,
		CreateTime:       item.CreateTime,
		IsDone:           item.IsDone,
		IsKilled:         item.IsKilled,
		PartsToDo:        uint64String(item.PartsToDo),
		PartsToDoNames:   item.PartsToDoNames,
		BlockNumbers:     blockNumbers,
		LatestFailedPart: item.LatestFailedPart,
		LatestFailTime:   item.LatestFailTime,
		LatestFailReason: item.LatestFailReason,
		Conditions:       apiConditions(item.Conditions),
	}
}

func apiReplicationQueueItem(item clusterstate.ReplicationQueueItem) replicationQueueResponse {
	attemptID := strconv.FormatUint(item.Position, 10) + ":" + item.Type + ":" + deref(item.NewPartName)

	return replicationQueueResponse{
		nodeRef:              apiNodeRef(item.Node),
		OperationID:          "replication_queue|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" + attemptID,
		Kind:                 "replication_queue",
		Database:             item.Database,
		Table:                item.Table,
		ReplicaName:          item.ReplicaName,
		Position:             uint64String(item.Position),
		NodeName:             item.NodeName,
		AttemptID:            attemptID,
		Type:                 item.Type,
		CreateTime:           item.CreateTime,
		RequiredQuorum:       uint64String(item.RequiredQuorum),
		SourceReplica:        item.SourceReplica,
		NewPartName:          item.NewPartName,
		PartsToMerge:         item.PartsToMerge,
		IsDetach:             item.IsDetach,
		IsCurrentlyExecuting: item.IsCurrentlyExecuting,
		NumTries:             uint64String(item.NumTries),
		LastAttemptTime:      item.LastAttemptTime,
		LastPostponeTime:     item.LastPostponeTime,
		NumPostponed:         uint64String(item.NumPostponed),
		PostponeReason:       item.PostponeReason,
		LastException:        item.LastException,
		Conditions:           apiConditions(item.Conditions),
	}
}

func apiPartEvent(item clusterstate.PartEvent) partEventResponse {
	eventID := "part_event|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" +
		item.PartitionID + "|" + item.PartName + "|" + item.EventType + "|" + item.EventTimeMicrostamp

	return partEventResponse{
		nodeRef:           apiNodeRef(item.Node),
		EventID:           eventID,
		Database:          item.Database,
		Table:             item.Table,
		PartitionID:       item.PartitionID,
		PartName:          item.PartName,
		EventType:         item.EventType,
		EventTime:         item.EventTime,
		DurationMs:        uint64String(item.DurationMs),
		Rows:              uint64String(item.Rows),
		BytesCompressed:   uint64String(item.BytesCompressed),
		BytesUncompressed: uint64String(item.BytesUncompressed),
		ReadRows:          uint64String(item.ReadRows),
		ReadBytes:         uint64String(item.ReadBytes),
		MergedFrom:        item.MergedFrom,
		SourceDisk:        item.SourceDisk,
		TargetDisk:        item.TargetDisk,
		Error:             int64String(item.Error),
		Exception:         item.Exception,
	}
}

func apiCondition(item clusterstate.Condition) conditionResponse {
	return conditionResponse{
		ConditionID: item.ConditionID,
		Severity:    item.Severity,
		Code:        item.Code,
		Message:     item.Message,
		ObservedAt:  item.ObservedAt,
		Database:    item.Database,
		Table:       item.Table,
		Partition:   item.Partition,
		PartitionID: item.PartitionID,
		NodeID:      item.NodeID,
		Evidence:    item.Evidence,
		Links:       item.Links,
	}
}

func apiConditions(items []clusterstate.Condition) []conditionResponse {
	conditions := make([]conditionResponse, 0, len(items))
	for _, item := range items {
		conditions = append(conditions, apiCondition(item))
	}

	return conditions
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
	if firstQuery(query, "operation") != "" && !slices.Contains(item.operations, firstQuery(query, "operation")) {
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

func conditionMatches(query map[string][]string, item clusterstate.Condition) bool {
	if firstQuery(query, "severity") != "" && item.Severity != firstQuery(query, "severity") {
		return false
	}
	if firstQuery(query, "code") != "" && item.Code != firstQuery(query, "code") {
		return false
	}
	if firstQuery(query, "nodeId") != "" && deref(item.NodeID) != firstQuery(query, "nodeId") {
		return false
	}
	if firstQuery(query, "database") != "" && deref(item.Database) != firstQuery(query, "database") {
		return false
	}
	if firstQuery(query, "table") != "" && deref(item.Table) != firstQuery(query, "table") {
		return false
	}
	if firstQuery(query, "partitionId") != "" && deref(item.PartitionID) != firstQuery(query, "partitionId") {
		return false
	}

	return true
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
		Detail:   detail,
		Instance: r.URL.RequestURI(),
		Errors: []problemError{
			{Parameter: parameter, Detail: detail},
		},
	})
}

func uint64String(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}

func nullableUInt64String(value *uint64) *string {
	if value == nil {
		return nil
	}

	formatted := uint64String(*value)

	return &formatted
}

func nullableInt64String(value *int64) *string {
	if value == nil {
		return nil
	}

	formatted := int64String(*value)

	return &formatted
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

func firstQuery(query map[string][]string, key string) string {
	values := query[key]
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func nilIfEmpty(value string) *string {
	if value == "" {
		return nil
	}

	return &value
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
