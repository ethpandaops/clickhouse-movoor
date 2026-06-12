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

	"github.com/ogen-go/ogen/ogenerrors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/ethpandaops/clickhouse-movoor/api/rest"
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
	log           *slog.Logger
	cfg           Config
	webFS         fs.FS
	state         StateReader
	tiering       TieringController
	tieringStore  *tiering.Store
	http          *http.Server
	addr          string
	serve         func(*http.Server, net.Listener) error
	shutdown      func(context.Context) error
	newRESTServer func(rest.Handler, ...rest.ServerOption) (*rest.Server, error)
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

	routes, err := s.routes()
	if err != nil {
		_ = ln.Close()

		return err
	}

	s.addr = ln.Addr().String()
	s.http = &http.Server{
		Handler:           otelhttp.NewHandler(routes, "movoor.http"),
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

// routes builds the HTTP handler tree: the ogen-generated API server mounted
// under /api/v1 plus the embedded SPA for everything else.
func (s *server) routes() (http.Handler, error) {
	handler := &apiHandler{
		log:          s.log,
		state:        s.state,
		tiering:      s.tiering,
		tieringStore: s.tieringStore,
	}
	newRESTServer := s.newRESTServer
	if newRESTServer == nil {
		newRESTServer = rest.NewServer
	}
	restServer, err := newRESTServer(handler,
		rest.WithPathPrefix("/api/v1"),
		rest.WithNotFound(s.handleAPINotFound),
		rest.WithErrorHandler(s.handleAPIError),
	)
	if err != nil {
		return nil, fmt.Errorf("create api server: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", restServer)
	mux.Handle("/", s.spaHandler())

	return mux, nil
}

// handleAPINotFound keeps unmatched API paths on the problem+json contract.
func (s *server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	s.writeProblemJSON(w, r, http.StatusNotFound, "API route is not implemented")
}

// handleAPIError renders ogen decode/validation failures (bad parameters,
// malformed bodies) as problem+json with the status ogen classified.
func (s *server) handleAPIError(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	status := ogenerrors.ErrorCode(err)
	s.writeProblemJSON(w, r, status, err.Error())
	s.log.DebugContext(ctx, "api request rejected", slog.Int("status", status), slog.Any("error", err))
}

func (s *server) writeProblemJSON(w http.ResponseWriter, r *http.Request, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	encodeErr := json.NewEncoder(w).Encode(map[string]any{
		"type":     "about:blank",
		"title":    http.StatusText(status),
		"status":   status,
		"detail":   detail,
		"instance": r.URL.RequestURI(),
	})
	if encodeErr != nil {
		s.log.DebugContext(r.Context(), "encode problem response", slog.Any("error", encodeErr))
	}
}
