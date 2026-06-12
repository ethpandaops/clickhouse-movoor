package server

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/api/rest"
	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

// StateReader is the collector surface the HTTP API needs. Keeping this as an
// interface lets handlers be unit-tested without a ClickHouse fixture.
type StateReader interface {
	Watches() []clusterstate.Watch
	CollectNodes(rctx context.Context) clusterstate.Result[clusterstate.NodeStatus]
	CollectDisks(rctx context.Context) clusterstate.Result[clusterstate.Disk]
	CollectTables(rctx context.Context) clusterstate.Result[clusterstate.TableState]
	CollectTableColumns(rctx context.Context, watch clusterstate.Watch) clusterstate.Result[clusterstate.NodeColumns]
	CollectParts(rctx context.Context, watch clusterstate.Watch) clusterstate.Result[clusterstate.Part]
	CollectActiveParts(rctx context.Context, watch clusterstate.Watch) clusterstate.Result[clusterstate.Part]
	CollectDetachedParts(rctx context.Context, watch clusterstate.Watch) clusterstate.Result[clusterstate.DetachedPart]
	CollectMutations(rctx context.Context) clusterstate.Result[clusterstate.Mutation]
	CollectReplicationQueue(rctx context.Context) clusterstate.Result[clusterstate.ReplicationQueueItem]
	CollectPartEvents(rctx context.Context, from *time.Time, to *time.Time) clusterstate.Result[clusterstate.PartEvent]
	CollectOperations(rctx context.Context) clusterstate.Result[clusterstate.Operation]
	CollectConditions(rctx context.Context) clusterstate.Result[clusterstate.Condition]
	TableConditions(rctx context.Context, tables clusterstate.Result[clusterstate.TableState]) clusterstate.Result[clusterstate.Condition]
	PartitionConditions(rctx context.Context, watch clusterstate.Watch, parts clusterstate.Result[clusterstate.Part]) clusterstate.Result[clusterstate.Condition]
}

// apiHandler implements the generated rest.Handler interface. Routing,
// parameter decoding, enum validation, and response encoding are all owned by
// the generated server; this type only maps domain state to response types.
type apiHandler struct {
	log          *slog.Logger
	state        StateReader
	tiering      TieringController
	tieringStore *tiering.Store
}

var _ rest.Handler = (*apiHandler)(nil)

// --- shared problem constructors -------------------------------------------

func problem(status int, detail string) rest.ProblemDetails {
	return rest.ProblemDetails{
		Type:   aboutBlank(),
		Title:  http.StatusText(status),
		Status: int32(status), //nolint:gosec // HTTP status codes fit in int32.
		Detail: detail,
	}
}

func noStateProblem() rest.ProblemDetails {
	return problem(http.StatusServiceUnavailable, "cluster state collector is not configured")
}

func noRespondersProblem() rest.ProblemDetails {
	return problem(http.StatusServiceUnavailable, "no configured ClickHouse node responded")
}

func watchNotFoundProblem() rest.ProblemDetails {
	return problem(http.StatusNotFound, "table is not configured as a watch")
}

func noTieringProblem() rest.ProblemDetails {
	return problem(http.StatusServiceUnavailable, "tiering controller is not configured")
}

// noResponders reports whether a collection got zero responses from a
// non-empty node set — the only collection shape served as an error.
func noResponders[T any](result clusterstate.Result[T]) bool {
	return result.NodesExpected > 0 && result.NodesResponded == 0
}

func (h *apiHandler) watch(database string, table string) (clusterstate.Watch, bool) {
	watch := clusterstate.Watch{Database: database, Table: table}

	return watch, h.state != nil && slices.Contains(h.state.Watches(), watch)
}

// --- typed filter helpers ---------------------------------------------------

func matchOpt(opt rest.OptString, value string) bool {
	filter, ok := opt.Get()

	return !ok || filter == "" || filter == value
}

func matchOptBool(opt rest.OptBool, value bool) bool {
	filter, ok := opt.Get()

	return !ok || filter == value
}

func matchNode(nodeID rest.OptString, shard rest.OptString, replica rest.OptString, node chclient.Node) bool {
	return matchOpt(nodeID, node.ID) && matchOpt(shard, node.Shard) && matchOpt(replica, node.Replica)
}

// --- health -----------------------------------------------------------------

func (h *apiHandler) GetHealth(_ context.Context) (rest.GetHealthRes, error) {
	return &rest.Health{Status: "ok"}, nil
}

// --- cluster state reads ----------------------------------------------------

func (h *apiHandler) ListNodes(ctx context.Context, params rest.ListNodesParams) (rest.ListNodesRes, error) {
	if h.state == nil {
		out := rest.ListNodesServiceUnavailable(noStateProblem())

		return &out, nil
	}
	result := h.state.CollectNodes(ctx)
	if noResponders(result) {
		out := rest.ListNodesServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.Node, 0, len(result.Items))
	for _, item := range result.Items {
		if !matchOpt(params.NodeId, item.Node.ID) {
			continue
		}
		items = append(items, apiNode(item))
	}
	slices.SortStableFunc(items, func(a, b rest.Node) int {
		return cmp.Compare(a.NodeId, b.NodeId)
	})

	return &rest.NodesResponse{Collection: apiCollectionMeta(result), Items: items}, nil
}

func (h *apiHandler) ListStorageDisks(ctx context.Context, params rest.ListStorageDisksParams) (rest.ListStorageDisksRes, error) {
	if h.state == nil {
		out := rest.ListStorageDisksServiceUnavailable(noStateProblem())

		return &out, nil
	}
	result := h.state.CollectDisks(ctx)
	if noResponders(result) {
		out := rest.ListStorageDisksServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.StorageDisk, 0, len(result.Items))
	for _, item := range result.Items {
		if !matchOpt(params.NodeId, item.Node.ID) ||
			!matchOpt(params.Disk, item.Name) ||
			!matchOpt(params.Type, item.Type) ||
			!matchOptBool(params.Broken, item.IsBroken) {
			continue
		}
		items = append(items, apiDisk(item))
	}
	slices.SortStableFunc(items, func(a, b rest.StorageDisk) int {
		return cmp.Or(cmp.Compare(a.NodeId, b.NodeId), cmp.Compare(a.Disk, b.Disk))
	})

	return &rest.StorageDisksResponse{Collection: apiCollectionMeta(result), Items: items}, nil
}

func (h *apiHandler) ListTables(ctx context.Context, params rest.ListTablesParams) (rest.ListTablesRes, error) {
	if h.state == nil {
		out := rest.ListTablesServiceUnavailable(noStateProblem())

		return &out, nil
	}
	result := h.state.CollectTables(ctx)
	if noResponders(result) {
		out := rest.ListTablesServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	conditions := h.state.TableConditions(ctx, result).Items
	grouped := aggregateTables(result.Items, conditions)
	items := make([]rest.TableListItem, 0, len(grouped))
	for _, item := range grouped {
		if !matchOpt(params.Database, item.database) ||
			!matchOpt(params.Table, item.table) ||
			!matchOpt(params.Engine, item.engine) ||
			!matchOpt(params.StoragePolicy, item.storagePolicy) ||
			!matchOptBool(params.HasPartitions, item.activePartitions > 0) ||
			!matchOptBool(params.HasConditions, len(item.conditions) > 0) {
			continue
		}
		items = append(items, apiTableListItem(item))
	}
	slices.SortStableFunc(items, func(a, b rest.TableListItem) int {
		return cmp.Or(cmp.Compare(a.Database, b.Database), cmp.Compare(a.Table, b.Table))
	})

	return &rest.TablesResponse{Collection: apiCollectionMeta(result), Items: items}, nil
}

func (h *apiHandler) GetTable(ctx context.Context, params rest.GetTableParams) (rest.GetTableRes, error) {
	watch, ok := h.watch(params.Database, params.Table)
	if !ok {
		if h.state == nil {
			out := rest.GetTableServiceUnavailable(noStateProblem())

			return &out, nil
		}
		out := rest.GetTableNotFound(watchNotFoundProblem())

		return &out, nil
	}
	result := h.state.CollectTables(ctx)
	if noResponders(result) {
		out := rest.GetTableServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	conditions := h.state.TableConditions(ctx, result).Items
	item, found := aggregateTableDetail(result.Items, conditions, watch)
	if !found {
		out := rest.GetTableNotFound(problem(http.StatusNotFound, "watched table was not observed"))

		return &out, nil
	}

	return &rest.TableResponse{Collection: apiCollectionMeta(result), Item: apiTableDetail(item)}, nil
}

func (h *apiHandler) ListTableColumns(ctx context.Context, params rest.ListTableColumnsParams) (rest.ListTableColumnsRes, error) {
	watch, ok := h.watch(params.Database, params.Table)
	if !ok {
		if h.state == nil {
			out := rest.ListTableColumnsServiceUnavailable(noStateProblem())

			return &out, nil
		}
		out := rest.ListTableColumnsNotFound(watchNotFoundProblem())

		return &out, nil
	}
	result := h.state.CollectTableColumns(ctx, watch)
	if noResponders(result) {
		out := rest.ListTableColumnsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.NodeColumns, 0, len(result.Items))
	for _, item := range result.Items {
		if !matchOpt(params.NodeId, item.Node.ID) {
			continue
		}
		columns := make([]rest.TableColumn, 0, len(item.Columns))
		for _, column := range item.Columns {
			if !matchOpt(params.Name, column.Name) || !matchOpt(params.Kind, column.Kind) {
				continue
			}
			columns = append(columns, apiColumn(column))
		}
		items = append(items, rest.NodeColumns{
			NodeId:     item.Node.ID,
			Shard:      item.Node.Shard,
			Replica:    item.Node.Replica,
			Columns:    columns,
			Conditions: apiEmbeddedConditions(item.Conditions),
		})
	}
	slices.SortStableFunc(items, func(a, b rest.NodeColumns) int {
		return cmp.Compare(a.NodeId, b.NodeId)
	})

	return &rest.TableColumnsResponse{
		Collection: apiCollectionMeta(result),
		Database:   watch.Database,
		Table:      watch.Table,
		Items:      items,
		Conditions: []rest.EmbeddedCondition{},
	}, nil
}

func (h *apiHandler) ListTablePartitions(ctx context.Context, params rest.ListTablePartitionsParams) (rest.ListTablePartitionsRes, error) {
	watch, ok := h.watch(params.Database, params.Table)
	if !ok {
		if h.state == nil {
			out := rest.ListTablePartitionsServiceUnavailable(noStateProblem())

			return &out, nil
		}
		out := rest.ListTablePartitionsNotFound(watchNotFoundProblem())

		return &out, nil
	}
	result := h.state.CollectActiveParts(ctx, watch)
	if noResponders(result) {
		out := rest.ListTablePartitionsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	conditions := h.state.PartitionConditions(ctx, watch, result).Items
	partitions := aggregatePartitions(result.Items, conditions)
	items := make([]rest.Partition, 0, len(partitions))
	for _, partition := range partitions {
		if !partitionMatches(params, partition) {
			continue
		}
		if !matchOptBool(params.HasConditions, len(partition.conditions) > 0) {
			continue
		}
		items = append(items, apiPartition(partition))
	}
	slices.SortStableFunc(items, func(a, b rest.Partition) int {
		return cmp.Compare(a.PartitionId, b.PartitionId)
	})

	return &rest.TablePartitionsResponse{Collection: apiCollectionMeta(result), Items: items}, nil
}

func partitionMatches(params rest.ListTablePartitionsParams, item partitionAggregate) bool {
	if !matchOpt(params.PartitionId, item.partitionID) {
		return false
	}
	if placement, ok := params.Placement.Get(); ok && string(placement) != item.placement {
		return false
	}
	if disk, ok := params.Disk.Get(); ok && disk != "" {
		if _, found := item.disks[disk]; !found {
			return false
		}
	}
	if operation, ok := params.Operation.Get(); ok && operation != "" && !slices.Contains(item.operations, operation) {
		return false
	}
	if params.NodeId.Set || params.Shard.Set || params.Replica.Set {
		for _, placement := range item.placements {
			if matchNode(params.NodeId, params.Shard, params.Replica, placement.node) {
				return true
			}
		}

		return false
	}

	return true
}

func (h *apiHandler) ListTableParts(ctx context.Context, params rest.ListTablePartsParams) (rest.ListTablePartsRes, error) {
	watch, ok := h.watch(params.Database, params.Table)
	if !ok {
		if h.state == nil {
			out := rest.ListTablePartsServiceUnavailable(noStateProblem())

			return &out, nil
		}
		out := rest.ListTablePartsNotFound(watchNotFoundProblem())

		return &out, nil
	}
	minBytes, ok := parseOptByteBound(params.MinBytesOnDisk)
	if !ok {
		out := rest.ListTablePartsBadRequest(problem(http.StatusBadRequest, "minBytesOnDisk must be an unsigned integer"))

		return &out, nil
	}
	maxBytes, ok := parseOptByteBound(params.MaxBytesOnDisk)
	if !ok {
		out := rest.ListTablePartsBadRequest(problem(http.StatusBadRequest, "maxBytesOnDisk must be an unsigned integer"))

		return &out, nil
	}

	result := h.state.CollectParts(ctx, watch)
	if noResponders(result) {
		out := rest.ListTablePartsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.TablePart, 0, len(result.Items))
	for _, item := range result.Items {
		if !matchNode(params.NodeId, params.Shard, params.Replica, item.Node) ||
			!matchOpt(params.PartitionId, item.PartitionID) ||
			!matchOpt(params.PartName, item.Name) ||
			!matchOpt(params.Disk, item.Disk) ||
			!matchOptBool(params.Active, item.Active) {
			continue
		}
		if minBytes != nil && item.BytesOnDisk < *minBytes {
			continue
		}
		if maxBytes != nil && item.BytesOnDisk > *maxBytes {
			continue
		}
		items = append(items, apiPart(item))
	}
	slices.SortStableFunc(items, func(a, b rest.TablePart) int {
		return cmp.Or(cmp.Compare(a.NodeId, b.NodeId), cmp.Compare(a.PartName, b.PartName))
	})

	return &rest.TablePartsResponse{
		Collection: apiCollectionMeta(result),
		Database:   watch.Database,
		Table:      watch.Table,
		Items:      items,
	}, nil
}

func parseOptByteBound(value rest.OptUInt64String) (*uint64, bool) {
	raw, ok := value.Get()
	if !ok {
		return nil, true
	}
	parsed, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return nil, false
	}

	return &parsed, true
}

func (h *apiHandler) ListDetachedParts(ctx context.Context, params rest.ListDetachedPartsParams) (rest.ListDetachedPartsRes, error) {
	watch, ok := h.watch(params.Database, params.Table)
	if !ok {
		if h.state == nil {
			out := rest.ListDetachedPartsServiceUnavailable(noStateProblem())

			return &out, nil
		}
		out := rest.ListDetachedPartsNotFound(watchNotFoundProblem())

		return &out, nil
	}
	result := h.state.CollectDetachedParts(ctx, watch)
	if noResponders(result) {
		out := rest.ListDetachedPartsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.DetachedPart, 0, len(result.Items))
	reasonCounts := rest.OpenCountMap{}
	for _, item := range result.Items {
		if !matchNode(params.NodeId, params.Shard, params.Replica, item.Node) ||
			!matchOpt(params.PartitionId, deref(item.PartitionID)) ||
			!matchOpt(params.PartName, item.Name) ||
			!matchOpt(params.Disk, item.Disk) ||
			!matchOpt(params.Reason, deref(item.Reason)) {
			continue
		}
		reasonCounts[deref(item.Reason)]++
		items = append(items, apiDetachedPart(item))
	}
	slices.SortStableFunc(items, func(a, b rest.DetachedPart) int {
		return cmp.Or(cmp.Compare(a.NodeId, b.NodeId), cmp.Compare(a.PartName, b.PartName))
	})

	return &rest.DetachedPartsResponse{
		Collection: apiCollectionMeta(result),
		Database:   watch.Database,
		Table:      watch.Table,
		Items:      items,
		Counts:     rest.DetachedPartCounts{Total: len(items), ByReason: reasonCounts},
	}, nil
}

func (h *apiHandler) ListOperations(ctx context.Context, params rest.ListOperationsParams) (rest.ListOperationsRes, error) {
	if h.state == nil {
		out := rest.ListOperationsServiceUnavailable(noStateProblem())

		return &out, nil
	}
	result := h.state.CollectOperations(ctx)
	if noResponders(result) {
		out := rest.ListOperationsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.Operation, 0, len(result.Items))
	counts := rest.OperationKindCounts{}
	for _, item := range result.Items {
		if kind, ok := params.Kind.Get(); ok && string(kind) != item.Kind {
			continue
		}
		if !matchOpt(params.NodeId, item.NodeID) ||
			!matchOpt(params.Database, item.Database) ||
			!matchOpt(params.Table, item.Table) ||
			!matchOpt(params.PartitionId, deref(item.PartitionID)) {
			continue
		}
		switch item.Kind {
		case "move":
			counts.Move++
		case "merge":
			counts.Merge++
		case "mutation":
			counts.Mutation++
		case "fetch":
			counts.Fetch++
		case "replication_queue":
			counts.ReplicationQueue++
		}
		items = append(items, apiOperation(item))
	}
	slices.SortStableFunc(items, func(a, b rest.Operation) int {
		return cmp.Or(cmp.Compare(a.NodeId, b.NodeId), cmp.Compare(a.OperationId, b.OperationId))
	})

	return &rest.OperationsResponse{
		Collection: apiCollectionMeta(result),
		Items:      items,
		Counts:     rest.OperationCounts{Total: len(items), ByKind: counts},
	}, nil
}

func (h *apiHandler) ListMutations(ctx context.Context, params rest.ListMutationsParams) (rest.ListMutationsRes, error) {
	if h.state == nil {
		out := rest.ListMutationsServiceUnavailable(noStateProblem())

		return &out, nil
	}
	result := h.state.CollectMutations(ctx)
	if noResponders(result) {
		out := rest.ListMutationsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.MutationOperation, 0, len(result.Items))
	unfinishedCount := 0
	failedCount := 0
	for _, item := range result.Items {
		isFailed := item.LatestFailReason != nil && *item.LatestFailReason != ""
		if !matchOpt(params.NodeId, item.Node.ID) ||
			!matchOpt(params.Database, item.Database) ||
			!matchOpt(params.Table, item.Table) ||
			!matchOpt(params.MutationId, item.MutationID) ||
			!matchOptBool(params.Done, item.IsDone) ||
			!matchOptBool(params.Failed, isFailed) {
			continue
		}
		if !item.IsDone {
			unfinishedCount++
		}
		if isFailed {
			failedCount++
		}
		items = append(items, apiMutation(item))
	}
	slices.SortStableFunc(items, func(a, b rest.MutationOperation) int {
		return cmp.Or(cmp.Compare(a.NodeId, b.NodeId), cmp.Compare(a.MutationId, b.MutationId))
	})

	return &rest.MutationsResponse{
		Collection: apiCollectionMeta(result),
		Items:      items,
		Counts:     rest.MutationCounts{Total: len(items), Unfinished: unfinishedCount, Failed: failedCount},
	}, nil
}

func (h *apiHandler) ListReplicationQueue(ctx context.Context, params rest.ListReplicationQueueParams) (rest.ListReplicationQueueRes, error) {
	if h.state == nil {
		out := rest.ListReplicationQueueServiceUnavailable(noStateProblem())

		return &out, nil
	}
	result := h.state.CollectReplicationQueue(ctx)
	if noResponders(result) {
		out := rest.ListReplicationQueueServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.ReplicationQueueOperation, 0, len(result.Items))
	byType := rest.OpenCountMap{}
	executingCount := 0
	exceptionCount := 0
	for _, item := range result.Items {
		itemHasException := item.LastException != nil && *item.LastException != ""
		if !matchOpt(params.NodeId, item.Node.ID) ||
			!matchOpt(params.Database, item.Database) ||
			!matchOpt(params.Table, item.Table) ||
			!matchOpt(params.Type, item.Type) ||
			!matchOptBool(params.CurrentlyExecuting, item.IsCurrentlyExecuting) ||
			!matchOptBool(params.HasException, itemHasException) {
			continue
		}
		byType[item.Type]++
		if item.IsCurrentlyExecuting {
			executingCount++
		}
		if itemHasException {
			exceptionCount++
		}
		items = append(items, apiReplicationQueueItem(item))
	}
	slices.SortStableFunc(items, func(a, b rest.ReplicationQueueOperation) int {
		return cmp.Or(cmp.Compare(a.NodeId, b.NodeId), cmp.Compare(a.OperationId, b.OperationId))
	})

	return &rest.ReplicationQueueResponse{
		Collection: apiCollectionMeta(result),
		Items:      items,
		Counts: rest.ReplicationQueueCounts{
			Total:              len(items),
			CurrentlyExecuting: executingCount,
			WithException:      exceptionCount,
			ByType:             byType,
		},
	}, nil
}

func (h *apiHandler) ListPartEvents(ctx context.Context, params rest.ListPartEventsParams) (rest.ListPartEventsRes, error) {
	if h.state == nil {
		out := rest.ListPartEventsServiceUnavailable(noStateProblem())

		return &out, nil
	}
	var from, to *time.Time
	if value, ok := params.From.Get(); ok {
		from = &value
	}
	if value, ok := params.To.Get(); ok {
		to = &value
	}

	result := h.state.CollectPartEvents(ctx, from, to)
	if noResponders(result) {
		out := rest.ListPartEventsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.PartEvent, 0, len(result.Items))
	byEventType := rest.OpenCountMap{}
	errorCount := 0
	for _, item := range result.Items {
		if !matchNode(params.NodeId, rest.OptString{}, rest.OptString{}, item.Node) ||
			!matchOpt(params.Database, item.Database) ||
			!matchOpt(params.Table, item.Table) ||
			!matchOpt(params.PartitionId, item.PartitionID) ||
			!matchOpt(params.PartName, item.PartName) ||
			!matchOpt(params.EventType, item.EventType) {
			continue
		}
		if from != nil && item.EventTime.Before(*from) {
			continue
		}
		if to != nil && item.EventTime.After(*to) {
			continue
		}
		byEventType[item.EventType]++
		if item.Error != 0 {
			errorCount++
		}
		items = append(items, apiPartEvent(item))
	}
	slices.SortStableFunc(items, func(a, b rest.PartEvent) int {
		return cmp.Or(a.EventTime.Compare(b.EventTime), cmp.Compare(a.EventId, b.EventId))
	})

	return &rest.PartEventsResponse{
		Collection: apiCollectionMeta(result),
		Items:      items,
		Counts:     rest.PartEventCounts{Total: len(items), ByEventType: byEventType, WithErrors: errorCount},
	}, nil
}

func (h *apiHandler) ListConditions(ctx context.Context, params rest.ListConditionsParams) (rest.ListConditionsRes, error) {
	if h.state == nil {
		out := rest.ListConditionsServiceUnavailable(noStateProblem())

		return &out, nil
	}
	result := h.state.CollectConditions(ctx)
	if noResponders(result) {
		out := rest.ListConditionsServiceUnavailable(noRespondersProblem())

		return &out, nil
	}

	items := make([]rest.Condition, 0, len(result.Items))
	counts := rest.SeverityCounts{}
	for _, item := range result.Items {
		if severity, ok := params.Severity.Get(); ok && string(severity) != item.Severity {
			continue
		}
		if !matchOpt(params.Code, item.Code) ||
			!matchOpt(params.NodeId, deref(item.NodeID)) ||
			!matchOpt(params.Database, deref(item.Database)) ||
			!matchOpt(params.Table, deref(item.Table)) ||
			!matchOpt(params.PartitionId, deref(item.PartitionID)) {
			continue
		}
		switch item.Severity {
		case "critical":
			counts.Critical++
		case "warning":
			counts.Warning++
		case "info":
			counts.Info++
		}
		items = append(items, apiCondition(item))
	}
	slices.SortStableFunc(items, func(a, b rest.Condition) int {
		return cmp.Or(cmp.Compare(string(a.Severity), string(b.Severity)), cmp.Compare(a.ConditionId, b.ConditionId))
	})

	return &rest.ConditionsResponse{
		Collection: apiCollectionMeta(result),
		Items:      items,
		Counts:     rest.ConditionCounts{Total: len(items), BySeverity: counts},
	}, nil
}

// --- tiering ----------------------------------------------------------------

func (h *apiHandler) GetTieringPlan(_ context.Context, params rest.GetTieringPlanParams) (rest.GetTieringPlanRes, error) {
	if h.tieringStore == nil {
		out := rest.GetTieringPlanServiceUnavailable(noTieringProblem())

		return &out, nil
	}
	snapshot := h.tieringStore.Snapshot()
	// Slices start non-nil: the contract requires arrays, and nil marshals as
	// null — which the generated client's validation rejects outright. Empty
	// plans (fresh boot, zero watches) and filtered-empty plans are routine.
	response := rest.TieringPlanResponse{
		Tables: []rest.TieringTablePlan{},
		Items:  []rest.TieringPartition{},
	}
	for _, table := range snapshot.Tables {
		if !matchOpt(params.NodeId, table.NodeID) ||
			!matchOpt(params.Database, table.Database) ||
			!matchOpt(params.Table, table.Table) {
			continue
		}
		tablePlan := apiTieringTablePlan(table)
		for _, verdict := range table.Verdicts {
			if status, ok := params.Status.Get(); ok && string(status) != string(verdict.Status) {
				continue
			}
			if decision, ok := params.Decision.Get(); ok && string(decision) != string(verdict.Decision) {
				continue
			}
			if tiering.IsActionableDecision(verdict.Decision) {
				tablePlan.Actionable++
			}
			response.Items = append(response.Items, apiTieringPartition(verdict))
		}
		response.Tables = append(response.Tables, tablePlan)
	}

	return &response, nil
}

func (h *apiHandler) GetTieringStatus(_ context.Context) (rest.GetTieringStatusRes, error) {
	if h.tieringStore == nil {
		out := rest.GetTieringStatusServiceUnavailable(noTieringProblem())

		return &out, nil
	}
	var legs []tiering.InFlightLeg
	if h.tiering != nil {
		legs = h.tiering.InFlight()
	}
	out := apiTieringStatus(h.tieringStore.Status(), legs)

	return &out, nil
}

func (h *apiHandler) GetTieringHistory(_ context.Context, params rest.GetTieringHistoryParams) (rest.GetTieringHistoryRes, error) {
	if h.tieringStore == nil {
		out := rest.GetTieringHistoryServiceUnavailable(noTieringProblem())

		return &out, nil
	}
	entries := h.tieringStore.History()
	response := rest.TieringHistoryResponse{Items: make([]rest.TieringHistoryEntry, 0, len(entries))}
	for _, entry := range entries {
		if !matchOpt(params.NodeId, entry.NodeID) ||
			!matchOpt(params.Database, entry.Database) ||
			!matchOpt(params.Table, entry.Table) ||
			!matchOpt(params.PartitionId, entry.PartitionID) {
			continue
		}
		response.Items = append(response.Items, apiTieringHistory(entry))
	}

	return &response, nil
}

func (h *apiHandler) PauseTiering(_ context.Context) (rest.PauseTieringRes, error) {
	if h.tiering == nil {
		out := rest.PauseTieringServiceUnavailable(noTieringProblem())

		return &out, nil
	}
	out := apiTieringStatus(h.tiering.Pause(tiering.PauseReasonOperator), h.tiering.InFlight())

	return &out, nil
}

func (h *apiHandler) ResumeTiering(_ context.Context) (rest.ResumeTieringRes, error) {
	if h.tiering == nil {
		out := rest.ResumeTieringServiceUnavailable(noTieringProblem())

		return &out, nil
	}
	out := apiTieringStatus(h.tiering.Resume(), h.tiering.InFlight())

	return &out, nil
}

func (h *apiHandler) ApplyTieringPartition(ctx context.Context, req *rest.TieringApplyRequest, params rest.ApplyTieringPartitionParams) (rest.ApplyTieringPartitionRes, error) {
	if h.tiering == nil {
		out := rest.ApplyTieringPartitionServiceUnavailable(noTieringProblem())

		return &out, nil
	}
	entry, err := h.tiering.Apply(ctx, params.NodeId, params.Database, params.Table, params.PartitionId, req.StateToken)
	if problemOut := tieringActionProblem(entry, err); problemOut != nil {
		switch problemOut.Status {
		case http.StatusNotFound:
			out := rest.ApplyTieringPartitionNotFound(*problemOut)

			return &out, nil
		default:
			out := rest.ApplyTieringPartitionConflict(*problemOut)

			return &out, nil
		}
	}

	return &rest.TieringApplyResponse{Item: apiTieringHistory(entry)}, nil
}

func (h *apiHandler) RetryTieringPartition(ctx context.Context, req *rest.TieringApplyRequest, params rest.RetryTieringPartitionParams) (rest.RetryTieringPartitionRes, error) {
	if h.tiering == nil {
		out := rest.RetryTieringPartitionServiceUnavailable(noTieringProblem())

		return &out, nil
	}
	entry, err := h.tiering.Retry(ctx, params.NodeId, params.Database, params.Table, params.PartitionId, req.StateToken)
	if problemOut := tieringActionProblem(entry, err); problemOut != nil {
		switch problemOut.Status {
		case http.StatusNotFound:
			out := rest.RetryTieringPartitionNotFound(*problemOut)

			return &out, nil
		default:
			out := rest.RetryTieringPartitionConflict(*problemOut)

			return &out, nil
		}
	}

	return &rest.TieringApplyResponse{Item: apiTieringHistory(entry)}, nil
}

// tieringActionProblem maps a supervised apply/retry result to a problem:
// missing resources are 404, state-token mismatch / not-actionable / executor
// failures are 409 (the action cannot apply to the current plan). "started" is
// the detached-leg acknowledgement and is not an error.
func tieringActionProblem(entry tiering.HistoryEntry, err error) *rest.ProblemDetails {
	if err != nil {
		status := http.StatusConflict
		if isTieringNotFound(err) {
			status = http.StatusNotFound
		}
		out := problem(status, err.Error())

		return &out
	}
	if entry.Outcome != "" && entry.Outcome != "success" && entry.Outcome != "started" {
		detail := "tiering action failed"
		if entry.Error != "" {
			detail = "tiering action failed: " + entry.Error
		}
		out := problem(http.StatusConflict, detail)

		return &out
	}

	return nil
}

func isTieringNotFound(err error) bool {
	return errors.Is(err, tiering.ErrNodeNotConfigured) ||
		errors.Is(err, tiering.ErrWatchNotConfigured) ||
		errors.Is(err, tiering.ErrPartitionNotFound)
}
