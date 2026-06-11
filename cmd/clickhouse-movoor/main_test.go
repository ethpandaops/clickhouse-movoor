package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	movoor "github.com/ethpandaops/clickhouse-movoor"
)

func TestSetLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		configured string
		want       slog.Level
	}{
		{configured: "debug", want: slog.LevelDebug},
		{configured: "info", want: slog.LevelInfo},
		{configured: "warn", want: slog.LevelWarn},
		{configured: "warning", want: slog.LevelWarn},
		{configured: "error", want: slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.configured, func(t *testing.T) {
			t.Parallel()

			var level slog.LevelVar
			require.NoError(t, setLogLevel(&level, tt.configured))
			require.Equal(t, tt.want, level.Level())
		})
	}
}

func TestSetLogLevelRejectsUnknown(t *testing.T) {
	t.Parallel()

	var level slog.LevelVar
	require.ErrorContains(t, setLogLevel(&level, "trace"), `invalid logging level "trace"`)
}

func TestNewLogger(t *testing.T) {
	t.Parallel()

	var level slog.LevelVar
	require.NotNil(t, newLogger(&level, "json", true))
	require.NotNil(t, newLogger(&level, "text", false))
	require.NotNil(t, newLogger(&level, "unknown", false))
}

func TestMainVersionCommand(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"clickhouse-movoor", "version"}

	main()
}

func TestMainWith(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		runErr   error
		wantOut  string
		wantCode int
	}{
		{name: "ok"},
		{name: "cancelled", runErr: context.Canceled, wantOut: "Cancelled.\n"},
		{name: "error", runErr: errors.New("boom"), wantOut: "Error: boom\n", wantCode: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			code := 0
			mainWith(func() error { return tt.runErr }, func(got int) { code = got }, &out)

			require.Equal(t, tt.wantOut, out.String())
			require.Equal(t, tt.wantCode, code)
		})
	}
}

func TestRunVersionCommand(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"clickhouse-movoor", "version"}

	require.NoError(t, run())
}

func TestRunConfigError(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"clickhouse-movoor", "--config", filepath.Join(t.TempDir(), "missing.yaml")}

	require.ErrorContains(t, run(), "load config")
}

func TestRunApplicationStartsWithUnavailableClickHouse(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
logging: info
metricsAddr: "127.0.0.1:-1"
healthCheckAddr: ""
clickhouse:
  dialTimeout: 1ms
  queryTimeout: 1ms
  nodes:
    - name: node-a
      shard: shard1
      replica: replica1
      dsn: clickhouse://default@127.0.0.1:1/default
watches:
  - database: db
    table: tbl
frontend:
  enabled: false
`), 0o600))

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"clickhouse-movoor", "--config", configPath, "--verbose", "--log-format", "json"}

	err := run()
	require.ErrorContains(t, err, "start ops server")
	require.NotContains(t, err.Error(), "validate watches")
}

func TestRunApplicationInitError(t *testing.T) {
	oldLoad := loadConfigForRun
	t.Cleanup(func() { loadConfigForRun = oldLoad })
	disabled := false
	loadConfigForRun = func(string) (movoor.Config, error) {
		cfg := movoor.DefaultConfig()
		cfg.Frontend.Enabled = &disabled
		cfg.ClickHouse.Nodes = []movoor.ClickHouseNodeConfig{{
			Name:    "node-a",
			Shard:   "shard1",
			Replica: "replica1",
			DSN:     "clickhouse://default@localhost:9000,localhost:9001/default",
		}}
		cfg.Watches = []movoor.WatchConfig{{Database: "db", Table: "tbl"}}

		return cfg, nil
	}

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"clickhouse-movoor"}

	require.ErrorContains(t, run(), "init clickhouse-movoor")
}

func TestRunRejectsLoadedInvalidLogLevel(t *testing.T) {
	oldLoad := loadConfigForRun
	t.Cleanup(func() { loadConfigForRun = oldLoad })
	loadConfigForRun = func(string) (movoor.Config, error) {
		cfg := movoor.DefaultConfig()
		cfg.Logging = "trace"

		return cfg, nil
	}

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"clickhouse-movoor"}

	require.ErrorContains(t, run(), `invalid logging level "trace"`)
}

func TestLoadConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := loadConfig("")
	require.ErrorContains(t, err, "config file is required")

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
logging: error
clickhouse:
  nodes:
    - name: node-a
      shard: shard1
      replica: replica1
      dsn: clickhouse://default@localhost:9000/default
watches:
  - database: db
    table: tbl
`), 0o600))

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "error", cfg.Logging)

	homeConfig := filepath.Join(home, ".clickhouse-movoor", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(homeConfig), 0o755))
	require.NoError(t, os.WriteFile(homeConfig, []byte(`
logging: warn
clickhouse:
  nodes:
    - name: node-a
      shard: shard1
      replica: replica1
      dsn: clickhouse://default@localhost:9000/default
watches:
  - database: db
    table: tbl
`), 0o600))

	cfg, err = loadConfig("")
	require.NoError(t, err)
	require.Equal(t, "warn", cfg.Logging)

	_, err = loadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "load config")
}

func TestDiscoverConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.Empty(t, discoverConfigPath())

	want := filepath.Join(home, ".clickhouse-movoor", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(want), 0o755))
	require.NoError(t, os.WriteFile(want, []byte("logging: info\n"), 0o600))
	require.Equal(t, want, discoverConfigPath())
}

func TestDiscoverConfigPathUserHomeError(t *testing.T) {
	t.Setenv("HOME", "")
	require.Empty(t, discoverConfigPath())
}

func TestVersionCommand(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})
	version, commit, date = "v1", "abc123", "2026-01-01"

	cmd := versionCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)

	require.NoError(t, cmd.Execute())
	require.Equal(t, "clickhouse-movoor v1 (commit: abc123, built: 2026-01-01)\n", out.String())
}
