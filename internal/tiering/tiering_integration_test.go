package tiering_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/clickhouse-movoor/internal/chclient"
	"github.com/ethpandaops/clickhouse-movoor/internal/tiering"
)

func TestTieringExecutorDevClickHouse2S2R(t *testing.T) {
	if os.Getenv("MOVOOR_CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set MOVOOR_CLICKHOUSE_INTEGRATION=1 with dev/clickhouse-2s2r running")
	}

	pool, err := chclient.NewPool(chclient.Config{
		DialTimeout:  5 * time.Second,
		QueryTimeout: 30 * time.Second,
		Nodes: []chclient.NodeConfig{{
			Name:    "clickhouse-shard1-replica1",
			Shard:   "shard1",
			Replica: "replica1",
			DSN:     "clickhouse://default@127.0.0.1:9000/default",
		}},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, pool.Close())
	})
	client := pool.Clients()[0]
	ctx := context.Background()
	table := fmt.Sprintf("tiering_executor_%d", time.Now().UnixNano())

	_, err = client.DB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS movoor_it")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = client.DB.ExecContext(context.Background(), "DROP TABLE IF EXISTS movoor_it."+table)
	})
	_, err = client.DB.ExecContext(ctx, `
		CREATE TABLE movoor_it.`+table+` (
			network_id String,
			block_number UInt64,
			payload String
		)
		ENGINE = MergeTree
		PARTITION BY (network_id, intDiv(block_number, 100))
		ORDER BY (network_id, block_number)
		SETTINGS storage_policy = 'movoor_tiered'
	`)
	require.NoError(t, err)
	//nolint:gosec // Integration test table names are generated locally and quoted elsewhere in production paths.
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO movoor_it.`+table+`
		SELECT 'mainnet', number, concat('old-', toString(number)) FROM numbers(10)
		UNION ALL
		SELECT 'mainnet', number + 350, concat('new-', toString(number)) FROM numbers(10)
	`)
	require.NoError(t, err)
	require.NoError(t, waitForParts(ctx, client, "movoor_it", table, 2))
	time.Sleep(20 * time.Millisecond)

	settings := tiering.DefaultTierSettings()
	settings.Mode = tiering.ModeEnforce
	settings.QuietFor = tiering.Duration{Duration: time.Millisecond}
	settings.TierFrozenAfter = tiering.Duration{Duration: time.Millisecond}
	settings.OptimizeStallAfter = tiering.Duration{Duration: 5 * time.Second}
	settings.Age = tiering.AgeSettings{Basis: tiering.AgeBasisFrontier, Field: "block_number", KeepLast: 50}

	observer := tiering.NewSQLObserver(10 * time.Second)
	watch := tiering.EffectiveWatch{Database: "movoor_it", Table: table, Settings: &settings}
	obs, err := observer.ObserveTable(ctx, client, watch)
	require.NoError(t, err)
	require.Equal(t, "26.2.5.45", obs.Version)
	require.True(t, obs.TargetDiskFound)
	require.Equal(t, "hot", obs.HotVolume)

	var verdict tiering.Verdict
	for _, item := range tiering.DecideTable(obs, time.Now().UTC()) {
		if item.Decision == tiering.DecisionTier {
			verdict = item
			break
		}
	}
	require.Equal(t, tiering.DecisionTier, verdict.Decision)

	store := tiering.NewStore(10)
	executor := tiering.NewExecutor(slog.New(slog.DiscardHandler), store, observer, "integration")
	executor.PollInterval = 50 * time.Millisecond
	entry := executor.Apply(ctx, client, obs, verdict)
	require.Equal(t, "success", entry.Outcome, entry.Error)

	refreshed, err := observer.RefreshPartition(ctx, client, obs, verdict.PartitionID)
	require.NoError(t, err)
	require.Len(t, refreshed.Disks, 1)
	require.Equal(t, "s3_cache", refreshed.Disks[0].Disk)
	require.Len(t, store.History(), 1)
}

func waitForParts(ctx context.Context, client chclient.Client, database string, table string, wantPartitions int) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var partitions int
		if err := client.DB.QueryRowContext(ctx, `
			SELECT uniqExact(partition_id)
			FROM system.parts
			WHERE database = ? AND table = ? AND active
		`, database, table).Scan(&partitions); err != nil {
			return err
		}
		if partitions >= wantPartitions {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %d active partitions in %s.%s", wantPartitions, database, table)
}
