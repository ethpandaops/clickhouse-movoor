package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/api/rest"
	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

// coverageState serves canned collection results for handler branch tests.
type coverageState struct {
	operations clusterstate.Result[clusterstate.Operation]
	parts      clusterstate.Result[clusterstate.Part]
	watches    []clusterstate.Watch
}

func (s coverageState) Watches() []clusterstate.Watch { return s.watches }

func (s coverageState) CollectNodes(context.Context) clusterstate.Result[clusterstate.NodeStatus] {
	return clusterstate.Result[clusterstate.NodeStatus]{}
}

func (s coverageState) CollectDisks(context.Context) clusterstate.Result[clusterstate.Disk] {
	return clusterstate.Result[clusterstate.Disk]{}
}

func (s coverageState) CollectTables(context.Context) clusterstate.Result[clusterstate.TableState] {
	return clusterstate.Result[clusterstate.TableState]{}
}

func (s coverageState) CollectTableColumns(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.NodeColumns] {
	return clusterstate.Result[clusterstate.NodeColumns]{}
}

func (s coverageState) CollectParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	return s.parts
}

func (s coverageState) CollectActiveParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.Part] {
	return s.parts
}

func (s coverageState) CollectDetachedParts(context.Context, clusterstate.Watch) clusterstate.Result[clusterstate.DetachedPart] {
	return clusterstate.Result[clusterstate.DetachedPart]{}
}

func (s coverageState) CollectMutations(context.Context) clusterstate.Result[clusterstate.Mutation] {
	return clusterstate.Result[clusterstate.Mutation]{}
}

func (s coverageState) CollectReplicationQueue(context.Context) clusterstate.Result[clusterstate.ReplicationQueueItem] {
	return clusterstate.Result[clusterstate.ReplicationQueueItem]{}
}

func (s coverageState) CollectPartEvents(context.Context, *time.Time, *time.Time) clusterstate.Result[clusterstate.PartEvent] {
	return clusterstate.Result[clusterstate.PartEvent]{}
}

func (s coverageState) CollectOperations(context.Context) clusterstate.Result[clusterstate.Operation] {
	return s.operations
}

func (s coverageState) CollectConditions(context.Context) clusterstate.Result[clusterstate.Condition] {
	return clusterstate.Result[clusterstate.Condition]{}
}

func (s coverageState) TableConditions(context.Context, clusterstate.Result[clusterstate.TableState]) clusterstate.Result[clusterstate.Condition] {
	return clusterstate.Result[clusterstate.Condition]{}
}

func (s coverageState) PartitionConditions(context.Context, clusterstate.Watch, clusterstate.Result[clusterstate.Part]) clusterstate.Result[clusterstate.Condition] {
	return clusterstate.Result[clusterstate.Condition]{}
}

func TestHandlerUnknownWatchBranches(t *testing.T) {
	t.Parallel()

	h := &apiHandler{log: slog.New(slog.DiscardHandler), state: coverageState{}}

	columns, err := h.ListTableColumns(t.Context(), rest.ListTableColumnsParams{Database: "db", Table: "nope"})
	require.NoError(t, err)
	require.IsType(t, &rest.ListTableColumnsNotFound{}, columns)

	partitions, err := h.ListTablePartitions(t.Context(), rest.ListTablePartitionsParams{Database: "db", Table: "nope"})
	require.NoError(t, err)
	require.IsType(t, &rest.ListTablePartitionsNotFound{}, partitions)

	parts, err := h.ListTableParts(t.Context(), rest.ListTablePartsParams{Database: "db", Table: "nope"})
	require.NoError(t, err)
	require.IsType(t, &rest.ListTablePartsNotFound{}, parts)

	detached, err := h.ListDetachedParts(t.Context(), rest.ListDetachedPartsParams{Database: "db", Table: "nope"})
	require.NoError(t, err)
	require.IsType(t, &rest.ListDetachedPartsNotFound{}, detached)
}

func TestHandlerByteBoundOverflow(t *testing.T) {
	t.Parallel()

	// The schema pattern keeps non-digits out at decode time; only values past
	// uint64 reach the handler's own bound parsing.
	overflow := rest.NewOptUInt64String("99999999999999999999999999")
	h := &apiHandler{
		log:   slog.New(slog.DiscardHandler),
		state: coverageState{watches: []clusterstate.Watch{{Database: "db", Table: "tbl"}}},
	}

	res, err := h.ListTableParts(t.Context(), rest.ListTablePartsParams{Database: "db", Table: "tbl", MinBytesOnDisk: overflow})
	require.NoError(t, err)
	require.IsType(t, &rest.ListTablePartsBadRequest{}, res)

	res, err = h.ListTableParts(t.Context(), rest.ListTablePartsParams{Database: "db", Table: "tbl", MaxBytesOnDisk: overflow})
	require.NoError(t, err)
	require.IsType(t, &rest.ListTablePartsBadRequest{}, res)

	parsed, ok := parseOptByteBound(rest.NewOptUInt64String("42"))
	require.True(t, ok)
	require.Equal(t, uint64(42), *parsed)
}

func TestHandlerOperationKindCounts(t *testing.T) {
	t.Parallel()

	operations := make([]clusterstate.Operation, 0, 5)
	for _, kind := range []string{"move", "merge", "mutation", "fetch", "replication_queue"} {
		operations = append(operations, clusterstate.Operation{
			OperationID: kind + "|node-a",
			Kind:        kind,
			NodeID:      "node-a",
			State:       "running",
		})
	}
	h := &apiHandler{
		log: slog.New(slog.DiscardHandler),
		state: coverageState{operations: clusterstate.Result[clusterstate.Operation]{
			NodesExpected:  1,
			NodesResponded: 1,
			Items:          operations,
		}},
	}

	res, err := h.ListOperations(t.Context(), rest.ListOperationsParams{})
	require.NoError(t, err)
	response, ok := res.(*rest.OperationsResponse)
	require.True(t, ok)
	require.Equal(t, rest.OperationKindCounts{Move: 1, Merge: 1, Mutation: 1, Fetch: 1, ReplicationQueue: 1}, response.Counts.ByKind)
}

func TestHandlerRetryTieringBranches(t *testing.T) {
	t.Parallel()

	unavailable := &apiHandler{log: slog.New(slog.DiscardHandler)}
	res, err := unavailable.RetryTieringPartition(t.Context(), &rest.TieringApplyRequest{StateToken: "tok"}, rest.RetryTieringPartitionParams{})
	require.NoError(t, err)
	require.IsType(t, &rest.RetryTieringPartitionServiceUnavailable{}, res)

	controller := &coverageTieringController{err: tiering.ErrPartitionNotFound}
	h := &apiHandler{log: slog.New(slog.DiscardHandler), tiering: controller}
	res, err = h.RetryTieringPartition(t.Context(), &rest.TieringApplyRequest{StateToken: "tok"}, rest.RetryTieringPartitionParams{})
	require.NoError(t, err)
	require.IsType(t, &rest.RetryTieringPartitionNotFound{}, res)

	controller.err = errors.New("stale token")
	res, err = h.RetryTieringPartition(t.Context(), &rest.TieringApplyRequest{StateToken: "tok"}, rest.RetryTieringPartitionParams{})
	require.NoError(t, err)
	require.IsType(t, &rest.RetryTieringPartitionConflict{}, res)

	controller.err = nil
	controller.entry = tiering.HistoryEntry{Outcome: "started", Action: tiering.DecisionTier}
	res, err = h.RetryTieringPartition(t.Context(), &rest.TieringApplyRequest{StateToken: "tok"}, rest.RetryTieringPartitionParams{})
	require.NoError(t, err)
	require.IsType(t, &rest.TieringApplyResponse{}, res)
}

type coverageTieringController struct {
	err   error
	entry tiering.HistoryEntry
}

func (c *coverageTieringController) Apply(context.Context, string, string, string, string, string) (tiering.HistoryEntry, error) {
	return c.entry, c.err
}

func (c *coverageTieringController) Retry(context.Context, string, string, string, string, string) (tiering.HistoryEntry, error) {
	return c.entry, c.err
}

func (c *coverageTieringController) Pause(tiering.PauseReason) tiering.StatusSnapshot {
	return tiering.StatusSnapshot{}
}

func (c *coverageTieringController) Resume() tiering.StatusSnapshot { return tiering.StatusSnapshot{} }

func (c *coverageTieringController) InFlight() []tiering.InFlightLeg { return nil }

func TestRoutesSurfacesRESTServerError(t *testing.T) {
	t.Parallel()

	s := &server{
		log:   slog.New(slog.DiscardHandler),
		cfg:   Config{ListenAddress: "127.0.0.1:0"},
		webFS: fstest.MapFS{},
		newRESTServer: func(rest.Handler, ...rest.ServerOption) (*rest.Server, error) {
			return nil, errors.New("boom")
		},
	}
	_, err := s.routes()
	require.ErrorContains(t, err, "create api server: boom")
	// Start must close the listener and surface the same error.
	require.ErrorContains(t, s.Start(t.Context()), "create api server: boom")
}

func TestWriteProblemJSONEncodeFailure(t *testing.T) {
	t.Parallel()

	capture := &captureLogHandler{}
	s := &server{log: slog.New(capture)}
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/test", nil)

	s.writeProblemJSON(failingResponseWriter{}, req, 404, "missing")
	require.NotEmpty(t, capture.records)
	for _, record := range capture.records {
		require.Less(t, record.Level, slog.LevelError, record.Message)
	}
}

type failingResponseWriter struct{}

func (failingResponseWriter) Header() http.Header        { return http.Header{} }
func (failingResponseWriter) Write([]byte) (int, error)  { return 0, errors.New("write failed") }
func (failingResponseWriter) WriteHeader(statusCode int) {}

func TestMappingBranches(t *testing.T) {
	t.Parallel()

	// optU64Ptr covers both arms.
	value := uint64(7)
	require.True(t, optU64Ptr(&value).Set)
	require.False(t, optU64Ptr(nil).Set)

	// Collection warnings serialize with their node attribution.
	meta := apiCollectionMeta(clusterstate.Result[clusterstate.NodeStatus]{
		Warnings: []clusterstate.Warning{{Kind: "query_error", Code: "failed", Message: "bad", NodeID: "node-a"}},
	})
	require.Len(t, meta.Warnings, 1)
	require.Equal(t, rest.NewOptString("node-a"), meta.Warnings[0].NodeId)

	// Unparseable node addresses degrade to a bare scheme instead of leaking.
	endpoint := apiNodeEndpoint("bad addr with spaces")
	require.Equal(t, "clickhouse:", endpoint.String())

	// Nil slices serialize as empty arrays, not null.
	mutation := apiMutation(clusterstate.Mutation{Node: chclient.Node{ID: "n"}})
	require.NotNil(t, mutation.PartsToDoNames)
	queueItem := apiReplicationQueueItem(clusterstate.ReplicationQueueItem{Node: chclient.Node{ID: "n"}})
	require.NotNil(t, queueItem.PartsToMerge)
	event := apiPartEvent(clusterstate.PartEvent{Node: chclient.Node{ID: "n"}})
	require.NotNil(t, event.MergedFrom)

	// Unmarshalable evidence values are dropped rather than failing the response.
	evidence := apiEvidence(map[string]any{"ok": "yes", "bad": func() {}})
	require.True(t, evidence.Set)
	require.Len(t, evidence.Value, 1)

	// keepLast appears only when the policy uses a frontier window.
	policy := apiTieringPolicy(tiering.PolicySnapshot{KeepLast: 250})
	require.True(t, policy.KeepLast.Set)
}
