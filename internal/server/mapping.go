package server

import (
	"cmp"
	"encoding/json"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/jx"

	"github.com/ethpandaops/clickhouse-movoor/api/rest"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

// --- scalar helpers -------------------------------------------------------

func u64(value uint64) rest.UInt64String {
	return rest.UInt64String(strconv.FormatUint(value, 10))
}

func i64(value int64) rest.Int64String {
	return rest.Int64String(strconv.FormatInt(value, 10))
}

func optU64(value uint64) rest.OptUInt64String {
	return rest.NewOptUInt64String(u64(value))
}

func optI64(value int64) rest.OptInt64String {
	return rest.NewOptInt64String(i64(value))
}

func optNilU64Ptr(value *uint64) rest.OptNilUInt64String {
	if value == nil {
		out := rest.OptNilUInt64String{}
		out.SetToNull()

		return out
	}

	return rest.NewOptNilUInt64String(u64(*value))
}

func optI64Ptr(value *int64) rest.OptInt64String {
	if value == nil {
		return rest.OptInt64String{}
	}

	return optI64(*value)
}

func optU64Ptr(value *uint64) rest.OptUInt64String {
	if value == nil {
		return rest.OptUInt64String{}
	}

	return optU64(*value)
}

func nilU64Ptr(value *uint64) rest.NilUInt64String {
	out := rest.NilUInt64String{}
	if value == nil {
		out.SetToNull()

		return out
	}
	out.SetTo(u64(*value))

	return out
}

// nilStr maps the "empty means absent" convention onto a nullable string.
func nilStr(value string) rest.NilString {
	out := rest.NilString{}
	if value == "" {
		out.SetToNull()

		return out
	}
	out.SetTo(value)

	return out
}

func nilStrPtr(value *string) rest.NilString {
	if value == nil {
		return nilStr("")
	}

	return nilStr(*value)
}

func optNilStrPtr(value *string) rest.OptNilString {
	if value == nil {
		out := rest.OptNilString{}
		out.SetToNull()

		return out
	}

	return rest.NewOptNilString(*value)
}

func optNilStr(value string) rest.OptNilString {
	if value == "" {
		out := rest.OptNilString{}
		out.SetToNull()

		return out
	}

	return rest.NewOptNilString(value)
}

func optNonEmpty(value string) rest.OptString {
	if value == "" {
		return rest.OptString{}
	}

	return rest.NewOptString(value)
}

func optNilTimePtr(value *time.Time) rest.OptNilDateTime {
	if value == nil {
		out := rest.OptNilDateTime{}
		out.SetToNull()

		return out
	}

	return rest.NewOptNilDateTime(*value)
}

func optTimePtr(value *time.Time) rest.OptDateTime {
	if value == nil {
		return rest.OptDateTime{}
	}

	return rest.NewOptDateTime(*value)
}

func aboutBlank() url.URL {
	return url.URL{Scheme: "about", Opaque: "blank"}
}

// --- collection envelope --------------------------------------------------

func apiCollectionMeta[T any](result clusterstate.Result[T]) rest.CollectionMeta {
	warnings := make([]rest.CollectionWarning, 0, len(result.Warnings))
	for _, warning := range result.Warnings {
		warnings = append(warnings, rest.CollectionWarning{
			Kind:    rest.WarningKind(warning.Kind),
			Code:    warning.Code,
			Message: warning.Message,
			NodeId:  optNonEmpty(warning.NodeID),
		})
	}

	return rest.CollectionMeta{
		CollectedAt:          result.CollectedAt,
		Partial:              result.Partial(),
		CollectionDurationMs: int(result.CollectionDuration.Milliseconds()),
		NodesExpected:        result.NodesExpected,
		NodesResponded:       result.NodesResponded,
		NodesFailed:          result.NodesFailed,
		Warnings:             warnings,
	}
}

// --- cluster state mappers ------------------------------------------------

func apiNode(item clusterstate.NodeStatus) rest.Node {
	node := rest.Node{
		NodeId:     item.Node.ID,
		Shard:      item.Node.Shard,
		Replica:    item.Node.Replica,
		Endpoint:   apiNodeEndpoint(item.Node.Addr),
		Reachable:  item.Reachable,
		ObservedAt: item.ObservedAt,
		Version:    optNonEmpty(item.Version),
		Timezone:   optNonEmpty(item.Timezone),
		LastError:  nilStr(item.LastError),
	}
	if item.UptimeSeconds != 0 {
		node.UptimeSeconds = optU64(item.UptimeSeconds)
	}

	return node
}

// apiNodeEndpoint strips DSN userinfo so credentials never reach the API.
func apiNodeEndpoint(addr string) url.URL {
	if at := strings.LastIndex(addr, "@"); at >= 0 {
		addr = addr[at+1:]
	}
	parsed, err := url.Parse("clickhouse://" + addr)
	if err != nil || parsed == nil {
		return url.URL{Scheme: "clickhouse"}
	}

	return *parsed
}

func apiDisk(item clusterstate.Disk) rest.StorageDisk {
	return rest.StorageDisk{
		NodeId:                 item.Node.ID,
		Shard:                  item.Node.Shard,
		Replica:                item.Node.Replica,
		Disk:                   item.Name,
		Type:                   item.Type,
		ObjectStorageType:      item.ObjectStorageType,
		IsRemote:               item.IsRemote,
		IsBroken:               item.IsBroken,
		Path:                   optNilStr(item.Path),
		CachePath:              optNilStr(item.CachePath),
		CapacityKnown:          item.CapacityKnown,
		FreeSpaceBytes:         nilU64Ptr(item.FreeSpaceBytes),
		TotalSpaceBytes:        nilU64Ptr(item.TotalSpaceBytes),
		UnreservedSpaceBytes:   nilU64Ptr(item.UnreservedSpaceBytes),
		UsedByActivePartsBytes: u64(item.UsedByActiveParts),
	}
}

func apiTableListItem(item tableAggregate) rest.TableListItem {
	return rest.TableListItem{
		Database:            item.database,
		Table:               item.table,
		Engine:              item.engine,
		StoragePolicy:       item.storagePolicy,
		TargetDisk:          item.targetDisk,
		PartitionKey:        item.partitionKey,
		SortingKey:          item.sortingKey,
		PrimaryKey:          item.primaryKey,
		VersionColumn:       optNilStrPtr(item.versionColumn),
		NodesObserved:       item.nodesObserved,
		ShardsObserved:      item.shardsObserved,
		ReplicasPerShard:    item.replicasPerShard,
		ActivePartitions:    item.activePartitions,
		ActiveParts:         u64(item.activeParts),
		Rows:                u64(item.rows),
		BytesOnDisk:         u64(item.bytesOnDisk),
		PartitionPlacements: apiPlacementCounts(item.partitionPlacements),
		PartitionOperations: apiOperationCountsFixed(item.partitionOperations),
		ActiveOperations:    item.activeOperations,
		Conditions:          apiEmbeddedConditions(item.conditions),
		Links: rest.LinkMap{
			"partEvents": "/api/v1/part-events?database=" + url.QueryEscape(item.database) + "&table=" + url.QueryEscape(item.table),
		},
	}
}

func apiTableDetail(item tableAggregate) rest.TableDetail {
	return rest.TableDetail{
		Database:             item.database,
		Table:                item.table,
		Engine:               item.engine,
		StoragePolicy:        item.storagePolicy,
		TargetDisk:           item.targetDisk,
		PartitionKey:         item.partitionKey,
		SortingKey:           item.sortingKey,
		PrimaryKey:           item.primaryKey,
		VersionColumn:        optNilStrPtr(item.versionColumn),
		UUID:                 item.uuid,
		SamplingKey:          item.samplingKey,
		IsReplicated:         item.isReplicated,
		NodesObserved:        item.nodesObserved,
		ActivePartitions:     item.activePartitions,
		ActiveParts:          u64(item.activeParts),
		Rows:                 u64(item.rows),
		BytesOnDisk:          u64(item.bytesOnDisk),
		MinPartition:         optNilStrPtr(item.minPartition),
		MaxPartition:         optNilStrPtr(item.maxPartition),
		LastModificationTime: optNilTimePtr(item.lastModificationTime),
		PartitionPlacements:  apiPlacementCounts(item.partitionPlacements),
		PartitionOperations:  apiOperationCountsFixed(item.partitionOperations),
		Nodes:                apiNodeTableStates(item.nodes),
		Conditions:           apiEmbeddedConditions(item.conditions),
	}
}

func apiNodeTableStates(items []clusterstate.TableState) []rest.NodeTableState {
	states := make([]rest.NodeTableState, 0, len(items))
	for _, item := range items {
		state := rest.NodeTableState{
			NodeId:      item.Node.ID,
			Engine:      item.Engine,
			ActiveParts: u64(item.ActiveParts),
			Rows:        u64(item.Rows),
			BytesOnDisk: u64(item.BytesOnDisk),
		}
		if item.Replica != nil {
			state.Replica = rest.NewOptReplicaState(rest.ReplicaState{
				Readonly:             rest.NewOptBool(item.Replica.Readonly),
				SessionExpired:       rest.NewOptBool(item.Replica.SessionExpired),
				QueueSize:            optU64(item.Replica.QueueSize),
				AbsoluteDelaySeconds: optU64(item.Replica.AbsoluteDelaySeconds),
				TotalReplicas:        optU64(item.Replica.TotalReplicas),
				ActiveReplicas:       optU64(item.Replica.ActiveReplicas),
			})
		}
		states = append(states, state)
	}
	slices.SortStableFunc(states, func(a, b rest.NodeTableState) int {
		return cmp.Compare(a.NodeId, b.NodeId)
	})

	return states
}

func apiColumn(column clusterstate.Column) rest.TableColumn {
	position := 0
	if column.Position <= uint64(1<<31-1) {
		position = int(column.Position)
	}

	return rest.TableColumn{
		Name:              column.Name,
		Position:          position,
		Type:              column.Type,
		Kind:              column.Kind,
		DefaultKind:       nilStrPtr(column.DefaultKind),
		DefaultExpression: nilStrPtr(column.DefaultExpression),
		CodecExpression:   nilStrPtr(column.CodecExpression),
		TtlExpression:     nilStrPtr(column.TTLExpression),
		Comment:           column.Comment,
		IsInPartitionKey:  column.IsInPartitionKey,
		IsInSortingKey:    column.IsInSortingKey,
		IsInPrimaryKey:    column.IsInPrimaryKey,
		IsInSamplingKey:   column.IsInSamplingKey,
	}
}

func apiPartition(item partitionAggregate) rest.Partition {
	placements := make([]rest.PartitionPlacement, 0, len(item.placements))
	for _, placement := range item.placements {
		placements = append(placements, rest.PartitionPlacement{
			NodeId:               placement.node.ID,
			Shard:                placement.node.Shard,
			Replica:              placement.node.Replica,
			Disk:                 placement.disk,
			ActiveParts:          u64(placement.activeParts),
			Rows:                 u64(placement.rows),
			BytesOnDisk:          u64(placement.bytesOnDisk),
			LastModificationTime: optNilTimePtr(placement.lastModificationTime),
		})
	}
	slices.SortStableFunc(placements, func(a, b rest.PartitionPlacement) int {
		return cmp.Or(cmp.Compare(a.NodeId, b.NodeId), cmp.Compare(a.Disk, b.Disk))
	})

	operations := item.operations
	if operations == nil {
		operations = []string{}
	}

	return rest.Partition{
		Database:             item.database,
		Table:                item.table,
		Partition:            item.partition,
		PartitionId:          item.partitionID,
		TargetDisk:           item.targetDisk,
		Placement:            rest.Placement(item.placement),
		Operations:           operations,
		Disks:                slices.Sorted(maps.Keys(item.disks)),
		ActiveParts:          u64(item.activeParts),
		Rows:                 u64(item.rows),
		BytesOnDisk:          u64(item.bytesOnDisk),
		LastModificationTime: optNilTimePtr(item.lastModificationTime),
		Placements:           placements,
		Conditions:           apiEmbeddedConditions(item.conditions),
	}
}

func apiPart(item clusterstate.Part) rest.TablePart {
	return rest.TablePart{
		NodeId:                            item.Node.ID,
		Shard:                             item.Node.Shard,
		Replica:                           item.Node.Replica,
		Database:                          item.Database,
		Table:                             item.Table,
		Partition:                         item.Partition,
		PartitionId:                       item.PartitionID,
		PartName:                          item.Name,
		UUID:                              rest.NewOptString(item.UUID),
		Active:                            item.Active,
		Disk:                              item.Disk,
		Path:                              item.Path,
		PartType:                          item.PartType,
		Rows:                              u64(item.Rows),
		Marks:                             optU64(item.Marks),
		BytesOnDisk:                       u64(item.BytesOnDisk),
		DataCompressedBytes:               optU64(item.DataCompressedBytes),
		DataUncompressedBytes:             optU64(item.DataUncompressedBytes),
		MarksBytes:                        optU64(item.MarksBytes),
		PrimaryKeyBytesInMemory:           optU64(item.PrimaryKeyBytesInMemory),
		PrimaryKeyBytesInMemoryAllocated:  optU64(item.PrimaryKeyBytesInMemoryAllocated),
		SecondaryIndicesCompressedBytes:   optU64(item.SecondaryIndicesCompressedBytes),
		SecondaryIndicesUncompressedBytes: optU64(item.SecondaryIndicesUncompressedBytes),
		SecondaryIndicesMarksBytes:        optU64(item.SecondaryIndicesMarksBytes),
		ModificationTime:                  item.ModificationTime,
		RemoveTime:                        optNilTimePtr(item.RemoveTime),
		Refcount:                          optU64(item.Refcount),
		MinBlockNumber:                    optI64(item.MinBlockNumber),
		MaxBlockNumber:                    optI64(item.MaxBlockNumber),
		Level:                             optU64(item.Level),
		DataVersion:                       optU64(item.DataVersion),
		DeleteTtlInfoMin:                  optNilTimePtr(item.DeleteTTLInfoMin),
		DeleteTtlInfoMax:                  optNilTimePtr(item.DeleteTTLInfoMax),
		MoveTtlInfo:                       []rest.TtlInfo{},
		RecompressionTtlInfo:              []rest.TtlInfo{},
		DefaultCompressionCodec:           rest.NewOptString(item.DefaultCompressionCodec),
		Conditions:                        apiEmbeddedConditions(item.Conditions),
	}
}

func apiDetachedPart(item clusterstate.DetachedPart) rest.DetachedPart {
	return rest.DetachedPart{
		NodeId:           item.Node.ID,
		Shard:            item.Node.Shard,
		Replica:          item.Node.Replica,
		Database:         item.Database,
		Table:            item.Table,
		PartitionId:      deref(item.PartitionID),
		PartName:         item.Name,
		Disk:             item.Disk,
		Reason:           deref(item.Reason),
		Path:             item.Path,
		BytesOnDisk:      u64(item.BytesOnDisk),
		Rows:             u64(item.Rows),
		MinBlockNumber:   optI64Ptr(item.MinBlockNumber),
		MaxBlockNumber:   optI64Ptr(item.MaxBlockNumber),
		Level:            optU64Ptr(item.Level),
		ModificationTime: rest.NewOptDateTime(item.ModificationTime),
		Conditions:       apiEmbeddedConditions(item.Conditions),
	}
}

func apiOperation(item clusterstate.Operation) rest.Operation {
	out := rest.Operation{
		OperationId: item.OperationID,
		Kind:        rest.OperationKind(item.Kind),
		NodeId:      item.NodeID,
		Database:    item.Database,
		Table:       item.Table,
		Partition:   optNilStrPtr(item.Partition),
		PartitionId: optNilStrPtr(item.PartitionID),
		AttemptId:   item.AttemptID,
		State:       item.State,
		SourceDisk:  optNilStrPtr(item.SourceDisk),
		TargetDisk:  optNilStrPtr(item.TargetDisk),
		// elapsedSeconds and progress are plain (non-nullable) numbers in the
		// schema: unknown values stay absent — moves carry no progress and
		// mutation/replication-queue operations carry neither.
		BytesTotal:     optNilU64Ptr(item.BytesTotal),
		BytesProcessed: optNilU64Ptr(item.BytesProcessed),
		LatestMessage:  optNilStrPtr(item.LatestMessage),
		StartedAt:      optNilTimePtr(item.StartedAt),
	}
	if item.ElapsedSeconds != nil {
		out.ElapsedSeconds = rest.NewOptFloat64(*item.ElapsedSeconds)
	}
	if item.Progress != nil {
		out.Progress = rest.NewOptFloat64(*item.Progress)
	}

	return out
}

func apiMutation(item clusterstate.Mutation) rest.MutationOperation {
	blockNumbers := make([]rest.MutationBlockNumber, 0, len(item.BlockNumbers))
	for _, block := range item.BlockNumbers {
		blockNumbers = append(blockNumbers, rest.MutationBlockNumber{
			PartitionId: block.PartitionID,
			Number:      u64(block.Number),
		})
	}
	partsToDoNames := item.PartsToDoNames
	if partsToDoNames == nil {
		partsToDoNames = []string{}
	}

	return rest.MutationOperation{
		NodeId:           item.Node.ID,
		Shard:            item.Node.Shard,
		Replica:          item.Node.Replica,
		OperationId:      "mutation|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" + item.MutationID,
		Kind:             "mutation",
		Database:         item.Database,
		Table:            item.Table,
		MutationId:       item.MutationID,
		AttemptId:        item.MutationID,
		Command:          item.Command,
		CreateTime:       item.CreateTime,
		IsDone:           item.IsDone,
		IsKilled:         item.IsKilled,
		PartsToDo:        u64(item.PartsToDo),
		PartsToDoNames:   partsToDoNames,
		BlockNumbers:     blockNumbers,
		LatestFailedPart: optNilStrPtr(item.LatestFailedPart),
		LatestFailTime:   optNilTimePtr(item.LatestFailTime),
		LatestFailReason: optNilStrPtr(item.LatestFailReason),
		Conditions:       apiEmbeddedConditions(item.Conditions),
	}
}

func apiReplicationQueueItem(item clusterstate.ReplicationQueueItem) rest.ReplicationQueueOperation {
	attemptID := strconv.FormatUint(item.Position, 10) + ":" + item.Type + ":" + deref(item.NewPartName)
	partsToMerge := item.PartsToMerge
	if partsToMerge == nil {
		partsToMerge = []string{}
	}

	return rest.ReplicationQueueOperation{
		NodeId:               item.Node.ID,
		Shard:                item.Node.Shard,
		Replica:              item.Node.Replica,
		OperationId:          "replication_queue|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" + attemptID,
		Kind:                 "replication_queue",
		Database:             item.Database,
		Table:                item.Table,
		ReplicaName:          item.ReplicaName,
		Position:             u64(item.Position),
		NodeName:             item.NodeName,
		AttemptId:            attemptID,
		Type:                 item.Type,
		CreateTime:           item.CreateTime,
		RequiredQuorum:       optU64(item.RequiredQuorum),
		SourceReplica:        optNilStrPtr(item.SourceReplica),
		NewPartName:          optNilStrPtr(item.NewPartName),
		PartsToMerge:         partsToMerge,
		IsDetach:             rest.NewOptBool(item.IsDetach),
		IsCurrentlyExecuting: item.IsCurrentlyExecuting,
		NumTries:             u64(item.NumTries),
		LastAttemptTime:      optNilTimePtr(item.LastAttemptTime),
		LastPostponeTime:     optNilTimePtr(item.LastPostponeTime),
		NumPostponed:         optU64(item.NumPostponed),
		PostponeReason:       optNilStrPtr(item.PostponeReason),
		LastException:        optNilStrPtr(item.LastException),
		Conditions:           apiEmbeddedConditions(item.Conditions),
	}
}

func apiPartEvent(item clusterstate.PartEvent) rest.PartEvent {
	eventID := "part_event|" + item.Node.ID + "|" + item.Database + "|" + item.Table + "|" +
		item.PartitionID + "|" + item.PartName + "|" + item.EventType + "|" + item.EventTimeMicrostamp
	mergedFrom := item.MergedFrom
	if mergedFrom == nil {
		mergedFrom = []string{}
	}

	return rest.PartEvent{
		NodeId:            item.Node.ID,
		Shard:             item.Node.Shard,
		Replica:           item.Node.Replica,
		EventId:           eventID,
		Database:          item.Database,
		Table:             item.Table,
		PartitionId:       item.PartitionID,
		PartName:          item.PartName,
		EventType:         item.EventType,
		EventTime:         item.EventTime,
		DurationMs:        u64(item.DurationMs),
		Rows:              u64(item.Rows),
		BytesCompressed:   u64(item.BytesCompressed),
		BytesUncompressed: u64(item.BytesUncompressed),
		ReadRows:          optU64(item.ReadRows),
		ReadBytes:         optU64(item.ReadBytes),
		MergedFrom:        mergedFrom,
		SourceDisk:        optNilStrPtr(item.SourceDisk),
		TargetDisk:        optNilStrPtr(item.TargetDisk),
		Error:             i64(item.Error),
		Exception:         optNilStrPtr(item.Exception),
	}
}

func apiCondition(item clusterstate.Condition) rest.Condition {
	return rest.Condition{
		ConditionId: item.ConditionID,
		Severity:    rest.Severity(item.Severity),
		Code:        item.Code,
		Message:     item.Message,
		Database:    optNilStrPtr(item.Database),
		Table:       optNilStrPtr(item.Table),
		Partition:   optNilStrPtr(item.Partition),
		PartitionId: optNilStrPtr(item.PartitionID),
		NodeId:      optNilStrPtr(item.NodeID),
		ObservedAt:  item.ObservedAt,
		Evidence:    apiEvidence(item.Evidence),
		Links:       apiLinks(item.Links),
	}
}

func apiEvidence(evidence map[string]any) rest.OptEvidence {
	if len(evidence) == 0 {
		return rest.OptEvidence{}
	}
	out := make(rest.Evidence, len(evidence))
	for key, value := range evidence {
		raw, err := json.Marshal(value)
		if err != nil {
			continue
		}
		out[key] = jx.Raw(raw)
	}

	return rest.NewOptEvidence(out)
}

func apiLinks(links map[string]string) rest.OptLinkMap {
	if len(links) == 0 {
		return rest.OptLinkMap{}
	}

	return rest.NewOptLinkMap(rest.LinkMap(links))
}

// apiEmbeddedConditions renders the bounded condition summaries embedded in
// resource payloads; the full condition objects live on /conditions.
func apiEmbeddedConditions(items []clusterstate.Condition) []rest.EmbeddedCondition {
	conditions := make([]rest.EmbeddedCondition, 0, len(items))
	for _, item := range items {
		conditions = append(conditions, rest.EmbeddedCondition{
			Code:     item.Code,
			Severity: rest.Severity(item.Severity),
			Message:  rest.NewOptString(item.Message),
			Evidence: apiEvidence(item.Evidence),
		})
	}

	return conditions
}

func apiPlacementCounts(counts map[string]int) rest.PlacementCounts {
	return rest.PlacementCounts{
		OnTarget:         counts["on_target"],
		OffTarget:        counts["off_target"],
		Split:            counts["split"],
		ReplicaDivergent: counts["replica_divergent"],
		MissingReplica:   counts["missing_replica"],
		Unknown:          counts["unknown"],
	}
}

func apiOperationCountsFixed(counts map[string]int) rest.PartitionOperationCounts {
	return rest.PartitionOperationCounts{
		Moving:   counts["moving"],
		Merging:  counts["merging"],
		Mutating: counts["mutating"],
		Fetching: counts["fetching"],
	}
}

// --- tiering mappers ------------------------------------------------------

func apiTieringPartition(verdict tiering.Verdict) rest.TieringPartition {
	disks := make([]rest.TieringDiskPart, 0, len(verdict.Disks))
	for _, disk := range verdict.Disks {
		disks = append(disks, rest.TieringDiskPart{
			Disk:  disk.Disk,
			Parts: u64(disk.Parts),
		})
	}

	return rest.TieringPartition{
		NodeId:        verdict.NodeID,
		Shard:         verdict.Shard,
		Replica:       verdict.Replica,
		Database:      verdict.Database,
		Table:         verdict.Table,
		Partition:     verdict.Partition,
		PartitionId:   verdict.PartitionID,
		Status:        rest.TieringPartitionStatus(verdict.Status),
		Decision:      rest.TieringDecision(verdict.Decision),
		Reason:        verdict.Reason,
		Rows:          u64(verdict.Rows),
		BytesOnDisk:   u64(verdict.BytesOnDisk),
		ActiveParts:   u64(verdict.ActiveParts),
		Disks:         disks,
		TargetDisk:    verdict.TargetDisk,
		HotVolume:     optNonEmpty(verdict.HotVolume),
		Policy:        apiTieringPolicy(verdict.Policy),
		Conditions:    apiTieringConditions(verdict.Conditions),
		Hold:          apiTieringHold(verdict.Hold),
		StateToken:    verdict.Token,
		ReconciledAt:  verdict.ReconciledAt,
		EffectiveMode: rest.TieringMode(verdict.EffectiveMode),
	}
}

func apiTieringHold(detail *tiering.HoldDetail) rest.OptTieringHoldDetail {
	if detail == nil {
		return rest.OptTieringHoldDetail{}
	}
	hold := rest.TieringHoldDetail{
		Gate:         detail.Gate,
		Window:       optNonEmpty(detail.Window),
		LastInsertAt: optTimePtr(detail.LastInsert),
		LastChangeAt: optTimePtr(detail.LastChange),
		ReleasesAt:   optTimePtr(detail.ReleasesAt),
		RetryAt:      optTimePtr(detail.RetryAt),
	}
	if detail.Failures != 0 {
		hold.Failures = rest.NewOptInt(detail.Failures)
	}

	return rest.NewOptTieringHoldDetail(hold)
}

func apiTieringPolicy(policy tiering.PolicySnapshot) rest.TieringPolicy {
	out := rest.TieringPolicy{
		Mode:                   rest.TieringMode(policy.Mode),
		AgeBasis:               rest.TieringAgeBasis(policy.AgeBasis),
		OlderThan:              optNonEmpty(policy.OlderThan),
		Field:                  optNonEmpty(policy.Field),
		QuietFor:               policy.QuietFor,
		TierFrozenAfter:        policy.TierFrozenAfter,
		TargetDisk:             policy.TargetDisk,
		HotVolume:              optNonEmpty(policy.HotVolume),
		OptimizeToParts:        u64(policy.OptimizeToParts),
		SkipOptimize:           policy.SkipOptimize,
		OptimizeOn:             rest.TieringOptimizeSide(policy.OptimizeOn),
		OptimizeSkipAboveBytes: rest.UInt64String(policy.OptimizeSkipAboveBytes),
		ResplitStrategy:        rest.TieringResplitStrategy(policy.ResplitStrategy),
		ResplitQuietFor:        policy.ResplitQuietFor,
		FragmentAbovePartCount: u64(policy.FragmentAbovePartCount),
	}
	if policy.KeepLast != 0 {
		out.KeepLast = optU64(policy.KeepLast)
	}

	return out
}

func apiTieringConditions(conditions []tiering.Condition) []rest.TieringCondition {
	items := make([]rest.TieringCondition, 0, len(conditions))
	for _, condition := range conditions {
		items = append(items, rest.TieringCondition{
			Severity:    rest.Severity(condition.Severity),
			Code:        condition.Code,
			Message:     condition.Message,
			ObservedAt:  condition.ObservedAt,
			NodeId:      optNonEmpty(condition.NodeID),
			Database:    optNonEmpty(condition.Database),
			Table:       optNonEmpty(condition.Table),
			Partition:   optNonEmpty(condition.Partition),
			PartitionId: optNonEmpty(condition.PartitionID),
		})
	}

	return items
}

func apiTieringStatus(status tiering.StatusSnapshot, legs []tiering.InFlightLeg) rest.TieringStatus {
	inFlight := make([]rest.TieringInFlightLeg, 0, len(legs))
	for _, leg := range legs {
		inFlight = append(inFlight, rest.TieringInFlightLeg{
			NodeId:      leg.NodeID,
			Database:    leg.Database,
			Table:       leg.Table,
			Partition:   leg.Partition,
			PartitionId: leg.PartitionID,
			Action:      rest.TieringDecision(leg.Action),
			Bytes:       u64(leg.Bytes),
			StartedAt:   leg.StartedAt,
			Source:      rest.TieringInFlightLegSource(leg.Source),
		})
	}

	return rest.TieringStatus{
		Mode:                    rest.TieringMode(status.Mode),
		PauseState:              rest.TieringPauseState(status.PauseState),
		PauseReason:             rest.NewOptString(string(status.PauseReason)),
		InFlight:                inFlight,
		MaxConcurrentPartitions: status.MaxConcurrentPartitions,
		MaxMovesPerCycle:        status.MaxMovesPerCycle,
		MaxBytesInFlight:        u64(status.MaxBytesInFlight),
		BytesInFlight:           u64(status.BytesInFlight),
		MaxBytesPerDay:          u64(status.MaxBytesPerDay),
		BytesMovedToday:         u64(status.BytesMovedToday),
		UpdatedAt:               status.UpdatedAt,
	}
}

func apiTieringHistory(entry tiering.HistoryEntry) rest.TieringHistoryEntry {
	return rest.TieringHistoryEntry{
		Time:        entry.Time,
		NodeId:      entry.NodeID,
		Database:    entry.Database,
		Table:       entry.Table,
		Partition:   entry.Partition,
		PartitionId: entry.PartitionID,
		Action:      rest.TieringDecision(entry.Action),
		Outcome:     entry.Outcome,
		DurationMs:  int(entry.Duration.Milliseconds()),
		Bytes:       u64(entry.Bytes),
		Error:       optNonEmpty(entry.Error),
		AttemptId:   optNonEmpty(entry.AttemptID),
	}
}

func apiTieringTablePlan(table tiering.TablePlan) rest.TieringTablePlan {
	return rest.TieringTablePlan{
		NodeId:         table.NodeID,
		Database:       table.Database,
		Table:          table.Table,
		ReconciledAt:   table.ReconciledAt,
		TickDurationMs: int(table.TickDuration.Milliseconds()),
		Generation:     table.Generation,
		LastError:      nilStr(table.LastError),
		Partitions:     len(table.Verdicts),
		Conditions:     apiTieringConditions(table.Conditions),
	}
}
