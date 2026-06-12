package clusterstate_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
)

func TestCollectorDevClickHouse2S2R(t *testing.T) {
	if os.Getenv("MOVOOR_CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set MOVOOR_CLICKHOUSE_INTEGRATION=1 with dev/clickhouse-2s2r running")
	}

	pool, err := chclient.NewPool(chclient.Config{
		DialTimeout: 5 * time.Second,
		Nodes: []chclient.NodeConfig{
			{
				Name:    "clickhouse-shard1-replica1",
				Shard:   "shard1",
				Replica: "replica1",
				DSN:     "clickhouse://default@127.0.0.1:9000/default",
			},
			{
				Name:    "clickhouse-shard1-replica2",
				Shard:   "shard1",
				Replica: "replica2",
				DSN:     "clickhouse://default@127.0.0.1:9001/default",
			},
			{
				Name:    "clickhouse-shard2-replica1",
				Shard:   "shard2",
				Replica: "replica1",
				DSN:     "clickhouse://default@127.0.0.1:9002/default",
			},
			{
				Name:    "clickhouse-shard2-replica2",
				Shard:   "shard2",
				Replica: "replica2",
				DSN:     "clickhouse://default@127.0.0.1:9003/default",
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, pool.Close())
	})

	collector := clusterstate.New(pool, 10*time.Second, []clusterstate.Watch{
		{Database: "movoor_dev", Table: "test_generic_network_month_local"},
		{Database: "movoor_dev", Table: "test_generic_plain_month_local"},
	})
	ctx := context.Background()

	nodes := collector.CollectNodes(ctx)
	require.False(t, nodes.Partial(), "node warnings: %#v", nodes.Warnings)
	require.Equal(t, 4, nodes.NodesExpected)
	require.Equal(t, 4, nodes.NodesResponded)
	require.Len(t, nodes.Items, 4)
	for _, node := range nodes.Items {
		require.True(t, node.Reachable, node.Node.ID)
		require.Equal(t, "26.2.5.45", node.Version)
		require.Equal(t, "UTC", node.Timezone)
		require.NotZero(t, node.UptimeSeconds)
	}

	disks := collector.CollectDisks(ctx)
	require.False(t, disks.Partial(), "disk warnings: %#v", disks.Warnings)
	require.Equal(t, 4, disks.NodesExpected)
	require.Equal(t, 4, disks.NodesResponded)
	require.Len(t, disks.Items, 12)

	byNodeDisk := make(map[string]clusterstate.Disk, len(disks.Items))
	for _, disk := range disks.Items {
		byNodeDisk[disk.Node.ID+"/"+disk.Name] = disk
	}
	for _, nodeID := range []string{
		"clickhouse-shard1-replica1",
		"clickhouse-shard1-replica2",
		"clickhouse-shard2-replica1",
		"clickhouse-shard2-replica2",
	} {
		defaultDisk := byNodeDisk[nodeID+"/default"]
		require.True(t, defaultDisk.CapacityKnown, nodeID)
		require.NotNil(t, defaultDisk.FreeSpaceBytes, nodeID)
		require.Equal(t, "Local", defaultDisk.Type, nodeID)

		s3Disk := byNodeDisk[nodeID+"/s3"]
		require.False(t, s3Disk.CapacityKnown, nodeID)
		require.Nil(t, s3Disk.FreeSpaceBytes, nodeID)
		require.True(t, s3Disk.IsRemote, nodeID)

		s3CacheDisk := byNodeDisk[nodeID+"/s3_cache"]
		require.False(t, s3CacheDisk.CapacityKnown, nodeID)
		require.Nil(t, s3CacheDisk.FreeSpaceBytes, nodeID)
		require.True(t, s3CacheDisk.IsRemote, nodeID)
	}

	warnings, err := collector.ValidateWatches(ctx)
	require.NoError(t, err)
	require.Empty(t, warnings)
}

func TestValidateWatchesRejectsDistributedTables(t *testing.T) {
	if os.Getenv("MOVOOR_CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set MOVOOR_CLICKHOUSE_INTEGRATION=1 with dev/clickhouse-2s2r running")
	}

	pool, err := chclient.NewPool(chclient.Config{
		DialTimeout: 5 * time.Second,
		Nodes: []chclient.NodeConfig{
			{
				Name:    "clickhouse-shard1-replica1",
				Shard:   "shard1",
				Replica: "replica1",
				DSN:     "clickhouse://default@127.0.0.1:9000/default",
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, pool.Close())
	})

	collector := clusterstate.New(pool, 10*time.Second, []clusterstate.Watch{
		{Database: "movoor_dev", Table: "test_generic_network_month"},
	})

	warnings, err := collector.ValidateWatches(context.Background())
	require.ErrorContains(t, err, `movoor_dev.test_generic_network_month: engine "Distributed"`)
	require.Empty(t, warnings)
}
