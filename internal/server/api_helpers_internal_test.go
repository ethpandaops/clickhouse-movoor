package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/api/rest"
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
	op := apiOperation(clusterstate.Operation{
		OperationID: "move|node-a|part-a",
		Kind:        "move",
		State:       "running",
	})
	body, err := json.Marshal(&op)
	require.NoError(t, err)
	require.NotContains(t, string(body), "elapsedSeconds")
	require.NotContains(t, string(body), "progress")

	elapsed := 1.5
	progress := 0.25
	op = apiOperation(clusterstate.Operation{
		OperationID:    "merge|node-a|part-a",
		Kind:           "merge",
		State:          "running",
		ElapsedSeconds: &elapsed,
		Progress:       &progress,
	})
	body, err = json.Marshal(&op)
	require.NoError(t, err)
	require.Contains(t, string(body), `"elapsedSeconds":1.5`)
	require.Contains(t, string(body), `"progress":0.25`)
}

func TestInternalAPINodeEndpointRedactsUserinfo(t *testing.T) {
	t.Parallel()

	endpoint := apiNodeEndpoint("localhost:9000")
	require.Equal(t, "clickhouse://localhost:9000", endpoint.String())
	endpoint = apiNodeEndpoint("default:secret@localhost:9000")
	require.Equal(t, "clickhouse://localhost:9000", endpoint.String())
	endpoint = apiNodeEndpoint("default:secret@[::1]:9000")
	require.Equal(t, "clickhouse://[::1]:9000", endpoint.String())
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

func TestInternalPartitionMatches(t *testing.T) {
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
	params := func(mutate func(*rest.ListTablePartitionsParams)) rest.ListTablePartitionsParams {
		out := rest.ListTablePartitionsParams{Database: "db", Table: "tbl"}
		if mutate != nil {
			mutate(&out)
		}

		return out
	}

	require.True(t, partitionMatches(params(nil), partition))
	require.True(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) {
		p.PartitionId = rest.NewOptString("pid")
		p.Placement = rest.NewOptPlacement(rest.PlacementUnknown)
		p.Disk = rest.NewOptString("default")
		p.Operation = rest.NewOptString("moving")
		p.NodeId = rest.NewOptString("node-a")
		p.Shard = rest.NewOptString("shard1")
		p.Replica = rest.NewOptString("replica1")
	}), partition))
	require.False(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) { p.PartitionId = rest.NewOptString("other") }), partition))
	require.False(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) { p.Placement = rest.NewOptPlacement(rest.PlacementSplit) }), partition))
	require.False(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) { p.Disk = rest.NewOptString("s3") }), partition))
	require.False(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) { p.Operation = rest.NewOptString("merging") }), partition))
	require.False(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) { p.NodeId = rest.NewOptString("node-b") }), partition))
	require.False(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) { p.Shard = rest.NewOptString("shard2") }), partition))
	require.False(t, partitionMatches(params(func(p *rest.ListTablePartitionsParams) { p.Replica = rest.NewOptString("replica2") }), partition))

	node := chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}
	require.False(t, matchNode(rest.OptString{}, rest.NewOptString("shard2"), rest.OptString{}, node))
	require.False(t, matchNode(rest.OptString{}, rest.OptString{}, rest.NewOptString("replica2"), node))
	require.True(t, matchNode(rest.NewOptString("node-a"), rest.OptString{}, rest.OptString{}, node))
}

func TestInternalSmallHelpers(t *testing.T) {
	t.Parallel()

	value := "x"
	require.Empty(t, deref(nil))
	require.Equal(t, "x", deref(&value))

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

	number := int64(-7)
	require.Equal(t, rest.NewOptInt64String("-7"), optI64Ptr(&number))
	require.False(t, optI64Ptr(nil).Set)
}

func TestInternalTieringActionProblem(t *testing.T) {
	t.Parallel()

	require.Nil(t, tieringActionProblem(tiering.HistoryEntry{Outcome: "started"}, nil))
	require.Nil(t, tieringActionProblem(tiering.HistoryEntry{Outcome: "success"}, nil))

	out := tieringActionProblem(tiering.HistoryEntry{Outcome: "skipped"}, nil)
	require.NotNil(t, out)
	require.Equal(t, int32(http.StatusConflict), out.Status)

	out = tieringActionProblem(tiering.HistoryEntry{}, tiering.ErrPartitionNotFound)
	require.NotNil(t, out)
	require.Equal(t, int32(http.StatusNotFound), out.Status)

	out = tieringActionProblem(tiering.HistoryEntry{}, errors.New("stale token"))
	require.NotNil(t, out)
	require.Equal(t, int32(http.StatusConflict), out.Status)
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

func TestInternalAPIErrorHandlerWritesProblem(t *testing.T) {
	t.Parallel()

	capture := &captureLogHandler{}
	s := &server{log: slog.New(capture)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/test?broken=zzz", nil)

	recorder := httptest.NewRecorder()
	s.handleAPIError(t.Context(), recorder, req, errors.New("decode failed"))
	require.Equal(t, "application/problem+json", recorder.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "decode failed", body["detail"])

	// Client write failures (closed tabs, page refreshes) must never log at
	// error level — the encode layer is owned by ogen and failures there are
	// the client's business.
	for _, record := range capture.records {
		require.Less(t, record.Level, slog.LevelError, record.Message)
	}
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
