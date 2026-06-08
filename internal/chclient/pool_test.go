package chclient

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
