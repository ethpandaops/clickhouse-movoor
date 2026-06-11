// Package server provides the HTTP server that serves the embedded
// single-page web application and the JSON API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

// readHeaderTimeout bounds how long the server waits for request headers,
// guarding against slow-loris clients.
const readHeaderTimeout = 10 * time.Second

// Config configures the HTTP Server.
type Config struct {
	// ListenAddress is the host:port the server binds to.
	ListenAddress string
}

// Server is the HTTP server lifecycle contract.
type Server interface {
	// Start binds the listener and serves requests in the background. It
	// returns once the listener is bound or fails to bind.
	Start(ctx context.Context) error
	// Stop gracefully shuts the server down, respecting ctx's deadline.
	Stop(ctx context.Context) error
	// Addr returns the bound network address. It is only meaningful after a
	// successful Start (useful when binding to port 0).
	Addr() string
}

type server struct {
	log          *slog.Logger
	cfg          Config
	webFS        fs.FS
	state        StateReader
	tiering      TieringController
	tieringStore *tiering.Store
	http         *http.Server
	addr         string
	serve        func(*http.Server, net.Listener) error
	shutdown     func(context.Context) error
}

// compile-time check that *server satisfies Server.
var _ Server = (*server)(nil)

// TieringController is the write-capable control surface exposed by the API.
type TieringController interface {
	Apply(ctx context.Context, nodeID string, database string, table string, partitionID string, stateToken string) (tiering.HistoryEntry, error)
	Retry(ctx context.Context, nodeID string, database string, table string, partitionID string, stateToken string) (tiering.HistoryEntry, error)
	Pause(reason tiering.PauseReason) tiering.StatusSnapshot
	Resume() tiering.StatusSnapshot
	InFlight() []tiering.InFlightLeg
}

// New constructs a Server that serves webFS as a single-page application and
// exposes a small JSON API under /api/v1.
func New(log *slog.Logger, cfg Config, webFS fs.FS, state StateReader) Server {
	return NewWithTiering(log, cfg, webFS, state, nil, nil)
}

// NewWithTiering constructs a Server with tiering plan/control APIs enabled.
func NewWithTiering(log *slog.Logger, cfg Config, webFS fs.FS, state StateReader, tieringController TieringController, tieringStore *tiering.Store) Server {
	return &server{
		log:          log.With(slog.String("component", "server")),
		cfg:          cfg,
		webFS:        webFS,
		state:        state,
		tiering:      tieringController,
		tieringStore: tieringStore,
	}
}

// Start binds the listener and serves in a background goroutine.
func (s *server) Start(ctx context.Context) error {
	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", s.cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.ListenAddress, err)
	}

	s.addr = ln.Addr().String()
	s.http = &http.Server{
		Handler:           otelhttp.NewHandler(s.routes(), "movoor.http"),
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	go func() {
		s.log.InfoContext(ctx, "http server listening", slog.String("address", s.addr))

		serve := s.serve
		if serve == nil {
			serve = func(httpServer *http.Server, listener net.Listener) error {
				return httpServer.Serve(listener)
			}
		}
		if serveErr := serve(s.http, ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.log.ErrorContext(ctx, "http server stopped unexpectedly", slog.Any("error", serveErr))
		}
	}()

	return nil
}

// Stop gracefully shuts the server down.
func (s *server) Stop(ctx context.Context) error {
	if s.http == nil {
		return nil
	}

	shutdown := s.shutdown
	if shutdown == nil {
		shutdown = s.http.Shutdown
	}
	if err := shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	return nil
}

// Addr returns the bound network address.
func (s *server) Addr() string {
	return s.addr
}

// routes builds the HTTP handler tree.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	mux.HandleFunc("GET /api/v1/storage/disks", s.handleStorageDisks)
	mux.HandleFunc("GET /api/v1/tables", s.handleTables)
	mux.HandleFunc("GET /api/v1/tables/{database}/{table}", s.handleTable)
	mux.HandleFunc("GET /api/v1/tables/{database}/{table}/columns", s.handleTableColumns)
	mux.HandleFunc("GET /api/v1/tables/{database}/{table}/partitions", s.handleTablePartitions)
	mux.HandleFunc("GET /api/v1/tables/{database}/{table}/parts", s.handleTableParts)
	mux.HandleFunc("GET /api/v1/tables/{database}/{table}/detached-parts", s.handleDetachedParts)
	mux.HandleFunc("GET /api/v1/operations", s.handleOperations)
	mux.HandleFunc("GET /api/v1/operations/mutations", s.handleMutations)
	mux.HandleFunc("GET /api/v1/operations/replication-queue", s.handleReplicationQueue)
	mux.HandleFunc("GET /api/v1/part-events", s.handlePartEvents)
	mux.HandleFunc("GET /api/v1/conditions", s.handleConditions)
	mux.HandleFunc("GET /api/v1/tiering/plan", s.handleTieringPlan)
	mux.HandleFunc("GET /api/v1/tiering/status", s.handleTieringStatus)
	mux.HandleFunc("GET /api/v1/tiering/history", s.handleTieringHistory)
	mux.HandleFunc("POST /api/v1/tiering/pause", s.handleTieringPause)
	mux.HandleFunc("POST /api/v1/tiering/resume", s.handleTieringResume)
	mux.HandleFunc("POST /api/v1/tiering/tables/{database}/{table}/partitions/{partitionId}/apply", s.handleTieringApply)
	mux.HandleFunc("POST /api/v1/tiering/tables/{database}/{table}/partitions/{partitionId}/retry", s.handleTieringRetry)
	mux.HandleFunc("/api/", s.handleAPINotFound)
	mux.Handle("/", s.spaHandler())

	return mux
}

// handleHealth reports basic liveness as JSON.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		s.log.ErrorContext(r.Context(), "encode health response", slog.Any("error", err))
	}
}
