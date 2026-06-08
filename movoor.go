// Package movoor is the application root for clickhouse-movoor. It wires
// configuration to the HTTP server that serves the embedded single-page web
// application alongside a small JSON API.
//
// This is a skeleton: the domain logic lives in internal/ packages and is
// orchestrated from here. Add new subsystems by giving them their own package
// under internal/ and starting them from App.Run.
package movoor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/server"
	"github.com/ethpandaops/clickhouse-movoor/web"
)

// shutdownTimeout bounds how long Run waits for in-flight requests to drain.
const shutdownTimeout = 10 * time.Second

// App is the top-level application. It owns long-lived dependencies and
// orchestrates their lifecycle.
type App struct {
	log    *slog.Logger
	cfg    Config
	server server.Server
	ch     *chclient.Pool
	state  *clusterstate.Collector
}

// New constructs an App from the given logger and configuration. It performs
// only lightweight wiring; heavy initialisation happens in Run.
func New(log *slog.Logger, cfg Config) (*App, error) {
	var chPool *chclient.Pool
	var stateCollector *clusterstate.Collector
	if len(cfg.ClickHouse.Nodes) > 0 {
		var err error
		chPool, err = chclient.NewPool(clickHouseClientConfig(cfg.ClickHouse))
		if err != nil {
			return nil, fmt.Errorf("create clickhouse clients: %w", err)
		}
		stateCollector = clusterstate.New(chPool, cfg.ClickHouse.QueryTimeout, clusterStateWatches(cfg.Watches))
	}

	var srv server.Server
	if cfg.Frontend.IsEnabled() {
		webFS, err := web.GetFS()
		if err != nil {
			return nil, fmt.Errorf("load embedded web assets: %w", err)
		}

		srv = server.New(log, server.Config{ListenAddress: cfg.Frontend.Addr}, webFS, stateCollector)
	}

	return &App{
		log:    log,
		cfg:    cfg,
		server: srv,
		ch:     chPool,
		state:  stateCollector,
	}, nil
}

// Run starts the application and blocks until ctx is cancelled, at which point
// it shuts the HTTP server down gracefully.
func (a *App) Run(ctx context.Context) error {
	a.log.InfoContext(ctx, "starting clickhouse-movoor",
		slog.String("version", Version),
		slog.Bool("frontend_enabled", a.cfg.Frontend.IsEnabled()),
		slog.String("listen_address", a.cfg.Frontend.Addr),
		slog.String("metrics_address", a.cfg.MetricsAddr),
		slog.String("health_check_address", a.cfg.HealthCheckAddr),
		slog.Int("clickhouse_nodes", len(a.cfg.ClickHouse.Nodes)),
		slog.Int("watches", len(a.cfg.Watches)),
	)

	if err := a.validateWatches(ctx); err != nil {
		return err
	}

	if a.server != nil {
		if err := a.server.Start(ctx); err != nil {
			return fmt.Errorf("start server: %w", err)
		}
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	a.log.InfoContext(shutdownCtx, "shutting down")

	if a.server != nil {
		if err := a.server.Stop(shutdownCtx); err != nil {
			return fmt.Errorf("stop server: %w", err)
		}
	}

	if a.ch != nil {
		if err := a.ch.Close(); err != nil {
			return fmt.Errorf("close clickhouse clients: %w", err)
		}
	}

	return nil
}

func (a *App) validateWatches(ctx context.Context) error {
	if a.state == nil {
		return nil
	}

	warnings, err := a.state.ValidateWatches(ctx)
	for _, warning := range warnings {
		a.log.WarnContext(ctx, "watch validation warning",
			slog.String("kind", warning.Kind),
			slog.String("code", warning.Code),
			slog.String("node_id", warning.NodeID),
			slog.String("message", warning.Message),
		)
	}
	if err != nil {
		return fmt.Errorf("validate watches: %w", err)
	}

	return nil
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
		DialTimeout: cfg.DialTimeout,
		Nodes:       nodes,
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
