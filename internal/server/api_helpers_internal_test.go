package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

func TestInternalOperationOmitsUnknownNumbers(t *testing.T) {
	t.Parallel()

	// elapsedSeconds and progress are non-nullable numbers in the API schema:
	// a move carries no progress and a mutation/replication-queue operation
	// carries neither, so nil must serialize as an absent key — a null breaks
	// the generated client's response validation.
	body, err := json.Marshal(apiOperation(clusterstate.Operation{
		OperationID: "move|node-a|part-a",
		Kind:        "move",
		State:       "running",
	}))
	require.NoError(t, err)
	require.NotContains(t, string(body), "elapsedSeconds")
	require.NotContains(t, string(body), "progress")

	elapsed := 1.5
	progress := 0.25
	body, err = json.Marshal(apiOperation(clusterstate.Operation{
		OperationID:    "merge|node-a|part-a",
		Kind:           "merge",
		State:          "running",
		ElapsedSeconds: &elapsed,
		Progress:       &progress,
	}))
	require.NoError(t, err)
	require.Contains(t, string(body), `"elapsedSeconds":1.5`)
	require.Contains(t, string(body), `"progress":0.25`)
}

func TestInternalResponseEncodingErrors(t *testing.T) {
	t.Parallel()

	s := &server{log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/test", nil)

	s.writeJSON(httptest.NewRecorder(), req, map[string]any{"bad": func() {}})
	s.handleHealth(errorResponseWriter{}, req)
	s.writeProblem(errorResponseWriter{}, req, problemDetails{
		Type:   "about:blank",
		Title:  "Bad",
		Status: http.StatusBadRequest,
		Detail: "bad",
	})
}

func TestInternalAPINodeEndpointRedactsUserinfo(t *testing.T) {
	t.Parallel()

	require.Equal(t, "clickhouse://localhost:9000", apiNodeEndpoint("localhost:9000"))
	require.Equal(t, "clickhouse://localhost:9000", apiNodeEndpoint("default:secret@localhost:9000"))
	require.Equal(t, "clickhouse://[::1]:9000", apiNodeEndpoint("default:secret@[::1]:9000"))
}

func TestInternalServerLifecycleHooks(t *testing.T) {
	t.Parallel()

	serveCalled := make(chan struct{})
	s := &server{
		log:   slog.New(slog.DiscardHandler),
		cfg:   Config{ListenAddress: "127.0.0.1:0"},
		webFS: fstest.MapFS{},
		serve: func(_ *http.Server, listener net.Listener) error {
			close(serveCalled)
			_ = listener.Close()

			return errors.New("serve failed")
		},
	}
	require.NoError(t, s.Start(t.Context()))
	<-serveCalled

	s = &server{
		http: &http.Server{ReadHeaderTimeout: time.Second},
		shutdown: func(context.Context) error {
			return errors.New("shutdown failed")
		},
	}
	require.ErrorContains(t, s.Stop(t.Context()), "shutdown http server: shutdown failed")
}

func TestInternalFilterHelpers(t *testing.T) {
	t.Parallel()

	partition := partitionAggregate{
		partitionID: "pid",
		placement:   "unknown",
		disks:       map[string]struct{}{"default": {}},
		operations:  []string{"moving"},
		placements: map[string]*partitionPlacementAggregate{
			"node-a\x00default": {
				node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
				disk: "default",
			},
		},
	}
	require.True(t, partitionMatches(map[string][]string{}, partition))
	require.True(t, partitionMatches(map[string][]string{
		"partitionId": {"pid"},
		"placement":   {"unknown"},
		"disk":        {"default"},
		"operation":   {"moving"},
		"nodeId":      {"node-a"},
		"shard":       {"shard1"},
		"replica":     {"replica1"},
	}, partition))
	require.False(t, partitionMatches(map[string][]string{"partitionId": {"other"}}, partition))
	require.False(t, partitionMatches(map[string][]string{"placement": {"split"}}, partition))
	require.False(t, partitionMatches(map[string][]string{"disk": {"s3"}}, partition))
	require.False(t, partitionMatches(map[string][]string{"operation": {"merging"}}, partition))
	require.False(t, partitionMatches(map[string][]string{"nodeId": {"node-b"}}, partition))
	require.False(t, partitionMatches(map[string][]string{"shard": {"shard2"}}, partition))
	require.False(t, partitionMatches(map[string][]string{"replica": {"replica2"}}, partition))

	nodeID := "node-a"
	database := "db"
	table := "tbl"
	partitionID := "pid"
	condition := clusterstate.Condition{
		Severity:    "warning",
		Code:        "split_partition",
		NodeID:      &nodeID,
		Database:    &database,
		Table:       &table,
		PartitionID: &partitionID,
	}
	require.True(t, conditionMatches(map[string][]string{
		"severity":    {"warning"},
		"code":        {"split_partition"},
		"nodeId":      {"node-a"},
		"database":    {"db"},
		"table":       {"tbl"},
		"partitionId": {"pid"},
	}, condition))
	require.False(t, conditionMatches(map[string][]string{"severity": {"critical"}}, condition))
	require.False(t, conditionMatches(map[string][]string{"code": {"other"}}, condition))
	require.False(t, conditionMatches(map[string][]string{"nodeId": {"node-b"}}, condition))
	require.False(t, conditionMatches(map[string][]string{"database": {"other"}}, condition))
	require.False(t, conditionMatches(map[string][]string{"table": {"other"}}, condition))
	require.False(t, conditionMatches(map[string][]string{"partitionId": {"other"}}, condition))

	node := chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}
	require.False(t, nodeMatches(map[string][]string{"shard": {"shard2"}}, node))
	require.False(t, nodeMatches(map[string][]string{"replica": {"replica2"}}, node))
}

func TestInternalSmallHelpers(t *testing.T) {
	t.Parallel()

	value := "x"
	require.Empty(t, deref(nil))
	require.Equal(t, "x", deref(&value))

	number := int64(-7)
	require.Nil(t, nullableInt64String(nil))
	require.Equal(t, "-7", *nullableInt64String(&number))

	left := "a"
	right := "b"
	require.False(t, stringLess(nil, &right))
	require.True(t, stringLess(&left, nil))
	require.True(t, stringLess(&left, &right))
	require.False(t, stringLess(&right, &left))
	require.False(t, stringGreater(nil, &right))
	require.True(t, stringGreater(&left, nil))
	require.True(t, stringGreater(&right, &left))
	require.False(t, stringGreater(&left, &right))

	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	require.False(t, timeGreater(nil, &early))
	require.True(t, timeGreater(&early, nil))
	require.True(t, timeGreater(&late, &early))
	require.False(t, timeGreater(&early, &late))

	warnings := apiWarnings([]clusterstate.Warning{{Kind: "query", Code: "failed", Message: "bad", NodeID: "node-a"}})
	require.Equal(t, []warningResponse{{Kind: "query", Code: "failed", Message: "bad", NodeID: "node-a"}}, warnings)
}

func TestInternalTieringEntryErrorFallbacks(t *testing.T) {
	t.Parallel()

	err := tieringEntryError(tiering.HistoryEntry{Outcome: "skipped"})
	require.ErrorIs(t, err, tiering.ErrActionFailed)
	require.EqualError(t, err, `tiering action failed: outcome "skipped"`)

	err = tieringEntryError(tiering.HistoryEntry{})
	require.ErrorIs(t, err, tiering.ErrActionFailed)
	require.EqualError(t, err, "tiering action failed")
}

func TestInternalAggregateHelperBranches(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	tables := aggregateTables([]clusterstate.TableState{
		{
			Node:                 chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			Database:             "db",
			Table:                "tbl",
			Engine:               "MergeTree",
			LastModificationTime: &early,
		},
		{
			Node:                 chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"},
			Database:             "db",
			Table:                "tbl",
			Engine:               "MergeTree",
			LastModificationTime: &late,
		},
	}, nil)
	require.Len(t, tables, 1)
	require.Equal(t, late, *tables[0].lastModificationTime)

	partitions := aggregatePartitions([]clusterstate.Part{
		{Active: false, Database: "db", Table: "tbl", PartitionID: "old", Node: chclient.Node{ID: "node-a"}},
		{Active: true, Database: "db", Table: "tbl", Partition: "202601", PartitionID: "new", Disk: "default", Node: chclient.Node{ID: "node-a"}},
	}, nil)
	require.Len(t, partitions, 1)
	require.Equal(t, "new", partitions[0].partitionID)
}

func TestInternalWriteJSONDowngradesClientDisconnects(t *testing.T) {
	t.Parallel()

	capture := &captureLogHandler{}
	s := &server{log: slog.New(capture)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/test", nil)

	// Broken pipe: the client closed the socket mid-response.
	s.writeJSON(pipeErrorResponseWriter{}, req, map[string]any{"ok": true})

	// Cancelled request context: the client vanished before the write.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	s.writeJSON(errorResponseWriter{}, req.WithContext(cancelled), map[string]any{"ok": true})

	require.Len(t, capture.records, 2)
	for _, record := range capture.records {
		require.Less(t, record.Level, slog.LevelError, record.Message)
	}

	// A genuine encode failure on a live connection stays an error.
	s.writeJSON(errorResponseWriter{}, req, map[string]any{"ok": true})
	require.Equal(t, slog.LevelError, capture.records[len(capture.records)-1].Level)
}

type captureLogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

func (h *captureLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureLogHandler) WithGroup(string) slog.Handler { return h }

type pipeErrorResponseWriter struct{ errorResponseWriter }

func (pipeErrorResponseWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write tcp [::1]:8080->[::1]:57588: %w", syscall.EPIPE)
}

type errorResponseWriter struct{}

func (errorResponseWriter) Header() http.Header {
	return http.Header{}
}

func (errorResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func (errorResponseWriter) WriteHeader(int) {}
