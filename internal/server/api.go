package server

import (
	"context"
	"net/http"

	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
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
	CollectDetachedParts(rctx context.Context, watch clusterstate.Watch) clusterstate.Result[clusterstate.DetachedPart]
	CollectMutations(rctx context.Context) clusterstate.Result[clusterstate.Mutation]
	CollectReplicationQueue(rctx context.Context) clusterstate.Result[clusterstate.ReplicationQueueItem]
	CollectPartEvents(rctx context.Context) clusterstate.Result[clusterstate.PartEvent]
	CollectOperations(rctx context.Context) clusterstate.Result[clusterstate.Operation]
	CollectConditions(rctx context.Context) clusterstate.Result[clusterstate.Condition]
}

type problemError struct {
	Parameter string `json:"parameter,omitempty"`
	Detail    string `json:"detail"`
}

func (s *server) handleNodes(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}

	result := state.CollectNodes(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	nodeID := r.URL.Query().Get("nodeId")
	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		if nodeID != "" && item.Node.ID != nodeID {
			continue
		}
		items = append(items, apiNode(item))
	}
	sortMapItems(items, "nodeId", "")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
	})
}

func (s *server) handleStorageDisks(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}

	broken, ok := parseOptionalBool(w, r, "broken")
	if !ok {
		return
	}

	query := r.URL.Query()
	result := state.CollectDisks(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		if query.Get("nodeId") != "" && item.Node.ID != query.Get("nodeId") {
			continue
		}
		if query.Get("disk") != "" && item.Name != query.Get("disk") {
			continue
		}
		if query.Get("type") != "" && item.Type != query.Get("type") {
			continue
		}
		if broken != nil && item.IsBroken != *broken {
			continue
		}
		items = append(items, apiDisk(item))
	}
	sortMapItems(items, "nodeId", "disk")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
	})
}

//nolint:gocognit // The handler mirrors the explicit list filter contract.
func (s *server) handleTables(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}

	hasPartitions, ok := parseOptionalBool(w, r, "hasPartitions")
	if !ok {
		return
	}
	hasConditions, ok := parseOptionalBool(w, r, "hasConditions")
	if !ok {
		return
	}

	query := r.URL.Query()
	result := state.CollectTables(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	conditions := collectConditionsBestEffort(r, state)
	grouped := aggregateTables(result.Items, conditions)
	items := make([]map[string]any, 0, len(grouped))
	for _, item := range grouped {
		if query.Get("database") != "" && item.database != query.Get("database") {
			continue
		}
		if query.Get("table") != "" && item.table != query.Get("table") {
			continue
		}
		if query.Get("engine") != "" && item.engine != query.Get("engine") {
			continue
		}
		if query.Get("storagePolicy") != "" && item.storagePolicy != query.Get("storagePolicy") {
			continue
		}
		if hasPartitions != nil && (item.activePartitions > 0) != *hasPartitions {
			continue
		}
		if hasConditions != nil && (len(item.conditions) > 0) != *hasConditions {
			continue
		}
		items = append(items, apiTableListItem(item))
	}
	sortMapItems(items, "database", "table")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
	})
}

func (s *server) handleTable(w http.ResponseWriter, r *http.Request) {
	state, watch, ok := s.requireWatch(w, r)
	if !ok {
		return
	}

	result := state.CollectTables(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	conditions := collectConditionsBestEffort(r, state)
	item, found := aggregateTableDetail(result.Items, conditions, watch)
	if !found {
		s.writeProblem(w, r, problemDetails{
			Type:     "about:blank",
			Title:    http.StatusText(http.StatusNotFound),
			Status:   http.StatusNotFound,
			Detail:   "watched table was not observed",
			Instance: r.URL.RequestURI(),
		})

		return
	}

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"item":       apiTableDetail(item),
	})
}

func (s *server) handleTableColumns(w http.ResponseWriter, r *http.Request) {
	state, watch, ok := s.requireWatch(w, r)
	if !ok {
		return
	}

	result := state.CollectTableColumns(r.Context(), watch)
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	query := r.URL.Query()
	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		if query.Get("nodeId") != "" && item.Node.ID != query.Get("nodeId") {
			continue
		}
		nodeColumns := apiNodeRef(item.Node)
		columns := make([]map[string]any, 0, len(item.Columns))
		for _, column := range item.Columns {
			if query.Get("name") != "" && column.Name != query.Get("name") {
				continue
			}
			if query.Get("kind") != "" && column.Kind != query.Get("kind") {
				continue
			}
			columns = append(columns, apiColumn(column))
		}
		nodeColumns["columns"] = columns
		nodeColumns["conditions"] = apiConditions(item.Conditions)
		items = append(items, nodeColumns)
	}
	sortMapItems(items, "nodeId", "")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"database":   watch.Database,
		"table":      watch.Table,
		"items":      items,
		"conditions": []map[string]any{},
	})
}

func (s *server) handleTablePartitions(w http.ResponseWriter, r *http.Request) {
	state, watch, ok := s.requireWatch(w, r)
	if !ok {
		return
	}

	if !validateClosedQuery(w, r, "placement", map[string]struct{}{
		"on_target": {}, "off_target": {}, "split": {}, "replica_divergent": {}, "missing_replica": {}, "unknown": {},
	}) {
		return
	}
	hasConditions, ok := parseOptionalBool(w, r, "hasConditions")
	if !ok {
		return
	}

	result := state.CollectParts(r.Context(), watch)
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	conditions := collectConditionsBestEffort(r, state)
	partitions := aggregatePartitions(result.Items, conditions)
	query := r.URL.Query()
	items := make([]map[string]any, 0, len(partitions))
	for _, partition := range partitions {
		if !partitionMatches(query, partition) {
			continue
		}
		if hasConditions != nil && (len(partition.conditions) > 0) != *hasConditions {
			continue
		}
		items = append(items, apiPartition(partition))
	}
	sortMapItems(items, "partitionId", "")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
	})
}

//nolint:gocognit // The handler mirrors the explicit list filter contract.
func (s *server) handleTableParts(w http.ResponseWriter, r *http.Request) {
	state, watch, ok := s.requireWatch(w, r)
	if !ok {
		return
	}

	active, ok := parseOptionalBool(w, r, "active")
	if !ok {
		return
	}
	minBytes, ok := parseOptionalUint64(w, r, "minBytesOnDisk")
	if !ok {
		return
	}
	maxBytes, ok := parseOptionalUint64(w, r, "maxBytesOnDisk")
	if !ok {
		return
	}

	query := r.URL.Query()
	result := state.CollectParts(r.Context(), watch)
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		if !nodePartMatches(query, item.Node) {
			continue
		}
		if query.Get("partitionId") != "" && item.PartitionID != query.Get("partitionId") {
			continue
		}
		if query.Get("partName") != "" && item.Name != query.Get("partName") {
			continue
		}
		if query.Get("disk") != "" && item.Disk != query.Get("disk") {
			continue
		}
		if active != nil && item.Active != *active {
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
	sortMapItems(items, "nodeId", "partName")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"database":   watch.Database,
		"table":      watch.Table,
		"items":      items,
	})
}

func (s *server) handleDetachedParts(w http.ResponseWriter, r *http.Request) {
	state, watch, ok := s.requireWatch(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	result := state.CollectDetachedParts(r.Context(), watch)
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	reasonCounts := make(map[string]int)
	for _, item := range result.Items {
		if !nodePartMatches(query, item.Node) {
			continue
		}
		if query.Get("partitionId") != "" && deref(item.PartitionID) != query.Get("partitionId") {
			continue
		}
		if query.Get("partName") != "" && item.Name != query.Get("partName") {
			continue
		}
		if query.Get("disk") != "" && item.Disk != query.Get("disk") {
			continue
		}
		if query.Get("reason") != "" && deref(item.Reason) != query.Get("reason") {
			continue
		}
		reasonCounts[deref(item.Reason)]++
		items = append(items, apiDetachedPart(item))
	}
	sortMapItems(items, "nodeId", "partName")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"database":   watch.Database,
		"table":      watch.Table,
		"items":      items,
		"counts": map[string]any{
			"total":    len(items),
			"byReason": reasonCounts,
		},
	})
}

func (s *server) handleOperations(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}
	if !validateClosedQuery(w, r, "kind", map[string]struct{}{
		"move": {}, "merge": {}, "mutation": {}, "fetch": {}, "replication_queue": {},
	}) {
		return
	}

	query := r.URL.Query()
	result := state.CollectOperations(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	counts := operationKindCounts()
	for _, item := range result.Items {
		if query.Get("kind") != "" && item.Kind != query.Get("kind") {
			continue
		}
		if query.Get("nodeId") != "" && item.NodeID != query.Get("nodeId") {
			continue
		}
		if query.Get("database") != "" && item.Database != query.Get("database") {
			continue
		}
		if query.Get("table") != "" && item.Table != query.Get("table") {
			continue
		}
		if query.Get("partitionId") != "" && deref(item.PartitionID) != query.Get("partitionId") {
			continue
		}
		counts[item.Kind]++
		items = append(items, apiOperation(item))
	}
	sortMapItems(items, "nodeId", "operationId")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
		"counts": map[string]any{
			"total":  len(items),
			"byKind": counts,
		},
	})
}

//nolint:gocognit // The handler mirrors the explicit list filter contract.
func (s *server) handleMutations(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}
	done, ok := parseOptionalBool(w, r, "done")
	if !ok {
		return
	}
	failed, ok := parseOptionalBool(w, r, "failed")
	if !ok {
		return
	}

	query := r.URL.Query()
	result := state.CollectMutations(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	unfinishedCount := 0
	failedCount := 0
	for _, item := range result.Items {
		if !nodePartMatches(query, item.Node) {
			continue
		}
		if query.Get("database") != "" && item.Database != query.Get("database") {
			continue
		}
		if query.Get("table") != "" && item.Table != query.Get("table") {
			continue
		}
		if query.Get("mutationId") != "" && item.MutationID != query.Get("mutationId") {
			continue
		}
		isFailed := item.LatestFailReason != nil && *item.LatestFailReason != ""
		if done != nil && item.IsDone != *done {
			continue
		}
		if failed != nil && isFailed != *failed {
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
	sortMapItems(items, "nodeId", "mutationId")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
		"counts": map[string]any{
			"total":      len(items),
			"unfinished": unfinishedCount,
			"failed":     failedCount,
		},
	})
}

//nolint:gocognit // The handler mirrors the explicit list filter contract.
func (s *server) handleReplicationQueue(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}
	currentlyExecuting, ok := parseOptionalBool(w, r, "currentlyExecuting")
	if !ok {
		return
	}
	hasException, ok := parseOptionalBool(w, r, "hasException")
	if !ok {
		return
	}

	query := r.URL.Query()
	result := state.CollectReplicationQueue(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	byType := make(map[string]int)
	executingCount := 0
	exceptionCount := 0
	for _, item := range result.Items {
		if !nodePartMatches(query, item.Node) {
			continue
		}
		if query.Get("database") != "" && item.Database != query.Get("database") {
			continue
		}
		if query.Get("table") != "" && item.Table != query.Get("table") {
			continue
		}
		if query.Get("type") != "" && item.Type != query.Get("type") {
			continue
		}
		itemHasException := item.LastException != nil && *item.LastException != ""
		if currentlyExecuting != nil && item.IsCurrentlyExecuting != *currentlyExecuting {
			continue
		}
		if hasException != nil && itemHasException != *hasException {
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
	sortMapItems(items, "nodeId", "operationId")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
		"counts": map[string]any{
			"total":              len(items),
			"currentlyExecuting": executingCount,
			"withException":      exceptionCount,
			"byType":             byType,
		},
	})
}

//nolint:gocognit // The handler mirrors the explicit list filter contract.
func (s *server) handlePartEvents(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}
	from, ok := parseOptionalTime(w, r, "from")
	if !ok {
		return
	}
	to, ok := parseOptionalTime(w, r, "to")
	if !ok {
		return
	}

	query := r.URL.Query()
	result := state.CollectPartEvents(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	byEventType := make(map[string]int)
	errorCount := 0
	for _, item := range result.Items {
		if !nodePartMatches(query, item.Node) {
			continue
		}
		if query.Get("database") != "" && item.Database != query.Get("database") {
			continue
		}
		if query.Get("table") != "" && item.Table != query.Get("table") {
			continue
		}
		if query.Get("partitionId") != "" && item.PartitionID != query.Get("partitionId") {
			continue
		}
		if query.Get("partName") != "" && item.PartName != query.Get("partName") {
			continue
		}
		if query.Get("eventType") != "" && item.EventType != query.Get("eventType") {
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
	sortMapItems(items, "eventTime", "eventId")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
		"counts": map[string]any{
			"total":       len(items),
			"byEventType": byEventType,
			"withErrors":  errorCount,
		},
	})
}

//nolint:gocognit // The handler mirrors the explicit list filter contract.
func (s *server) handleConditions(w http.ResponseWriter, r *http.Request) {
	state, ok := s.requireState(w, r)
	if !ok {
		return
	}
	if !validateClosedQuery(w, r, "severity", map[string]struct{}{
		"critical": {}, "warning": {}, "info": {},
	}) {
		return
	}

	query := r.URL.Query()
	result := state.CollectConditions(r.Context())
	if s.writeNoResponders(w, r, result.NodesExpected, result.NodesResponded) {
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	counts := severityCounts()
	for _, item := range result.Items {
		if query.Get("severity") != "" && item.Severity != query.Get("severity") {
			continue
		}
		if query.Get("code") != "" && item.Code != query.Get("code") {
			continue
		}
		if query.Get("nodeId") != "" && deref(item.NodeID) != query.Get("nodeId") {
			continue
		}
		if query.Get("database") != "" && deref(item.Database) != query.Get("database") {
			continue
		}
		if query.Get("table") != "" && deref(item.Table) != query.Get("table") {
			continue
		}
		if query.Get("partitionId") != "" && deref(item.PartitionID) != query.Get("partitionId") {
			continue
		}
		counts[item.Severity]++
		items = append(items, apiCondition(item))
	}
	sortMapItems(items, "severity", "conditionId")

	s.writeJSON(w, r, map[string]any{
		"collection": collectionMeta(result),
		"items":      items,
		"counts": map[string]any{
			"total":      len(items),
			"bySeverity": counts,
		},
	})
}
