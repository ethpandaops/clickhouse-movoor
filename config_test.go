package movoor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(writeConfig(t, `
logging: debug
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default:password@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
frontend:
  enabled: true
`))
	require.NoError(t, err)

	require.Equal(t, "debug", cfg.Logging)
	require.Equal(t, ":9090", cfg.MetricsAddr)
	require.Equal(t, ":8081", cfg.HealthCheckAddr)
	require.Equal(t, 30*time.Second, cfg.ClickHouse.QueryTimeout)
	require.Equal(t, 5*time.Second, cfg.ClickHouse.DialTimeout)
	require.Equal(t, ":8080", cfg.Frontend.Addr)
	require.True(t, cfg.Frontend.IsEnabled())
	require.Equal(t, "node-0-0", cfg.ClickHouse.Nodes[0].Name)
	require.Equal(t, "events", cfg.Watches[0].Table)
}

func TestLoadConfigReadAndParseErrors(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "read config")

	_, err = LoadConfig(writeConfig(t, "clickhouse: ["))
	require.ErrorContains(t, err, "parse config")
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

	require.Equal(t, "info", cfg.Logging)
	require.Equal(t, ":9090", cfg.MetricsAddr)
	require.Equal(t, ":8081", cfg.HealthCheckAddr)
	require.Equal(t, 30*time.Second, cfg.ClickHouse.QueryTimeout)
	require.Equal(t, 5*time.Second, cfg.ClickHouse.DialTimeout)
	require.Equal(t, ":8080", cfg.Frontend.Addr)
	require.True(t, cfg.Frontend.IsEnabled())
}

//nolint:funlen // Validation cases are kept together so the matrix is easy to scan.
func TestLoadConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing clickhouse nodes",
			yaml: `
watches:
  - database: default
    table: events
`,
			wantErr: "clickhouse.nodes must contain at least one node",
		},
		{
			name: "duplicate node name",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
    - name: node-0-0
      shard: "0"
      replica: "1"
      dsn: "clickhouse://default@clickhouse-0-1:9000/default"
watches:
  - database: default
    table: events
`,
			wantErr: `name "node-0-0" is duplicated`,
		},
		{
			name: "missing node name",
			yaml: `
clickhouse:
  nodes:
    - shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
`,
			wantErr: "clickhouse.nodes[0].name is required",
		},
		{
			name: "missing node shard",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
`,
			wantErr: "clickhouse.nodes[0].shard is required",
		},
		{
			name: "missing node replica",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
`,
			wantErr: "clickhouse.nodes[0].replica is required",
		},
		{
			name: "missing node dsn",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
watches:
  - database: default
    table: events
`,
			wantErr: "clickhouse.nodes[0].dsn: is required",
		},
		{
			name: "bad dsn scheme",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "http://clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
`,
			wantErr: "must use clickhouse:// native protocol DSN",
		},
		{
			name: "bad dsn parse",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://%zz"
watches:
  - database: default
    table: events
`,
			wantErr: "clickhouse.nodes[0].dsn: parse",
		},
		{
			name: "bad dsn host",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse:///default"
watches:
  - database: default
    table: events
`,
			wantErr: "host is required",
		},
		{
			name: "failover dsn",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000,clickhouse-0-1:9000/default"
watches:
  - database: default
    table: events
`,
			wantErr: "must identify exactly one ClickHouse host",
		},
		{
			name: "missing watches",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
`,
			wantErr: "watches must contain at least one table",
		},
		{
			name: "missing watch database",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - table: events
`,
			wantErr: "watches[0].database is required",
		},
		{
			name: "missing watch table",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
`,
			wantErr: "watches[0].table is required",
		},
		{
			name: "duplicate watch",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
  - database: default
    table: events
`,
			wantErr: `watches[1] "default.events" is duplicated`,
		},
		{
			name: "invalid logging",
			yaml: `
logging: trace
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
`,
			wantErr: "logging must be one of debug, info, warn, or error",
		},
		{
			name: "invalid tracing",
			yaml: `
tracing:
  endpoint: missing-port
`,
			wantErr: "tracing.endpoint",
		},
		{
			name: "invalid tiering",
			yaml: `
tiering:
  safety:
    maxMovesPerCycle: -1
`,
			wantErr: "tiering.safety.maxMovesPerCycle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := LoadConfig(writeConfig(t, tt.yaml))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestResolveTieringMarshalError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Watches = []WatchConfig{{Database: "db", Table: "tbl", Tier: yaml.Node{Kind: yaml.Kind(99)}}}
	err := cfg.ResolveTiering()
	require.ErrorContains(t, err, "marshal tier block")
}

func TestLoadConfigParseError(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(writeConfig(t, "logging: ["))
	require.ErrorContains(t, err, "parse config")
}

func TestLoadConfigTieringResolution(t *testing.T) {
	t.Setenv("CLICKHOUSE_PASSWORD", "secret")

	cfg, err := LoadConfig(writeConfig(t, `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://movoor:${CLICKHOUSE_PASSWORD}@clickhouse-0-0:9000/default"
tiering:
  mode: enforce
  defaults:
    targetDisk: s3_cache
    quietFor: 12h
watches:
  - database: default
    table: observe_only
  - database: default
    table: frontier_table
    tier:
      age:
        basis: frontier
        field: block_number
        keepLast: 100
      mode: plan
      excludePartitions:
        - "('mainnet',1)"
  - database: default
    table: time_table
    tier:
      age:
        basis: partitionTime
        olderThan: 35d
`))
	require.NoError(t, err)

	require.Contains(t, cfg.ClickHouse.Nodes[0].DSN, ":secret@")
	require.Nil(t, cfg.Watches[0].TierSettings)
	require.NotNil(t, cfg.Watches[1].TierSettings)
	require.Equal(t, "12h0m0s", cfg.Watches[1].TierSettings.QuietFor.String())
	require.Equal(t, "s3_cache", cfg.Watches[1].TierSettings.TargetDisk)
	require.Equal(t, "plan", string(cfg.Watches[1].TierSettings.Mode))
	require.Equal(t, "enforce", string(cfg.Watches[2].TierSettings.Mode))

	watches := cfg.TieringWatches()
	require.Len(t, watches, 3)
	require.Nil(t, watches[0].Settings)
	require.Equal(t, "frontier_table", watches[1].Table)
}

func TestLoadConfigTieringResolutionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "unknown tier key",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
    tier:
      nope: true
`,
			wantErr: "field nope not found",
		},
		{
			name: "tier missing age",
			yaml: `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@clickhouse-0-0:9000/default"
watches:
  - database: default
    table: events
    tier:
      targetDisk: s3_cache
`,
			wantErr: "watches[0].tier.age.basis is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadConfig(writeConfig(t, tt.yaml))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateFrontendRequiresAddrWhenEnabled(t *testing.T) {
	t.Parallel()

	enabled := true
	cfg := Config{
		Logging: "info",
		ClickHouse: ClickHouseConfig{Nodes: []ClickHouseNodeConfig{{
			Name:    "node-0-0",
			Shard:   "0",
			Replica: "0",
			DSN:     "clickhouse://default@clickhouse-0-0:9000/default",
		}}},
		Watches:  []WatchConfig{{Database: "default", Table: "events"}},
		Frontend: FrontendConfig{Enabled: &enabled},
	}

	require.ErrorContains(t, cfg.Validate(), "frontend.addr is required when frontend.enabled is true")
}

func writeConfig(t *testing.T, data string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	return path
}

func TestTracingSampleRatioExplicitZeroSurvives(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(writeConfig(t, `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@localhost:9000/default"
watches:
  - database: default
    table: events
tracing:
  endpoint: localhost:4317
  sampleRatio: 0
`))
	require.NoError(t, err)
	require.NotNil(t, cfg.Tracing.SampleRatio)
	// An explicit zero means "record nothing" and must not be coerced to the
	// default of 1 by zero-value backfill.
	require.Zero(t, *cfg.Tracing.SampleRatio)

	cfg, err = LoadConfig(writeConfig(t, `
clickhouse:
  nodes:
    - name: node-0-0
      shard: "0"
      replica: "0"
      dsn: "clickhouse://default@localhost:9000/default"
watches:
  - database: default
    table: events
`))
	require.NoError(t, err)
	require.NotNil(t, cfg.Tracing.SampleRatio)
	require.InEpsilon(t, 1.0, *cfg.Tracing.SampleRatio, 1e-9)
}

func TestLoadConfigEmptyFile(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(writeConfig(t, "\n  \n"))
	require.ErrorContains(t, err, "is empty")
}
