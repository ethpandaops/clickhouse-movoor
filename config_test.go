package movoor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := LoadConfig(writeConfig(t, tt.yaml))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func writeConfig(t *testing.T, data string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	return path
}
