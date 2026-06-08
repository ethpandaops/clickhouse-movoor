// Package server provides the HTTP server that serves the embedded
// single-page web application and the JSON API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"time"
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
	log   *slog.Logger
	cfg   Config
	webFS fs.FS
	state StateReader
	http  *http.Server
	addr  string
}

// compile-time check that *server satisfies Server.
var _ Server = (*server)(nil)

type problemDetails struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail"`
	Instance string         `json:"instance,omitempty"`
	Errors   []problemError `json:"errors,omitempty"`
}

// New constructs a Server that serves webFS as a single-page application and
// exposes a small JSON API under /api/v1.
func New(log *slog.Logger, cfg Config, webFS fs.FS, state StateReader) Server {
	return &server{
		log:   log.With(slog.String("component", "server")),
		cfg:   cfg,
		webFS: webFS,
		state: state,
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
		Handler:           s.routes(),
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	go func() {
		s.log.InfoContext(ctx, "http server listening", slog.String("address", s.addr))

		if serveErr := s.http.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
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

	if err := s.http.Shutdown(ctx); err != nil {
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

func (s *server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	s.writeProblem(w, r, problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusNotFound),
		Status:   http.StatusNotFound,
		Detail:   "API route is not implemented",
		Instance: r.URL.RequestURI(),
	})
}

func (s *server) writeProblem(w http.ResponseWriter, r *http.Request, problem problemDetails) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		s.log.ErrorContext(r.Context(), "encode problem response", slog.Any("error", err))
	}
}

// spaHandler serves static assets from the embedded filesystem, falling back to
// index.html for unknown paths so client-side routing can take over.
func (s *server) spaHandler() http.Handler {
	fileServer := http.FileServerFS(s.webFS)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
			if f, err := s.webFS.Open(name); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)

				return
			}
		}

		s.serveIndex(w, r)
	})
}

// serveIndex writes the SPA entrypoint, or a 404 when the web UI has not been
// built into the binary (see web/embed_stub.go).
func (s *server) serveIndex(w http.ResponseWriter, r *http.Request) {
	index, err := s.webFS.Open("index.html")
	if err != nil {
		http.Error(w, "web UI not built", http.StatusNotFound)

		return
	}
	defer func() { _ = index.Close() }()

	stat, err := index.Stat()
	if err != nil {
		http.Error(w, "web UI unavailable", http.StatusInternalServerError)

		return
	}

	seeker, ok := index.(io.ReadSeeker)
	if !ok {
		http.Error(w, "web UI unavailable", http.StatusInternalServerError)

		return
	}

	http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
}
