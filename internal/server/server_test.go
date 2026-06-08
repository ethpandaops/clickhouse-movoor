package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/server"
)

func TestServer(t *testing.T) {
	t.Parallel()

	webFS := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('hi')")},
	}

	log := slog.New(slog.DiscardHandler)
	srv := server.New(log, server.Config{ListenAddress: "127.0.0.1:0"}, webFS)

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	base := "http://" + srv.Addr()

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "health endpoint",
			path:       "/api/v1/healthz",
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok"}`,
		},
		{
			name:       "static asset served directly",
			path:       "/assets/app.js",
			wantStatus: http.StatusOK,
			wantBody:   "console.log('hi')",
		},
		{
			name:       "unknown path falls back to index",
			path:       "/some/client/route",
			wantStatus: http.StatusOK,
			wantBody:   "<!doctype html><title>test</title>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+tt.path, nil)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			require.Equal(t, tt.wantStatus, resp.StatusCode)
			require.Equal(t, tt.wantBody, strings.TrimSpace(string(body)))
		})
	}
}
