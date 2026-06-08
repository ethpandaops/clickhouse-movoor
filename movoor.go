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
}

// New constructs an App from the given logger and configuration. It performs
// only lightweight wiring; heavy initialisation happens in Run.
func New(log *slog.Logger, cfg Config) (*App, error) {
	webFS, err := web.GetFS()
	if err != nil {
		return nil, fmt.Errorf("load embedded web assets: %w", err)
	}

	srv := server.New(log, server.Config{ListenAddress: cfg.HTTP.ListenAddress}, webFS)

	return &App{
		log:    log,
		cfg:    cfg,
		server: srv,
	}, nil
}

// Run starts the application and blocks until ctx is cancelled, at which point
// it shuts the HTTP server down gracefully.
func (a *App) Run(ctx context.Context) error {
	a.log.InfoContext(ctx, "starting clickhouse-movoor",
		slog.String("version", Version),
		slog.String("listen_address", a.cfg.HTTP.ListenAddress),
	)

	if err := a.server.Start(ctx); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	a.log.InfoContext(shutdownCtx, "shutting down")

	if err := a.server.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("stop server: %w", err)
	}

	return nil
}
