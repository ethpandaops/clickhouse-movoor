package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/server"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

//nolint:funlen // Single endpoint flow keeps shared tiering API fixtures explicit.
func TestTieringAPI(t *testing.T) {
	store := tiering.NewStore(10)
	store.SetStatus(tiering.StatusSnapshot{
		Mode:                    tiering.ModePlan,
		PauseState:              tiering.PauseRunning,
		MaxConcurrentPartitions: 1,
		MaxMovesPerCycle:        4,
		MaxBytesInFlight:        100,
		MaxBytesPerDay:          1000,
	})
	releaseAt := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)
	store.Publish(tiering.TablePlan{
		NodeID:       "node-a",
		Database:     "db",
		Table:        "tbl",
		ReconciledAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		TickDuration: time.Second,
		Generation:   "gen",
		Conditions: []tiering.Condition{
			tiering.NewCondition(tiering.ConditionSeverityWarning, "part_log_coverage_shortfall", "fallback", "node-a", "db", "tbl", "", ""),
		},
		Verdicts: []tiering.Verdict{
			{
				NodeID:        "node-a",
				Shard:         "s1",
				Replica:       "r1",
				Database:      "db",
				Table:         "tbl",
				Partition:     "202601",
				PartitionID:   "pid-ready",
				Status:        tiering.StatusReady,
				Decision:      tiering.DecisionTier,
				Reason:        "ready",
				Rows:          10,
				BytesOnDisk:   20,
				ActiveParts:   2,
				Disks:         []tiering.DiskPart{{Disk: "default", Parts: 2}},
				TargetDisk:    "s3_cache",
				Policy:        tiering.PolicySnapshot{Mode: tiering.ModePlan, AgeBasis: tiering.AgeBasisPartitionTime, TargetDisk: "s3_cache", OptimizeToParts: 1, ResplitStrategy: tiering.ResplitStrategyAuto},
				Hold:          &tiering.HoldDetail{Gate: "age", Window: "1h", ReleasesAt: &releaseAt, Failures: 2},
				Token:         "token-ready",
				ReconciledAt:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
				EffectiveMode: tiering.ModePlan,
			},
			{
				NodeID:        "node-a",
				Database:      "db",
				Table:         "tbl",
				PartitionID:   "pid-keep",
				Status:        tiering.StatusHot,
				Decision:      tiering.DecisionKeep,
				Token:         "token-keep",
				EffectiveMode: tiering.ModePlan,
			},
		},
	})
	store.AppendHistory(tiering.HistoryEntry{
		Time:        time.Date(2026, 6, 1, 0, 1, 0, 0, time.UTC),
		NodeID:      "node-a",
		Database:    "db",
		Table:       "tbl",
		PartitionID: "pid-ready",
		Action:      tiering.DecisionTier,
		Outcome:     "success",
		Duration:    time.Second,
		Bytes:       20,
	})
	controller := &fakeTieringController{
		store: store,
		inFlight: []tiering.InFlightLeg{{
			NodeID:      "node-a",
			Database:    "db",
			Table:       "tbl",
			Partition:   "202601",
			PartitionID: "pid-ready",
			Action:      tiering.DecisionTier,
			Bytes:       20,
			StartedAt:   time.Date(2026, 6, 1, 0, 2, 0, 0, time.UTC),
			Source:      "supervised",
		}},
	}
	srv := startTieringServer(t, controller, store)

	plan := getTieringJSON(t, srv, "/api/v1/tiering/plan?status=ready&decision=tier")
	items, ok := plan["items"].([]any)
	require.True(t, ok)
	tables, ok := plan["tables"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	item, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pid-ready", item["partitionId"])
	hold, ok := item["hold"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "age", hold["gate"])
	require.Equal(t, "1h", hold["window"])
	require.InEpsilon(t, float64(2), hold["failures"], 0)
	tablePlan, ok := tables[0].(map[string]any)
	require.True(t, ok)
	actionable, ok := tablePlan["actionable"].(float64)
	require.True(t, ok)
	require.InEpsilon(t, float64(1), actionable, 0)
	allPlan := getTieringJSON(t, srv, "/api/v1/tiering/plan")
	allItems, ok := allPlan["items"].([]any)
	require.True(t, ok)
	require.Len(t, allItems, 2)
	// Filtered-empty and unmatched plans MUST serialize as empty arrays, not
	// null: the OpenAPI contract requires arrays and the generated client's
	// validation rejects null outright.
	for _, query := range []string{
		"/api/v1/tiering/plan?nodeId=missing",
		"/api/v1/tiering/plan?database=missing",
		"/api/v1/tiering/plan?table=missing",
		"/api/v1/tiering/plan?decision=append",
	} {
		body := getTieringJSON(t, srv, query)
		require.Equal(t, []any{}, body["items"], query)
		require.NotNil(t, body["tables"], query)
	}
	for _, query := range []string{
		"/api/v1/tiering/plan?status=nope",
		"/api/v1/tiering/plan?decision=nope",
	} {
		body := getTieringJSONStatus(t, srv, query, http.StatusBadRequest)
		detail, isString := body["detail"].(string)
		require.True(t, isString, query)
		// The generated server phrases enum rejections; the offending value is
		// part of the message, the exact wording is not part of the contract.
		require.Contains(t, detail, "nope", query)
	}

	status := getTieringJSON(t, srv, "/api/v1/tiering/status")
	require.Equal(t, "plan", status["mode"])
	require.Equal(t, "running", status["pauseState"])
	inFlight, ok := status["inFlight"].([]any)
	require.True(t, ok)
	require.Len(t, inFlight, 1)
	leg, ok := inFlight[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pid-ready", leg["partitionId"])
	require.Equal(t, "supervised", leg["source"])

	history := getTieringJSON(t, srv, "/api/v1/tiering/history")
	historyItems, ok := history["items"].([]any)
	require.True(t, ok)
	historyItem, ok := historyItems[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "success", historyItem["outcome"])
	filteredHistory := getTieringJSON(t, srv, "/api/v1/tiering/history?nodeId=node-a&database=db&table=tbl&partitionId=pid-ready")
	filteredHistoryItems, ok := filteredHistory["items"].([]any)
	require.True(t, ok)
	require.Len(t, filteredHistoryItems, 1)
	emptyHistory := getTieringJSON(t, srv, "/api/v1/tiering/history?nodeId=missing&partitionId=pid-ready")
	require.Empty(t, emptyHistory["items"])

	pause := postTieringJSON(t, srv, "/api/v1/tiering/pause", nil, http.StatusAccepted)
	require.Equal(t, "stopped", pause["pauseState"])
	require.Equal(t, "operator", pause["pauseReason"])
	resume := postTieringJSON(t, srv, "/api/v1/tiering/resume", nil, http.StatusAccepted)
	require.Equal(t, "running", resume["pauseState"])

	apply := postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid-ready/apply?nodeId=node-a", map[string]string{"stateToken": "token-ready"}, http.StatusAccepted)
	applyItem, ok := apply["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pid-ready", applyItem["partitionId"])
	require.Equal(t, "apply", controller.lastAction)

	retry := postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid-ready/retry?nodeId=node-a", map[string]string{"stateToken": "token-ready"}, http.StatusAccepted)
	retryItem, ok := retry["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pid-ready", retryItem["partitionId"])
	require.Equal(t, "retry", controller.lastAction)

	// Parameter and body validation is owned by the generated server; the
	// offending field appears in the detail, the phrasing is ogen's.
	bad := postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid-ready/apply", map[string]string{"stateToken": "token-ready"}, http.StatusBadRequest)
	require.Contains(t, bad["detail"], "nodeId")
	bad = postTieringRaw(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid-ready/apply?nodeId=node-a", "{", http.StatusBadRequest)
	require.Contains(t, bad["detail"], "decode")
	bad = postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid-ready/apply?nodeId=node-a", map[string]string{}, http.StatusBadRequest)
	require.Contains(t, bad["detail"], "stateToken")

	controller.err = errors.New("stale token")
	conflict := postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid-ready/apply?nodeId=node-a", map[string]string{"stateToken": "token-ready"}, http.StatusConflict)
	require.Equal(t, "stale token", conflict["detail"])

	controller.err = tiering.ErrPartitionNotFound
	notFound := postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid-ready/apply?nodeId=node-a", map[string]string{"stateToken": "token-ready"}, http.StatusNotFound)
	require.Equal(t, tiering.ErrPartitionNotFound.Error(), notFound["detail"])
}

func TestTieringAPIUnavailable(t *testing.T) {
	srv := startTieringServer(t, nil, nil)
	requireServiceUnavailableStatus(t, getTieringJSONStatus(t, srv, "/api/v1/tiering/plan", http.StatusServiceUnavailable))
	requireServiceUnavailableStatus(t, getTieringJSONStatus(t, srv, "/api/v1/tiering/status", http.StatusServiceUnavailable))
	requireServiceUnavailableStatus(t, getTieringJSONStatus(t, srv, "/api/v1/tiering/history", http.StatusServiceUnavailable))
	requireServiceUnavailableStatus(t, postTieringJSON(t, srv, "/api/v1/tiering/pause", nil, http.StatusServiceUnavailable))
	requireServiceUnavailableStatus(t, postTieringJSON(t, srv, "/api/v1/tiering/resume", nil, http.StatusServiceUnavailable))
	requireServiceUnavailableStatus(t, postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/p/apply?nodeId=node-a", map[string]string{"stateToken": "token"}, http.StatusServiceUnavailable))
}

func TestTieringAPIRejectsErrorOutcome(t *testing.T) {
	store := tiering.NewStore(10)
	controller := &fakeTieringController{
		store: store,
		entry: tiering.HistoryEntry{
			Time:        time.Now().UTC(),
			NodeID:      "node-a",
			Database:    "db",
			Table:       "tbl",
			PartitionID: "pid",
			Action:      tiering.DecisionTier,
			Outcome:     "error",
			Error:       "move failed",
		},
	}
	srv := startTieringServer(t, controller, store)

	body := postTieringJSON(t, srv, "/api/v1/tiering/tables/db/tbl/partitions/pid/apply?nodeId=node-a", map[string]string{"stateToken": "token"}, http.StatusConflict)
	require.Equal(t, "tiering action failed: move failed", body["detail"])
}

type fakeTieringController struct {
	store      *tiering.Store
	err        error
	entry      tiering.HistoryEntry
	lastAction string
	inFlight   []tiering.InFlightLeg
}

func (f *fakeTieringController) InFlight() []tiering.InFlightLeg {
	return f.inFlight
}

func (f *fakeTieringController) Apply(_ context.Context, nodeID string, database string, table string, partitionID string, _ string) (tiering.HistoryEntry, error) {
	f.lastAction = "apply"
	if f.err != nil {
		return tiering.HistoryEntry{}, f.err
	}
	if f.entry.Outcome != "" {
		return f.entry, nil
	}
	return tiering.HistoryEntry{NodeID: nodeID, Database: database, Table: table, PartitionID: partitionID, Action: tiering.DecisionTier, Outcome: "success", Time: time.Now().UTC()}, nil
}

func (f *fakeTieringController) Retry(ctx context.Context, nodeID string, database string, table string, partitionID string, stateToken string) (tiering.HistoryEntry, error) {
	_ = ctx
	_ = stateToken
	f.lastAction = "retry"
	if f.err != nil {
		return tiering.HistoryEntry{}, f.err
	}
	if f.entry.Outcome != "" {
		return f.entry, nil
	}
	return tiering.HistoryEntry{NodeID: nodeID, Database: database, Table: table, PartitionID: partitionID, Action: tiering.DecisionTier, Outcome: "success", Time: time.Now().UTC()}, nil
}

func (f *fakeTieringController) Pause(reason tiering.PauseReason) tiering.StatusSnapshot {
	return f.store.Pause(reason)
}

func (f *fakeTieringController) Resume() tiering.StatusSnapshot {
	return f.store.Resume()
}

func startTieringServer(t *testing.T, controller server.TieringController, store *tiering.Store) server.Server {
	t.Helper()
	srv := server.NewWithTiering(
		slog.New(slog.DiscardHandler),
		server.Config{ListenAddress: "127.0.0.1:0"},
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}},
		nil,
		controller,
		store,
	)
	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, srv.Stop(ctx))
	})
	return srv
}

func getTieringJSON(t *testing.T, srv server.Server, path string) map[string]any {
	t.Helper()
	return getTieringJSONStatus(t, srv, path, http.StatusOK)
}

func getTieringJSONStatus(t *testing.T, srv server.Server, path string, status int) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+srv.Addr()+path, nil)
	require.NoError(t, err)
	return doTieringJSON(t, req, status)
}

func postTieringJSON(t *testing.T, srv server.Server, path string, body any, status int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+srv.Addr()+path, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return doTieringJSON(t, req, status)
}

func postTieringRaw(t *testing.T, srv server.Server, path string, body string, status int) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+srv.Addr()+path, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	return doTieringJSON(t, req, status)
}

func doTieringJSON(t *testing.T, req *http.Request, status int) map[string]any {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	require.Equal(t, status, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func requireServiceUnavailableStatus(t *testing.T, body map[string]any) {
	t.Helper()
	status, ok := body["status"].(float64)
	require.True(t, ok)
	require.InEpsilon(t, float64(http.StatusServiceUnavailable), status, 0)
}

func TestTieringPlanEmptyStoreSerializesArrays(t *testing.T) {
	// A fresh boot serves the plan before the first reconcile publishes
	// anything; both top-level collections must be arrays, never null.
	store := tiering.NewStore(10)
	srv := startTieringServer(t, &fakeTieringController{store: store}, store)
	body := getTieringJSON(t, srv, "/api/v1/tiering/plan")
	require.Equal(t, []any{}, body["tables"])
	require.Equal(t, []any{}, body["items"])
}
