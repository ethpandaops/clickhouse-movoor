// Package movoor is the application root for clickhouse-movoor. It wires
// configuration to ClickHouse collectors, the tiering controller, ops endpoints,
// and the HTTP server that serves the embedded operator UI.
package movoor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/opsserver"
	"github.com/ethpandaops/clickhouse-movoor/internal/server"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
	"github.com/ethpandaops/clickhouse-movoor/web"
)

const (
	// shutdownTimeout bounds how long Run waits for in-flight requests to drain.
	shutdownTimeout = 10 * time.Second
)

// clickHouseHealthRefreshInterval keeps ops readiness current without making
// the health endpoint itself block on ClickHouse.
var clickHouseHealthRefreshInterval = 10 * time.Second

// App is the top-level application. It owns long-lived dependencies and
// orchestrates their lifecycle.
type App struct {
	log     *slog.Logger
	cfg     Config
	server  server.Server
	ops     opsLifecycle
	ch      clickHouseCloser
	state   *clusterstate.Collector
	tiering tiering.Controller
}

type clickHouseCloser interface {
	Close() error
}

type opsLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
	TieringInstrumenter() tiering.Instrumenter
	SetTieringStore(*tiering.Store)
	SetClickHouseStatus(opsserver.ClickHouseStatus)
}

var getWebFS = web.GetFS

// New constructs an App from the given logger and configuration. It performs
// only lightweight wiring; heavy initialisation happens in Run.
func New(log *slog.Logger, cfg Config) (*App, error) {
	cfg.ResolveDefaults()
	var chPool *chclient.Pool
	var stateCollector *clusterstate.Collector
	var tieringController tiering.Controller
	// The PID suffix makes the tag unique per process: the single-mover guard
	// distinguishes a genuinely concurrent instance (duplicate tag, post-boot)
	// from this process's own statements — a version-only tag would make two
	// same-build instances invisible to each other.
	instanceID := fmt.Sprintf("movoor-%s-%d", Version, os.Getpid())
	ops, err := opsserver.New(log, opsserver.Config{
		MetricsAddr:      cfg.MetricsAddr,
		HealthCheckAddr:  cfg.HealthCheckAddr,
		Version:          Version,
		InstanceID:       instanceID,
		TracingEndpoint:  cfg.Tracing.Endpoint,
		TraceSampleRatio: *cfg.Tracing.SampleRatio,
	})
	if err != nil {
		return nil, fmt.Errorf("create ops server: %w", err)
	}
	cleanup := func() {
		if chPool != nil {
			_ = chPool.Close()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = ops.Stop(shutdownCtx)
	}
	if len(cfg.ClickHouse.Nodes) > 0 {
		chPool, err = chclient.NewPool(clickHouseClientConfig(cfg.ClickHouse))
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("create clickhouse clients: %w", err)
		}
		stateCollector = clusterstate.New(chPool, cfg.ClickHouse.QueryTimeout, clusterStateWatches(cfg.Watches))
		tieringController = tiering.New(log, chPool.Clients(), tiering.ControllerConfig{
			Tiering:      cfg.Tiering,
			Watches:      cfg.TieringWatches(),
			QueryTimeout: cfg.ClickHouse.QueryTimeout,
			InstanceID:   instanceID,
			Instrumenter: ops.TieringInstrumenter(),
		})
		ops.SetTieringStore(tieringController.Store())
	}

	var srv server.Server
	if cfg.Frontend.IsEnabled() {
		webFS, fsErr := getWebFS()
		if fsErr != nil {
			// Constructed dependencies must not leak when a later step fails;
			// main exits anyway, but embedders and tests reuse the process.
			cleanup()
			return nil, fmt.Errorf("load embedded web assets: %w", fsErr)
		}

		var tieringStore *tiering.Store
		if tieringController != nil {
			tieringStore = tieringController.Store()
		}
		srv = server.NewWithTiering(log, server.Config{ListenAddress: cfg.Frontend.Addr}, webFS, stateCollector, tieringController, tieringStore)
	}

	return &App{
		log:     log,
		cfg:     cfg,
		server:  srv,
		ops:     ops,
		ch:      chPool,
		state:   stateCollector,
		tiering: tieringController,
	}, nil
}

// Run starts the application and blocks until ctx is cancelled, at which point
// it shuts the HTTP server down gracefully.
func (a *App) Run(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	a.log.InfoContext(ctx, "starting clickhouse-movoor",
		slog.String("version", Version),
		slog.Bool("frontend_enabled", a.cfg.Frontend.IsEnabled()),
		slog.String("listen_address", a.cfg.Frontend.Addr),
		slog.String("metrics_address", a.cfg.MetricsAddr),
		slog.String("health_check_address", a.cfg.HealthCheckAddr),
		slog.Int("clickhouse_nodes", len(a.cfg.ClickHouse.Nodes)),
		slog.Int("watches", len(a.cfg.Watches)),
	)

	if validateErr := a.validateWatches(ctx); validateErr != nil {
		return validateErr
	}

	if ctx.Err() != nil {
		return a.shutdown()
	}
	// HTTP listeners bind first; the tiering controller starts LAST because
	// it is the only component that writes (enforce-mode reconcilers issue
	// ALTERs from their own goroutines). A bind failure — the most common
	// startup error — must happen before any write capability spawns, and
	// any partial start tears down what already started: main exits anyway,
	// but embedders and tests reuse the process, where leaked reconcilers
	// would keep moving parts (and a retried Run would race them under the
	// same instance tag).
	if a.ops != nil {
		if startErr := a.ops.Start(runCtx); startErr != nil {
			return errors.Join(fmt.Errorf("start ops server: %w", startErr), a.shutdown())
		}
	}
	if a.server != nil {
		if startErr := a.server.Start(runCtx); startErr != nil {
			return errors.Join(fmt.Errorf("start server: %w", startErr), a.shutdown())
		}
	}
	if a.tiering != nil {
		if startErr := a.tiering.Start(runCtx); startErr != nil {
			return errors.Join(fmt.Errorf("start tiering controller: %w", startErr), a.shutdown())
		}
	}

	healthMonitorDone := a.startClickHouseHealthMonitor(runCtx)
	<-ctx.Done()
	cancelRun()
	waitForClickHouseHealthMonitor(healthMonitorDone)
	return a.shutdown()
}

func (a *App) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	a.log.InfoContext(shutdownCtx, "shutting down")

	// Every component gets its shutdown attempt even when an earlier one
	// fails — aborting early would leak the pool and skip the span flush.
	var errs []error
	if a.server != nil {
		if stopErr := a.server.Stop(shutdownCtx); stopErr != nil {
			errs = append(errs, fmt.Errorf("stop server: %w", stopErr))
		}
	}

	if a.tiering != nil {
		if stopErr := a.tiering.Stop(shutdownCtx); stopErr != nil {
			errs = append(errs, fmt.Errorf("stop tiering controller: %w", stopErr))
		}
	}
	if a.ops != nil {
		if stopErr := a.ops.Stop(shutdownCtx); stopErr != nil {
			errs = append(errs, fmt.Errorf("stop ops server: %w", stopErr))
		}
	}

	if a.ch != nil {
		if closeErr := a.ch.Close(); closeErr != nil {
			errs = append(errs, fmt.Errorf("close clickhouse clients: %w", closeErr))
		}
	}

	return errors.Join(errs...)
}

func (a *App) validateWatches(ctx context.Context) error {
	if a.state == nil {
		return nil
	}

	a.log.InfoContext(ctx, "validating watches",
		slog.Int("clickhouse_nodes", len(a.cfg.ClickHouse.Nodes)),
		slog.Int("watches", len(a.cfg.Watches)),
	)

	result, err := a.state.ValidateWatchesDetailed(ctx)
	for _, warning := range result.Warnings {
		a.log.WarnContext(ctx, "watch validation warning",
			slog.String("kind", warning.Kind),
			slog.String("code", warning.Code),
			slog.String("node_id", warning.NodeID),
			slog.String("message", warning.Message),
		)
	}
	status := clickHouseValidationStatus(result, err)
	if a.ops != nil {
		a.ops.SetClickHouseStatus(status)
	}
	if status.Status == opsserver.ClickHouseReadinessDegraded {
		a.log.WarnContext(ctx, "clickhouse startup validation degraded",
			slog.Int("nodes_expected", status.NodesExpected),
			slog.Int("nodes_responded", status.NodesResponded),
			slog.Int("nodes_failed", status.NodesFailed),
		)
	}
	if status.Status == opsserver.ClickHouseReadinessUnavailable {
		a.log.WarnContext(ctx, "clickhouse startup validation unavailable",
			slog.Int("nodes_expected", status.NodesExpected),
			slog.Int("nodes_responded", status.NodesResponded),
			slog.Int("nodes_failed", status.NodesFailed),
			slog.String("error", status.LastError),
		)
	}
	if err != nil {
		if isReachabilityOnlyValidationFailure(result) {
			return nil
		}

		return fmt.Errorf("validate watches: %w", err)
	}

	a.log.InfoContext(ctx, "watch validation passed",
		slog.Int("nodes_responded", result.NodesResponded),
		slog.Int("nodes_expected", result.NodesExpected),
		slog.Duration("duration", result.CollectionDuration),
	)

	return nil
}

func isReachabilityOnlyValidationFailure(result clusterstate.WatchValidationResult) bool {
	if result.NodesExpected == 0 || result.NodesResponded != 0 || len(result.Warnings) == 0 {
		return false
	}
	for _, warning := range result.Warnings {
		if warning.Kind != "reachability" || warning.Code != "node_unreachable" {
			return false
		}
	}

	return true
}

func clickHouseValidationStatus(result clusterstate.WatchValidationResult, validationErr error) opsserver.ClickHouseStatus {
	status := opsserver.ClickHouseStatus{
		Status:          opsserver.ClickHouseReadinessOK,
		UpdatedAt:       result.CollectedAt,
		CheckDurationMs: int(result.CollectionDuration.Milliseconds()),
		NodesExpected:   result.NodesExpected,
		NodesResponded:  result.NodesResponded,
		NodesFailed:     result.NodesFailed,
		Warnings:        clickHouseValidationWarnings(result.Warnings),
	}
	if validationErr != nil {
		status.Status = opsserver.ClickHouseReadinessUnavailable
		status.LastError = validationErr.Error()

		return status
	}
	if result.NodesFailed > 0 || len(result.Warnings) > 0 {
		status.Status = opsserver.ClickHouseReadinessDegraded
	}

	return status
}

func clickHouseCollectionStatus(result clusterstate.Result[clusterstate.NodeStatus]) opsserver.ClickHouseStatus {
	status := opsserver.ClickHouseStatus{
		Status:          opsserver.ClickHouseReadinessOK,
		UpdatedAt:       result.CollectedAt,
		CheckDurationMs: int(result.CollectionDuration.Milliseconds()),
		NodesExpected:   result.NodesExpected,
		NodesResponded:  result.NodesResponded,
		NodesFailed:     result.NodesFailed,
		Warnings:        clickHouseValidationWarnings(result.Warnings),
	}
	if result.NodesExpected > 0 && result.NodesResponded == 0 {
		status.Status = opsserver.ClickHouseReadinessUnavailable
		status.LastError = "no configured ClickHouse node responded"

		return status
	}
	if result.NodesFailed > 0 || len(result.Warnings) > 0 {
		status.Status = opsserver.ClickHouseReadinessDegraded
	}

	return status
}

func (a *App) startClickHouseHealthMonitor(ctx context.Context) <-chan struct{} {
	if a.state == nil || a.ops == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(clickHouseHealthRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.ops.SetClickHouseStatus(clickHouseCollectionStatus(a.state.CollectNodes(ctx)))
			}
		}
	}()

	return done
}

func waitForClickHouseHealthMonitor(done <-chan struct{}) {
	if done == nil {
		return
	}
	<-done
}

func clickHouseValidationWarnings(warnings []clusterstate.Warning) []opsserver.ClickHouseWarning {
	items := make([]opsserver.ClickHouseWarning, 0, len(warnings))
	for _, warning := range warnings {
		items = append(items, opsserver.ClickHouseWarning{
			Kind:    warning.Kind,
			Code:    warning.Code,
			Message: warning.Message,
			NodeID:  warning.NodeID,
		})
	}

	return items
}

func clickHouseClientConfig(cfg ClickHouseConfig) chclient.Config {
	nodes := make([]chclient.NodeConfig, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		nodes = append(nodes, chclient.NodeConfig{
			Name:    node.Name,
			Shard:   node.Shard,
			Replica: node.Replica,
			DSN:     node.DSN,
		})
	}

	return chclient.Config{
		DialTimeout:  cfg.DialTimeout,
		QueryTimeout: cfg.QueryTimeout,
		Nodes:        nodes,
	}
}

func clusterStateWatches(watches []WatchConfig) []clusterstate.Watch {
	stateWatches := make([]clusterstate.Watch, 0, len(watches))
	for _, watch := range watches {
		stateWatches = append(stateWatches, clusterstate.Watch{
			Database: watch.Database,
			Table:    watch.Table,
		})
	}

	return stateWatches
}
