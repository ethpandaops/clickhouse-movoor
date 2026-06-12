package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/clusterstate"
	"github.com/ethpandaops/clickhouse-movoor/internal/server"
)

func TestServerDevClickHouse2S2RReadAPI(t *testing.T) {
	if os.Getenv("MOVOOR_CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set MOVOOR_CLICKHOUSE_INTEGRATION=1 to run ClickHouse-backed integration tests")
	}

	pool, err := chclient.NewPool(chclient.Config{
		DialTimeout: 5 * time.Second,
		Nodes: []chclient.NodeConfig{
			{Name: "clickhouse-shard1-replica1", Shard: "shard1", Replica: "replica1", DSN: "clickhouse://default@127.0.0.1:9000/default"},
			{Name: "clickhouse-shard1-replica2", Shard: "shard1", Replica: "replica2", DSN: "clickhouse://default@127.0.0.1:9001/default"},
			{Name: "clickhouse-shard2-replica1", Shard: "shard2", Replica: "replica1", DSN: "clickhouse://default@127.0.0.1:9002/default"},
			{Name: "clickhouse-shard2-replica2", Shard: "shard2", Replica: "replica2", DSN: "clickhouse://default@127.0.0.1:9003/default"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })

	collector := clusterstate.New(pool, 10*time.Second, []clusterstate.Watch{
		{Database: "movoor_dev", Table: "test_generic_network_month_local"},
		{Database: "movoor_dev", Table: "test_generic_plain_month_local"},
	})

	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}
	srv := server.New(slog.New(slog.DiscardHandler), server.Config{ListenAddress: "127.0.0.1:0"}, webFS, collector)

	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, srv.Stop(stopCtx))
	})

	base := "http://" + srv.Addr()

	nodes := getAPI(t, base+"/api/v1/nodes")
	requireCollection(t, nodes)
	require.Len(t, nodes["items"], 4)

	disks := getAPI(t, base+"/api/v1/storage/disks")
	requireCollection(t, disks)
	require.Len(t, disks["items"], 12)
	requireDiskEvidence(t, disks)

	tables := getAPI(t, base+"/api/v1/tables")
	requireCollection(t, tables)
	require.Len(t, tables["items"], 2)

	columns := getAPI(t, base+"/api/v1/tables/movoor_dev/test_generic_network_month_local/columns")
	requireCollection(t, columns)
	require.Len(t, columns["items"], 4)

	partitions := getAPI(t, base+"/api/v1/tables/movoor_dev/test_generic_network_month_local/partitions")
	requireCollection(t, partitions)
	require.NotEmpty(t, partitions["items"])

	parts := getAPI(t, base+"/api/v1/tables/movoor_dev/test_generic_network_month_local/parts?active=true")
	requireCollection(t, parts)
	require.NotEmpty(t, parts["items"])

	for _, path := range []string{
		"/api/v1/tables/movoor_dev/test_generic_network_month_local/detached-parts",
		"/api/v1/operations",
		"/api/v1/operations/mutations",
		"/api/v1/operations/replication-queue",
		"/api/v1/part-events",
		"/api/v1/conditions",
	} {
		response := getAPI(t, base+path)
		requireCollection(t, response)
		require.NotNil(t, response["items"])
	}
}

func getAPI(t *testing.T, url string) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	return decoded
}

func requireCollection(t *testing.T, response map[string]any) {
	t.Helper()

	collection, ok := response["collection"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 4, jsonNumberInt(t, collection, "nodesResponded"))
	require.Equal(t, 0, jsonNumberInt(t, collection, "nodesFailed"))
}

func requireDiskEvidence(t *testing.T, response map[string]any) {
	t.Helper()

	seenByDisk := make(map[string]int)
	items, itemsOK := response["items"].([]any)
	require.True(t, itemsOK)

	for _, raw := range items {
		item, itemOK := raw.(map[string]any)
		require.True(t, itemOK)

		disk, diskOK := item["disk"].(string)
		require.True(t, diskOK)
		seenByDisk[disk]++
		if disk == "s3_cache" {
			require.Equal(t, false, item["capacityKnown"])
			require.Nil(t, item["freeSpaceBytes"])
			require.Nil(t, item["totalSpaceBytes"])
			require.Nil(t, item["unreservedSpaceBytes"])
		}
	}

	require.Equal(t, 4, seenByDisk["default"])
	require.Equal(t, 4, seenByDisk["s3"])
	require.Equal(t, 4, seenByDisk["s3_cache"])
}

func jsonNumberInt(t *testing.T, item map[string]any, key string) int {
	t.Helper()

	value, ok := item[key].(float64)
	require.True(t, ok)

	return int(value)
}
