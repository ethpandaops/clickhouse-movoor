package clusterstate

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
)

func TestResultPartial(t *testing.T) {
	t.Parallel()

	require.False(t, Result[int]{}.Partial())
	require.True(t, Result[int]{NodesFailed: 1}.Partial())
	require.True(t, Result[int]{Warnings: []Warning{{Code: "warn"}}}.Partial())
}

func TestCollectorWatchesAndClients(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	collector := New(pool, time.Second, []Watch{{Database: "db", Table: "tbl"}})

	watches := collector.Watches()
	require.Equal(t, []Watch{{Database: "db", Table: "tbl"}}, watches)
	watches[0].Table = "mutated"
	require.Equal(t, "tbl", collector.Watches()[0].Table)

	require.Nil(t, (*Collector)(nil).Watches())
	require.Len(t, collector.clients(), 2)
	require.Nil(t, (*Collector)(nil).clients())
	require.Nil(t, (&Collector{}).clients())
}

func TestCollectorQueryContext(t *testing.T) {
	t.Parallel()

	withTimeout := &Collector{queryTimeout: time.Minute}
	ctx, cancel := withTimeout.queryContext(t.Context())
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(time.Minute), deadline, 2*time.Second)

	withoutTimeout := &Collector{}
	ctx, cancel = withoutTimeout.queryContext(t.Context())
	defer cancel()
	_, ok = ctx.Deadline()
	require.False(t, ok)
}

func TestCollectPerNode(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	collector := New(pool, time.Second, nil)
	result := collectPerNode(t.Context(), collector, 1, func(_ context.Context, client chclient.Client) ([]string, *Warning) {
		if client.Node.ID == "node-b" {
			return nil, &Warning{Kind: warningKindReachability, Code: "node_unreachable", Message: "dial", NodeID: client.Node.ID}
		}

		return []string{client.Node.ID}, nil
	})

	require.Equal(t, 2, result.NodesExpected)
	require.Equal(t, 1, result.NodesResponded)
	require.Equal(t, 1, result.NodesFailed)
	require.Equal(t, []string{"node-a"}, result.Items)
	require.Len(t, result.Warnings, 1)
}

func TestQueryWarningClassification(t *testing.T) {
	t.Parallel()

	reachable := queryWarning("node-a", "system_query_failed", &clickhouse.Exception{Code: 47, Message: "unknown identifier"})
	require.Equal(t, warningKindQueryError, reachable.Kind)
	require.Equal(t, "system_query_failed", reachable.Code)

	unreachable := queryWarning("node-a", "system_query_failed", io.ErrUnexpectedEOF)
	require.Equal(t, warningKindReachability, unreachable.Kind)
	require.Equal(t, "node_unreachable", unreachable.Code)
}

func TestTableConditionsAndIsWatched(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	collector := New(pool, time.Second, []Watch{{Database: "db", Table: "tbl"}})
	require.True(t, collector.isWatched("db", "tbl"))
	require.False(t, collector.isWatched("db", "other"))

	conditions := collector.tableConditions([]TableState{{
		Node:     chclient.Node{ID: "node-a"},
		Database: "db",
		Table:    "tbl",
	}})
	require.Len(t, conditions, 1)
	require.Equal(t, "table_missing_on_node", conditions[0].Code)
	require.Equal(t, "node-b", *conditions[0].NodeID)

	publicResult := collector.TableConditions(t.Context(), Result[TableState]{
		Items: []TableState{{
			Node:     chclient.Node{ID: "node-a"},
			Database: "db",
			Table:    "tbl",
		}},
	})
	require.Len(t, publicResult.Items, 1)
	require.Equal(t, "table_missing_on_node", publicResult.Items[0].Code)
}

func TestPartitionConditions(t *testing.T) {
	t.Parallel()

	parts := []Part{
		{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, Database: "db", Table: "tbl", Partition: "p", PartitionID: "pid", Active: true, Disk: "default"},
		{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, Database: "db", Table: "tbl", Partition: "p", PartitionID: "pid", Active: true, Disk: "s3_cache"},
		{Node: chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}, Database: "db", Table: "tbl", Partition: "p", PartitionID: "pid", Active: true, Disk: "default"},
		{Node: chclient.Node{ID: "node-c", Shard: "shard2", Replica: "replica1"}, Database: "db", Table: "tbl", Partition: "p2", PartitionID: "pid2", Active: false, Disk: "default"},
	}

	conditions := partitionConditions(parts)
	codes := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		codes = append(codes, condition.Code)
	}
	require.ElementsMatch(t, []string{"partition_split_across_disks", "replica_part_mismatch"}, codes)

	publicResult := (&Collector{}).PartitionConditions(t.Context(), Watch{}, Result[Part]{Items: parts})
	require.Len(t, publicResult.Items, 2)
}

func TestConditionBuilders(t *testing.T) {
	t.Parallel()

	partitionID := "pid"
	reason := "broken"
	detached := detachedPartCondition(DetachedPart{
		Node:        chclient.Node{ID: "node-a"},
		Database:    "db",
		Table:       "tbl",
		PartitionID: &partitionID,
		Name:        "part-a",
		Disk:        "default",
		Reason:      &reason,
	})
	require.Equal(t, "detached_part_present", detached.Code)
	require.Equal(t, "part-a", detached.Evidence["partName"])

	failReason := "parse failed"
	mutation := mutationFailureCondition(Mutation{
		Node:             chclient.Node{ID: "node-a"},
		Database:         "db",
		Table:            "tbl",
		MutationID:       "mut-1",
		LatestFailReason: &failReason,
	})
	require.Equal(t, "mutation_failed", mutation.Code)
	require.Equal(t, failReason, mutation.Message)

	queue := replicationQueueExceptionCondition(ReplicationQueueItem{
		Node:     chclient.Node{ID: "node-a"},
		Database: "db",
		Table:    "tbl",
		NodeName: "/queue/1",
		Type:     "GET_PART",
		Position: 1,
	})
	require.Equal(t, "replication_queue_exception", queue.Code)
	require.Equal(t, "replication queue item has a latest exception", queue.Message)

	observedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	warning := collectionWarningCondition(Warning{Kind: "query_error", Code: "failed", Message: "bad"}, observedAt)
	require.Equal(t, "failed", warning.Code)
	require.Nil(t, warning.NodeID)
	require.Equal(t, observedAt, warning.ObservedAt)
}

func TestWarningsAndRespondedNodes(t *testing.T) {
	t.Parallel()

	existing := []Warning{{Kind: "query", Code: "a", NodeID: "node-a", Message: "first"}}
	out := appendWarnings(existing,
		Warning{Kind: "query", Code: "a", NodeID: "node-a", Message: "first"},
		Warning{Kind: warningKindReachability, Code: "node_unreachable", NodeID: "node-b", Message: "dial"},
	)
	require.Len(t, out, 2)
	require.Equal(t, 2, respondedNodes(3, out))
	require.Equal(t, "query\x00a\x00node-a\x00first", warningKey(existing[0]))
}

func TestJoinUniqueWatchValidationError(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{})
	err := joinUniqueWatchValidationError(nil, seen, "first")
	err = joinUniqueWatchValidationError(err, seen, "first")
	err = joinUniqueWatchValidationError(err, seen, "second")

	require.ErrorContains(t, err, "first")
	require.ErrorContains(t, err, "second")
	require.Len(t, seen, 2)
}

func TestOperationAdapters(t *testing.T) {
	t.Parallel()

	failReason := "failed"
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mutationOp := operationFromMutation(Mutation{
		Node:             chclient.Node{ID: "node-a"},
		Database:         "db",
		Table:            "tbl",
		MutationID:       "mut-1",
		CreateTime:       started,
		LatestFailReason: &failReason,
	})
	require.Equal(t, "mutation", mutationOp.Kind)
	require.Equal(t, "running", mutationOp.State)
	require.Equal(t, &failReason, mutationOp.LatestMessage)
	require.Equal(t, &started, mutationOp.StartedAt)

	partName := "part-a"
	postponeReason := "postponed"
	queueOp := operationFromReplicationQueue(ReplicationQueueItem{
		Node:                 chclient.Node{ID: "node-a"},
		Database:             "db",
		Table:                "tbl",
		Position:             3,
		Type:                 "GET_PART",
		NewPartName:          &partName,
		IsCurrentlyExecuting: true,
		PostponeReason:       &postponeReason,
		CreateTime:           started,
	})
	require.Equal(t, "fetch", queueOp.Kind)
	require.Equal(t, "running", queueOp.State)
	require.Equal(t, "3:GET_PART:part-a", queueOp.AttemptID)
	require.Equal(t, &postponeReason, queueOp.LatestMessage)

	queuedOp := operationFromReplicationQueue(ReplicationQueueItem{
		Node:       chclient.Node{ID: "node-a"},
		Database:   "db",
		Table:      "tbl",
		Position:   4,
		Type:       "MERGE_PARTS",
		CreateTime: started,
	})
	require.Equal(t, "replication_queue", queuedOp.Kind)
	require.Equal(t, "queued", queuedOp.State)
}

func TestSmallHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, []MutationBlockNumber{{PartitionID: "p1", Number: 2}, {PartitionID: "p2", Number: 0}},
		mutationBlockNumbers([]string{"p1", "p2", "p3"}, []int64{2, -1}))
	require.Equal(t, "materialized", columnKind("MATERIALIZED"))
	require.Equal(t, "alias", columnKind("alias"))
	require.Equal(t, "regular", columnKind(""))
	require.True(t, errorsIsNoRows(sql.ErrNoRows))
	require.False(t, errorsIsNoRows(errors.New("other")))

	require.Nil(t, nullableStringPtr(sql.NullString{}))
	require.Nil(t, nullableStringPtr(sql.NullString{String: "", Valid: true}))
	require.Equal(t, "x", *nullableStringPtr(sql.NullString{String: "x", Valid: true}))

	require.Nil(t, nullableTimePtr(sql.NullTime{}))
	require.Nil(t, nullableTimePtr(sql.NullTime{Time: time.Unix(0, 0), Valid: true}))
	require.Equal(t, time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC), *nullableTimePtr(sql.NullTime{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("test", 3600)), Valid: true}))

	require.Nil(t, nullableInt64Ptr(sql.NullInt64{}))
	require.Equal(t, int64(-1), *nullableInt64Ptr(sql.NullInt64{Int64: -1, Valid: true}))
	require.Nil(t, nullableUint64Ptr(sql.NullInt64{}))
	require.Nil(t, nullableUint64Ptr(sql.NullInt64{Int64: -1, Valid: true}))
	require.Equal(t, uint64(1), *nullableUint64Ptr(sql.NullInt64{Int64: 1, Valid: true}))

	require.Nil(t, nonEmptyStringPtr(""))
	require.Equal(t, "x", *nonEmptyStringPtr("x"))
	require.Nil(t, nonZeroTimePtr(time.Time{}))
	require.Nil(t, nonZeroTimePtr(time.Unix(0, 0)))
	require.Equal(t, uint64(0), uint64FromInt64(-1))
	require.Equal(t, uint64(2), uint64FromInt64(2))
	require.Empty(t, derefString(nil))
	require.Nil(t, firstNonNilString(nil, nil))

	first := "first"
	second := "second"
	require.Equal(t, &second, firstNonNilString(nil, &second))
	require.Equal(t, &first, firstNonNilString(&first, &second))
	require.Equal(t, "a|b", opaqueID("a", "b"))
	require.Equal(t, "a,b", diskSignature(map[string]struct{}{"b": {}, "a": {}}))
	require.Equal(t, []string{"a", "b"}, sortedKeys(map[string]struct{}{"b": {}, "a": {}}))
}

func testPool(t *testing.T) *chclient.Pool {
	t.Helper()

	pool, err := chclient.NewPool(chclient.Config{Nodes: []chclient.NodeConfig{
		{Name: "node-a", Shard: "shard1", Replica: "replica1", DSN: "clickhouse://default@localhost:9000/default"},
		{Name: "node-b", Shard: "shard1", Replica: "replica2", DSN: "clickhouse://default@localhost:9001/default"},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })

	return pool
}
