package chclient

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/XSAM/otelsql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestNewPoolCreatesOneClientPerNode(t *testing.T) {
	t.Parallel()

	pool, err := NewPool(Config{
		DialTimeout: time.Second,
		Nodes: []NodeConfig{
			{
				Name:    "node-0-0",
				Shard:   "0",
				Replica: "0",
				DSN:     "clickhouse://default@localhost:9000/default",
			},
			{
				Name:    "node-0-1",
				Shard:   "0",
				Replica: "1",
				DSN:     "clickhouse://default@localhost:9001/default",
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, pool.Close())
	})

	clients := pool.Clients()
	require.Len(t, clients, 2)
	require.Equal(t, "node-0-0", clients[0].Node.ID)
	require.Equal(t, "localhost:9000", clients[0].Node.Addr)
	require.Equal(t, "node-0-1", clients[1].Node.ID)
	require.Equal(t, "localhost:9001", clients[1].Node.Addr)
}

func TestNewPoolRejectsFailoverDSN(t *testing.T) {
	t.Parallel()

	_, err := NewPool(Config{
		Nodes: []NodeConfig{
			{
				Name:    "node-0-0",
				Shard:   "0",
				Replica: "0",
				DSN:     "clickhouse://default@localhost:9000,localhost:9001/default",
			},
		},
	})
	require.ErrorContains(t, err, "dsn must contain exactly one address")
}

func TestNewPoolRejectsMissingNodes(t *testing.T) {
	t.Parallel()

	_, err := NewPool(Config{})
	require.ErrorContains(t, err, "at least one clickhouse node is required")
}

func TestOpenNodeValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		node    NodeConfig
		wantErr string
	}{
		{
			name: "missing name",
			node: NodeConfig{
				Shard:   "0",
				Replica: "0",
				DSN:     "clickhouse://default@localhost:9000/default",
			},
			wantErr: "name is required",
		},
		{
			name: "missing shard",
			node: NodeConfig{
				Name:    "node-0-0",
				Replica: "0",
				DSN:     "clickhouse://default@localhost:9000/default",
			},
			wantErr: "shard is required",
		},
		{
			name: "missing replica",
			node: NodeConfig{
				Name:  "node-0-0",
				Shard: "0",
				DSN:   "clickhouse://default@localhost:9000/default",
			},
			wantErr: "replica is required",
		},
		{
			name: "parse dsn",
			node: NodeConfig{
				Name:    "node-0-0",
				Shard:   "0",
				Replica: "0",
				DSN:     "://bad",
			},
			wantErr: "parse dsn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, addr, err := openNode(tt.node, 0, 0)

			require.Nil(t, db)
			require.Empty(t, addr)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestOpenNodeAppliesDefaults(t *testing.T) {
	t.Parallel()

	db, addr, err := openNode(NodeConfig{
		Name:    "node-0-0",
		Shard:   "0",
		Replica: "0",
		DSN:     "clickhouse://default@localhost:9000/default",
	}, time.Second, 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, "localhost:9000", addr)
	require.NoError(t, db.Close())
}

func TestApplyQueryTimeout(t *testing.T) {
	t.Parallel()

	opts := &clickhouse.Options{}
	applyQueryTimeout(opts, 1500*time.Millisecond)
	require.Equal(t, clickhouse.Settings{"max_execution_time": 2}, opts.Settings)

	applyQueryTimeout(opts, 3*time.Second)
	require.Equal(t, clickhouse.Settings{"max_execution_time": 2}, opts.Settings)

	withoutTimeout := &clickhouse.Options{}
	applyQueryTimeout(withoutTimeout, 0)
	require.Nil(t, withoutTimeout.Settings)
}

func TestPoolNilMethods(t *testing.T) {
	t.Parallel()

	require.Nil(t, (*Pool)(nil).Clients())
	require.NoError(t, (*Pool)(nil).Close())
}

func TestNewPoolDoesNotEchoCredentialsOnParseFailure(t *testing.T) {
	t.Parallel()

	_, err := NewPool(Config{
		Nodes: []NodeConfig{
			{
				Name:    "node-0-0",
				Shard:   "0",
				Replica: "0",
				DSN:     "clickhouse://default:supersecret@localhost:bad-port/default",
			},
		},
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "supersecret")
}

func TestRedactDSNErrorNil(t *testing.T) {
	t.Parallel()

	require.NoError(t, RedactDSNError(nil, "clickhouse://user:secret@host:9000/default"))
}

func TestRedactDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "password is masked",
			raw:  "clickhouse://user:secret@host:9000/db",
			want: "clickhouse://user:xxxxx@host:9000/db",
		},
		{
			name: "no password passes through",
			raw:  "clickhouse://user@host:9000/db",
			want: "clickhouse://user@host:9000/db",
		},
		{
			name: "no userinfo passes through",
			raw:  "clickhouse://host:9000/db",
			want: "clickhouse://host:9000/db",
		},
		{
			name: "no scheme still masks",
			raw:  "user:secret@host:9000",
			want: "user:xxxxx@host:9000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, RedactDSN(tt.raw))
		})
	}
}

func TestQuerySpanName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method otelsql.Method
		query  string
		want   string
	}{
		{
			name:   "select query names by verb",
			method: otelsql.MethodConnQuery,
			query:  "SELECT 1 FROM system.parts",
			want:   "clickhouse.select",
		},
		{
			name:   "alter statement names by verb",
			method: otelsql.MethodConnExec,
			query:  "ALTER TABLE db.t MOVE PARTITION ID 'p'",
			want:   "clickhouse.alter",
		},
		{
			name:   "leading whitespace is ignored",
			method: otelsql.MethodConnExec,
			query:  "  optimize TABLE db.t",
			want:   "clickhouse.optimize",
		},
		{
			name:   "empty query falls back to the driver method",
			method: otelsql.MethodConnPing,
			query:  "",
			want:   "sql.conn.ping",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, querySpanName(context.Background(), tt.method, tt.query))
		})
	}
}

func TestChildSpansOnly(t *testing.T) {
	t.Parallel()

	require.False(t, childSpansOnly(context.Background(), otelsql.MethodConnQuery, "SELECT 1", nil),
		"calls without a parent span must not create root spans")

	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x01},
		SpanID:  trace.SpanID{0x01},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), parent)
	require.True(t, childSpansOnly(ctx, otelsql.MethodConnQuery, "SELECT 1", nil))
}
