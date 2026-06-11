package movoor

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/opsserver"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

func TestNewWithoutFrontendOrClickHouse(t *testing.T) {
	t.Parallel()

	enabled := false
	cfg := DefaultConfig()
	cfg.Frontend.Enabled = &enabled

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)
	require.NotNil(t, app)
	require.Nil(t, app.server)
	require.Nil(t, app.ch)
	require.Nil(t, app.state)
}

func TestNewWithFrontend(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)
	require.NotNil(t, app.server)
	require.Nil(t, app.ch)
	require.Nil(t, app.state)
}

func TestNewWithClickHouse(t *testing.T) {
	t.Parallel()

	enabled := false
	cfg := DefaultConfig()
	cfg.Frontend.Enabled = &enabled
	cfg.ClickHouse.Nodes = []ClickHouseNodeConfig{{
		Name:    "node-a",
		Shard:   "shard1",
		Replica: "replica1",
		DSN:     "clickhouse://default@localhost:9000/default",
	}}
	cfg.Watches = []WatchConfig{{Database: "db", Table: "tbl"}}

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.ch.Close()) })
	require.NotNil(t, app.ch)
	require.NotNil(t, app.state)
	require.Nil(t, app.server)
}

func TestNewWithFrontendAndTieringStore(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ClickHouse.Nodes = []ClickHouseNodeConfig{{
		Name:    "node-a",
		Shard:   "shard1",
		Replica: "replica1",
		DSN:     "clickhouse://default@localhost:9000/default",
	}}
	cfg.Watches = []WatchConfig{{Database: "db", Table: "tbl"}}

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.ch.Close()) })
	require.NotNil(t, app.server)
	require.NotNil(t, app.tiering)
}

func TestNewWithTieringOffStillExposesStore(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Tiering.Mode = tiering.ModeOff
	cfg.ClickHouse.Nodes = []ClickHouseNodeConfig{{
		Name:    "node-a",
		Shard:   "shard1",
		Replica: "replica1",
		DSN:     "clickhouse://default@localhost:9000/default",
	}}
	cfg.Watches = []WatchConfig{{Database: "db", Table: "tbl"}}

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.ch.Close()) })
	require.NotNil(t, app.server)
	require.NotNil(t, app.tiering)
	require.Equal(t, tiering.ModeOff, app.tiering.Store().Status().Mode)
}

func TestNewReturnsOpsServerError(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Tracing.Endpoint = "\n"
	_, err := New(slog.New(slog.DiscardHandler), cfg)
	require.ErrorContains(t, err, "create ops server")
}

func TestNewReturnsWebFSError(t *testing.T) {
	oldGetWebFS := getWebFS
	t.Cleanup(func() { getWebFS = oldGetWebFS })
	getWebFS = func() (fs.FS, error) {
		return nil, errors.New("embed failed")
	}

	cfg := DefaultConfig()
	cfg.ClickHouse.Nodes = []ClickHouseNodeConfig{{
		Name:    "node-a",
		Shard:   "shard1",
		Replica: "replica1",
		DSN:     "clickhouse://default@localhost:9000/default",
	}}
	cfg.Watches = []WatchConfig{{Database: "db", Table: "tbl"}}
	_, err := New(slog.New(slog.DiscardHandler), cfg)

	require.ErrorContains(t, err, "load embedded web assets: embed failed")
}

func TestRunReturnsWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	enabled := false
	cfg := DefaultConfig()
	cfg.Frontend.Enabled = &enabled

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, app.Run(ctx))
}

func TestRunReturnsClickHouseCloseError(t *testing.T) {
	t.Parallel()

	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: DefaultConfig(),
		ch:  errorCloser{err: errors.New("close failed")},
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorContains(t, app.Run(ctx), "close clickhouse clients: close failed")
}

func TestRunStartsAndStopsServer(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	started := make(chan struct{})
	fake := &fakeServer{started: started}
	app := &App{
		log:    slog.New(slog.DiscardHandler),
		cfg:    cfg,
		server: fake,
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	<-started
	cancel()

	require.NoError(t, <-done)
	require.True(t, fake.stopped)
}

func TestRunStartsAndStopsOpsServer(t *testing.T) {
	t.Parallel()

	enabled := false
	cfg := DefaultConfig()
	cfg.Frontend.Enabled = &enabled
	cfg.MetricsAddr = "127.0.0.1:0"
	cfg.HealthCheckAddr = "127.0.0.1:0"

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	require.NoError(t, <-done)
}

func TestRunReturnsOpsStartError(t *testing.T) {
	t.Parallel()

	enabled := false
	cfg := DefaultConfig()
	cfg.Frontend.Enabled = &enabled
	cfg.MetricsAddr = "127.0.0.1:-1"
	cfg.HealthCheckAddr = ""

	app, err := New(slog.New(slog.DiscardHandler), cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	require.ErrorContains(t, app.Run(ctx), "start ops server")
}

func TestRunReturnsServerStartError(t *testing.T) {
	t.Parallel()

	app := &App{
		log:    slog.New(slog.DiscardHandler),
		cfg:    DefaultConfig(),
		server: &fakeServer{startErr: errors.New("bind failed")},
	}

	require.ErrorContains(t, app.Run(t.Context()), "start server: bind failed")
}

func TestRunReturnsTieringStartError(t *testing.T) {
	t.Parallel()

	app := &App{
		log:     slog.New(slog.DiscardHandler),
		cfg:     DefaultConfig(),
		tiering: &fakeTieringController{startErr: errors.New("tiering start failed")},
	}

	require.ErrorContains(t, app.Run(t.Context()), "start tiering controller: tiering start failed")
}

func TestRunReturnsWatchValidationError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	mock.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("Distributed"))

	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: DefaultConfig(),
		state: clusterstate.New(poolWithClients(
			chclient.Client{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, DB: db},
		), 0, []clusterstate.Watch{{Database: "db", Table: "tbl"}}),
	}

	require.ErrorContains(t, app.Run(t.Context()), "validate watches")
}

func TestRunStartsWithDegradedClickHouseValidation(t *testing.T) {
	t.Parallel()

	dbA, mockA, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockA.ExpectationsWereMet())
		_ = dbA.Close()
	})
	dbB, mockB, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockB.ExpectationsWereMet())
		_ = dbB.Close()
	})
	mockA.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("MergeTree"))
	mockB.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnError(errors.New("connection refused"))

	started := make(chan struct{})
	ops := &fakeOpsServer{started: started}
	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: DefaultConfig(),
		ops: ops,
		state: clusterstate.New(poolWithClients(
			chclient.Client{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, DB: dbA},
			chclient.Client{Node: chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}, DB: dbB},
		), 0, []clusterstate.Watch{{Database: "db", Table: "tbl"}}),
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	<-started
	cancel()

	require.NoError(t, <-done)
	require.True(t, ops.clickhouseSet)
	require.Equal(t, opsserver.ClickHouseReadinessDegraded, ops.clickhouse.Status)
	require.Equal(t, 2, ops.clickhouse.NodesExpected)
	require.Equal(t, 1, ops.clickhouse.NodesResponded)
	require.Equal(t, 1, ops.clickhouse.NodesFailed)
	require.Len(t, ops.clickhouse.Warnings, 1)
	require.Equal(t, "node_unreachable", ops.clickhouse.Warnings[0].Code)
}

func TestRunStartsUnavailableWhenNoClickHouseNodeResponds(t *testing.T) {
	t.Parallel()

	dbA, mockA, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockA.ExpectationsWereMet())
		_ = dbA.Close()
	})
	dbB, mockB, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockB.ExpectationsWereMet())
		_ = dbB.Close()
	})
	mockA.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnError(errors.New("connection refused"))
	mockB.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnError(errors.New("connection refused"))

	started := make(chan struct{})
	ops := &fakeOpsServer{started: started}
	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: DefaultConfig(),
		ops: ops,
		state: clusterstate.New(poolWithClients(
			chclient.Client{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, DB: dbA},
			chclient.Client{Node: chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}, DB: dbB},
		), 0, []clusterstate.Watch{{Database: "db", Table: "tbl"}}),
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	<-started
	cancel()

	require.NoError(t, <-done)
	require.True(t, ops.clickhouseSet)
	require.Equal(t, opsserver.ClickHouseReadinessUnavailable, ops.clickhouse.Status)
	require.Equal(t, 2, ops.clickhouse.NodesExpected)
	require.Equal(t, 0, ops.clickhouse.NodesResponded)
	require.Equal(t, 2, ops.clickhouse.NodesFailed)
	require.NotEmpty(t, ops.clickhouse.LastError)
}

func TestRunFailsWhenNoClickHouseNodeValidatesForQueryErrors(t *testing.T) {
	t.Parallel()

	dbA, mockA, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockA.ExpectationsWereMet())
		_ = dbA.Close()
	})
	dbB, mockB, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockB.ExpectationsWereMet())
		_ = dbB.Close()
	})
	mockA.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnError(errors.New("validation query failed"))
	mockB.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnError(errors.New("validation query failed"))

	started := make(chan struct{})
	ops := &fakeOpsServer{started: started}
	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: DefaultConfig(),
		ops: ops,
		state: clusterstate.New(poolWithClients(
			chclient.Client{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, DB: dbA},
			chclient.Client{Node: chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}, DB: dbB},
		), 0, []clusterstate.Watch{{Database: "db", Table: "tbl"}}),
	}

	require.ErrorContains(t, app.Run(t.Context()), "no ClickHouse node responded")
	require.True(t, ops.clickhouseSet)
	require.Equal(t, opsserver.ClickHouseReadinessUnavailable, ops.clickhouse.Status)
	select {
	case <-started:
		t.Fatal("ops server started despite validation query errors")
	default:
	}
}

func TestRunFailsOnSchemaErrorFromRespondingNodeEvenWhenAnotherNodeIsDown(t *testing.T) {
	t.Parallel()

	dbA, mockA, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockA.ExpectationsWereMet())
		_ = dbA.Close()
	})
	dbB, mockB, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockB.ExpectationsWereMet())
		_ = dbB.Close()
	})
	mockA.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("Distributed"))
	mockB.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnError(errors.New("connection refused"))

	ops := &fakeOpsServer{}
	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: DefaultConfig(),
		ops: ops,
		state: clusterstate.New(poolWithClients(
			chclient.Client{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, DB: dbA},
			chclient.Client{Node: chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}, DB: dbB},
		), 0, []clusterstate.Watch{{Database: "db", Table: "tbl"}}),
	}

	require.ErrorContains(t, app.Run(t.Context()), "engine \"Distributed\"")
	require.True(t, ops.clickhouseSet)
	require.Equal(t, opsserver.ClickHouseReadinessUnavailable, ops.clickhouse.Status)
	require.Equal(t, 2, ops.clickhouse.NodesExpected)
	require.Equal(t, 1, ops.clickhouse.NodesResponded)
	require.Equal(t, 1, ops.clickhouse.NodesFailed)
	require.Contains(t, ops.clickhouse.LastError, "Distributed")
}

func TestRunReturnsServerStopError(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	app := &App{
		log:    slog.New(slog.DiscardHandler),
		cfg:    DefaultConfig(),
		server: &fakeServer{started: started, stopErr: errors.New("shutdown failed")},
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	<-started
	cancel()

	require.ErrorContains(t, <-done, "stop server: shutdown failed")
}

func TestRunReturnsTieringStopError(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	app := &App{
		log:     slog.New(slog.DiscardHandler),
		cfg:     DefaultConfig(),
		tiering: &fakeTieringController{started: started, stopErr: errors.New("tiering stop failed")},
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	<-started
	cancel()

	require.ErrorContains(t, <-done, "stop tiering controller: tiering stop failed")
}

func TestRunReturnsOpsStopError(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: DefaultConfig(),
		ops: &fakeOpsServer{started: started, stopErr: errors.New("ops stop failed")},
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	<-started
	cancel()

	require.ErrorContains(t, <-done, "stop ops server: ops stop failed")
}

func TestRunClosesClickHousePool(t *testing.T) {
	t.Parallel()

	pool, err := chclient.NewPool(chclient.Config{Nodes: []chclient.NodeConfig{{
		Name:    "node-a",
		Shard:   "shard1",
		Replica: "replica1",
		DSN:     "clickhouse://default@localhost:9000/default",
	}}})
	require.NoError(t, err)

	cfg := DefaultConfig()
	app := &App{
		log: slog.New(slog.DiscardHandler),
		cfg: cfg,
		ch:  pool,
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, app.Run(ctx))
}

func TestNewRejectsInvalidClickHousePool(t *testing.T) {
	t.Parallel()

	enabled := false
	cfg := DefaultConfig()
	cfg.Frontend.Enabled = &enabled
	cfg.ClickHouse.Nodes = []ClickHouseNodeConfig{{
		Name:    "node-a",
		Shard:   "shard1",
		Replica: "replica1",
		DSN:     "clickhouse://default@localhost:9000,localhost:9001/default",
	}}

	_, err := New(slog.New(slog.DiscardHandler), cfg)
	require.ErrorContains(t, err, "create clickhouse clients")
}

func TestValidateWatchesLogsWarnings(t *testing.T) {
	t.Parallel()

	dbA, mockA, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockA.ExpectationsWereMet())
		_ = dbA.Close()
	})
	dbB, mockB, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mockB.ExpectationsWereMet())
		_ = dbB.Close()
	})
	mockA.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("MergeTree"))
	mockB.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnError(errors.New("query failed"))

	app := &App{
		log: slog.New(slog.DiscardHandler),
		state: clusterstate.New(poolWithClients(
			chclient.Client{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, DB: dbA},
			chclient.Client{Node: chclient.Node{ID: "node-b", Shard: "shard1", Replica: "replica2"}, DB: dbB},
		), 0, []clusterstate.Watch{{Database: "db", Table: "tbl"}}),
	}

	require.NoError(t, app.validateWatches(t.Context()))
}

func TestValidateWatchesReturnsError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	mock.ExpectQuery("(?s).*FROM system\\.tables.*").
		WithArgs("db", "tbl").
		WillReturnRows(sqlmock.NewRows([]string{"engine"}).AddRow("Distributed"))

	app := &App{
		log: slog.New(slog.DiscardHandler),
		state: clusterstate.New(poolWithClients(
			chclient.Client{Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"}, DB: db},
		), 0, []clusterstate.Watch{{Database: "db", Table: "tbl"}}),
	}

	require.ErrorContains(t, app.validateWatches(t.Context()), "validate watches")
}

func TestAppConfigAdapters(t *testing.T) {
	t.Parallel()

	cfg := ClickHouseConfig{
		DialTimeout:  2 * time.Second,
		QueryTimeout: 3 * time.Second,
		Nodes: []ClickHouseNodeConfig{{
			Name:    "node-a",
			Shard:   "shard1",
			Replica: "replica1",
			DSN:     "clickhouse://default@localhost:9000/default",
		}},
	}

	clientCfg := clickHouseClientConfig(cfg)
	require.Equal(t, 2*time.Second, clientCfg.DialTimeout)
	require.Equal(t, 3*time.Second, clientCfg.QueryTimeout)
	require.Len(t, clientCfg.Nodes, 1)
	require.Equal(t, "node-a", clientCfg.Nodes[0].Name)

	watches := clusterStateWatches([]WatchConfig{{Database: "db", Table: "tbl"}})
	require.Equal(t, []clusterstate.Watch{{Database: "db", Table: "tbl"}}, watches)
}

func TestClickHouseCollectionStatus(t *testing.T) {
	t.Parallel()

	collectedAt := time.Now().UTC()
	okStatus := clickHouseCollectionStatus(clusterstate.Result[clusterstate.NodeStatus]{
		CollectedAt:        collectedAt,
		CollectionDuration: time.Second,
		NodesExpected:      2,
		NodesResponded:     2,
	})
	require.Equal(t, opsserver.ClickHouseReadinessOK, okStatus.Status)
	require.Equal(t, collectedAt, okStatus.UpdatedAt)
	require.Equal(t, 1000, okStatus.CheckDurationMs)

	degraded := clickHouseCollectionStatus(clusterstate.Result[clusterstate.NodeStatus]{
		NodesExpected:  2,
		NodesResponded: 1,
		NodesFailed:    1,
		Warnings:       []clusterstate.Warning{{Kind: "reachability", Code: "node_unreachable", NodeID: "node-b"}},
	})
	require.Equal(t, opsserver.ClickHouseReadinessDegraded, degraded.Status)
	require.Len(t, degraded.Warnings, 1)

	unavailable := clickHouseCollectionStatus(clusterstate.Result[clusterstate.NodeStatus]{
		NodesExpected:  2,
		NodesResponded: 0,
		NodesFailed:    2,
	})
	require.Equal(t, opsserver.ClickHouseReadinessUnavailable, unavailable.Status)
	require.Equal(t, "no configured ClickHouse node responded", unavailable.LastError)
}

func TestStartClickHouseHealthMonitor(t *testing.T) {
	oldInterval := clickHouseHealthRefreshInterval
	clickHouseHealthRefreshInterval = time.Millisecond
	t.Cleanup(func() { clickHouseHealthRefreshInterval = oldInterval })

	require.Nil(t, (&App{}).startClickHouseHealthMonitor(t.Context()))

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	mock.ExpectQuery("(?s).*FROM system\\.one.*").
		WillReturnRows(sqlmock.NewRows([]string{"version()", "timezone()", "toUInt64(uptime())"}).AddRow("26.2.5.45", "UTC", uint64(1)))

	ops := &statusOpsServer{statuses: make(chan opsserver.ClickHouseStatus, 1)}
	app := &App{
		state: clusterstate.New(poolWithClients(chclient.Client{
			Node: chclient.Node{ID: "node-a", Shard: "shard1", Replica: "replica1"},
			DB:   db,
		}), 0, nil),
		ops: ops,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := app.startClickHouseHealthMonitor(ctx)
	require.NotNil(t, done)

	select {
	case status := <-ops.statuses:
		require.Equal(t, opsserver.ClickHouseReadinessOK, status.Status)
		require.Equal(t, 1, status.NodesResponded)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for health monitor update")
	}
	cancel()
	waitForClickHouseHealthMonitor(done)
}

type fakeServer struct {
	started  chan struct{}
	startErr error
	stopErr  error
	stopped  bool
}

func (s *fakeServer) Start(context.Context) error {
	if s.started != nil {
		close(s.started)
	}

	return s.startErr
}

func (s *fakeServer) Stop(context.Context) error {
	s.stopped = true

	return s.stopErr
}

func (*fakeServer) Addr() string {
	return "127.0.0.1:0"
}

type fakeOpsServer struct {
	started       chan struct{}
	startErr      error
	stopErr       error
	clickhouse    opsserver.ClickHouseStatus
	clickhouseSet bool
}

func (s *fakeOpsServer) Start(context.Context) error {
	if s.started != nil {
		close(s.started)
	}

	return s.startErr
}

func (s *fakeOpsServer) Stop(context.Context) error {
	return s.stopErr
}

func (*fakeOpsServer) TieringInstrumenter() tiering.Instrumenter {
	return nil
}

func (*fakeOpsServer) SetTieringStore(*tiering.Store) {}

func (s *fakeOpsServer) SetClickHouseStatus(status opsserver.ClickHouseStatus) {
	s.clickhouse = status
	s.clickhouseSet = true
}

type statusOpsServer struct {
	statuses chan opsserver.ClickHouseStatus
}

func (*statusOpsServer) Start(context.Context) error { return nil }

func (*statusOpsServer) Stop(context.Context) error { return nil }

func (*statusOpsServer) TieringInstrumenter() tiering.Instrumenter { return nil }

func (*statusOpsServer) SetTieringStore(*tiering.Store) {}

func (s *statusOpsServer) SetClickHouseStatus(status opsserver.ClickHouseStatus) {
	select {
	case s.statuses <- status:
	default:
	}
}

type fakeTieringController struct {
	started  chan struct{}
	startErr error
	stopErr  error
	store    *tiering.Store
}

func (c *fakeTieringController) InFlight() []tiering.InFlightLeg {
	return nil
}

func (c *fakeTieringController) Start(context.Context) error {
	if c.started != nil {
		close(c.started)
	}
	return c.startErr
}

func (c *fakeTieringController) Stop(context.Context) error {
	return c.stopErr
}

func (c *fakeTieringController) Store() *tiering.Store {
	if c.store == nil {
		c.store = tiering.NewStore(1)
	}
	return c.store
}

func (c *fakeTieringController) Apply(context.Context, string, string, string, string, string) (tiering.HistoryEntry, error) {
	return tiering.HistoryEntry{}, nil
}

func (c *fakeTieringController) Retry(context.Context, string, string, string, string, string) (tiering.HistoryEntry, error) {
	return tiering.HistoryEntry{}, nil
}

func (c *fakeTieringController) Pause(tiering.PauseReason) tiering.StatusSnapshot {
	return tiering.StatusSnapshot{}
}

func (c *fakeTieringController) Resume() tiering.StatusSnapshot {
	return tiering.StatusSnapshot{}
}

type errorCloser struct {
	err error
}

func (c errorCloser) Close() error {
	return c.err
}

func poolWithClients(clients ...chclient.Client) *chclient.Pool {
	pool := &chclient.Pool{}
	field := reflect.ValueOf(pool).Elem().FieldByName("clients")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(clients))

	return pool
}

func TestRunBindFailureStartsNothingWriteCapable(t *testing.T) {
	t.Parallel()

	// Occupy a port so the ops server's bind fails.
	listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	cfg := DefaultConfig()
	ops, err := opsserver.New(slog.New(slog.DiscardHandler), opsserver.Config{
		MetricsAddr:     listener.Addr().String(),
		HealthCheckAddr: "127.0.0.1:0",
		Version:         "test",
	})
	require.NoError(t, err)

	started := make(chan struct{})
	app := &App{
		log:     slog.New(slog.DiscardHandler),
		cfg:     cfg,
		ops:     ops,
		tiering: &fakeTieringController{started: started},
	}

	err = app.Run(t.Context())
	require.ErrorContains(t, err, "start ops server")
	// The write-capable controller starts LAST: a bind failure must occur
	// before any reconcile goroutine could have spawned.
	select {
	case <-started:
		t.Fatal("tiering controller was started despite the bind failure")
	default:
	}
}
