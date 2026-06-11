// Package opsserver serves movoor operational endpoints: Prometheus metrics
// and a process health endpoint.
package opsserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

const readHeaderTimeout = 10 * time.Second

var shutdownHTTPServer = func(ctx context.Context, srv *http.Server) error { return srv.Shutdown(ctx) }

type Config struct {
	MetricsAddr     string
	HealthCheckAddr string
	Version         string
	InstanceID      string
	TracingEndpoint string
	// TraceSampleRatio is the RESOLVED sampling ratio in [0, 1]; zero means
	// "sample nothing" (config distinguishes unset from explicit zero and
	// resolves before this layer). Direct constructors note: the zero value
	// therefore traces nothing — pass 1 for the historical default.
	TraceSampleRatio float64
}

type ClickHouseReadiness string

const (
	ClickHouseReadinessUnknown     ClickHouseReadiness = "unknown"
	ClickHouseReadinessOK          ClickHouseReadiness = "ok"
	ClickHouseReadinessDegraded    ClickHouseReadiness = "degraded"
	ClickHouseReadinessUnavailable ClickHouseReadiness = "unavailable"
)

type ClickHouseWarning struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"nodeId,omitempty"`
}

type ClickHouseStatus struct {
	Status          ClickHouseReadiness `json:"status"`
	UpdatedAt       time.Time           `json:"updatedAt,omitzero"`
	CheckDurationMs int                 `json:"checkDurationMs,omitempty"`
	NodesExpected   int                 `json:"nodesExpected"`
	NodesResponded  int                 `json:"nodesResponded"`
	NodesFailed     int                 `json:"nodesFailed"`
	LastError       string              `json:"lastError,omitempty"`
	Warnings        []ClickHouseWarning `json:"warnings,omitempty"`
}

type Server struct {
	log       *slog.Logger
	cfg       Config
	store     *tiering.Store
	chStatus  ClickHouseStatus
	tiering   *TieringMetrics
	provider  shutdowner
	tracer    shutdowner
	registry  *promclient.Registry
	metrics   *http.Server
	health    *http.Server
	listeners []net.Listener
	mu        sync.Mutex
}

func New(log *slog.Logger, cfg Config) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID = "default"
	}
	telemetry, err := newTelemetry(cfg)
	if err != nil {
		return nil, err
	}
	tieringMetrics, err := newTieringMetricsFn(telemetry.meter, func() *tiering.Store {
		return nil
	})
	if err != nil {
		return nil, err
	}
	s := &Server{
		log:      log.With(slog.String("component", "opsserver")),
		cfg:      cfg,
		chStatus: ClickHouseStatus{Status: ClickHouseReadinessUnknown},
		tiering:  tieringMetrics,
		provider: telemetry.provider,
		tracer:   telemetry.tracer,
		registry: telemetry.registry,
	}
	tieringMetrics.store = s.Store
	return s, nil
}

func (s *Server) TieringInstrumenter() tiering.Instrumenter {
	if s == nil || s.tiering == nil {
		return nil
	}
	return s.tiering
}

func (s *Server) SetTieringStore(store *tiering.Store) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
}

func (s *Server) SetClickHouseStatus(status ClickHouseStatus) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status.Warnings = append([]ClickHouseWarning(nil), status.Warnings...)
	if status.Status == "" {
		status.Status = ClickHouseReadinessUnknown
	}
	s.chStatus = status
}

func (s *Server) ClickHouseStatus() ClickHouseStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.chStatus
	status.Warnings = append([]ClickHouseWarning(nil), status.Warnings...)
	if status.Status == "" {
		status.Status = ClickHouseReadinessUnknown
	}
	return status
}

func (s *Server) Store() *tiering.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store
}

func (s *Server) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.cfg.MetricsAddr != "" {
		handler := promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})
		if err := s.startHTTP(ctx, s.cfg.MetricsAddr, handler, &s.metrics, "metrics"); err != nil {
			return err
		}
	}
	if s.cfg.HealthCheckAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", s.handleHealth)
		if err := s.startHTTP(ctx, s.cfg.HealthCheckAddr, mux, &s.health, "health"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	// Attempt every shutdown even when one fails: aborting early would skip
	// the meter/span flush and leave listeners running.
	var errs []error
	for _, srv := range []*http.Server{s.metrics, s.health} {
		if srv == nil {
			continue
		}
		if err := shutdownHTTPServer(ctx, srv); err != nil {
			errs = append(errs, err)
		}
	}
	if s.provider != nil {
		if err := s.provider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if s.tracer != nil {
		if err := s.tracer.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Server) startHTTP(ctx context.Context, addr string, handler http.Handler, target **http.Server, name string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	*target = srv
	s.listeners = append(s.listeners, ln)
	go func() {
		s.log.InfoContext(ctx, "ops server listening", slog.String("kind", name), slog.String("address", ln.Addr().String()))
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			s.log.ErrorContext(ctx, "ops server stopped unexpectedly", slog.String("kind", name), slog.Any("error", serveErr))
		}
	}()
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	clickhouseStatus := s.ClickHouseStatus()
	response := map[string]any{
		"status":     "ok",
		"version":    s.cfg.Version,
		"readiness":  clickhouseStatus.Status,
		"clickhouse": clickhouseStatus,
	}
	if store := s.Store(); store != nil {
		status := store.Status()
		response["tiering"] = map[string]any{
			"mode":            status.Mode,
			"pauseState":      status.PauseState,
			"pauseReason":     status.PauseReason,
			"bytesInFlight":   status.BytesInFlight,
			"bytesMovedToday": status.BytesMovedToday,
			"updatedAt":       status.UpdatedAt,
		}
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.log.ErrorContext(r.Context(), "encode health response", slog.Any("error", err))
	}
}
