# ClickHouse 2 Shards, 2 Replicas

Local development cluster for movoor:

- 4 ClickHouse server containers, two replicas per shard.
- 3 ClickHouse Keeper containers for replicated tables and distributed DDL.
- 1 MinIO container used as the S3-compatible object store.
- A `movoor_tiered` storage policy with `default` as hot storage and `s3_cache` as cold storage over an `s3` object disk.

ClickHouse image defaults are pinned to the refined production cluster version
this fixture is validated against. Image defaults can still be overridden with
`CLICKHOUSE_IMAGE`, `CLICKHOUSE_KEEPER_IMAGE`, `MINIO_IMAGE`, and
`MINIO_MC_IMAGE`.

ClickHouse exposes three disks in this fixture:

- `default`: local hot storage
- `s3`: MinIO-backed object storage, bucket `movoor-clickhouse`, prefix `clickhouse/`
- `s3_cache`: local filesystem cache over the `s3` disk, used by the cold volume

ClickHouse reports moved cold parts with `system.parts.disk_name = 's3_cache'`. The
cache layer is visible through `SHOW FILESYSTEM CACHES` / `DESCRIBE FILESYSTEM
CACHE 's3_cache'`.

## Start

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml up -d --wait
```

The `verify` service runs after bootstrap and becomes healthy only after the
fixture assertions pass. This makes `up -d --wait` return only after ClickHouse
is started, schema bootstrap is complete, seed states are present, and the
expected disk/part layout is visible in `system.parts`.

Run movoor against this fixture:

```sh
./clickhouse-movoor --config dev/clickhouse-2s2r/movoor.config.yaml
```

Ports:

- Shard 1 replica 1: HTTP `8123`, native `9000`
- Shard 1 replica 2: HTTP `8124`, native `9001`
- Shard 2 replica 1: HTTP `8125`, native `9002`
- Shard 2 replica 2: HTTP `8126`, native `9003`
- MinIO: API `9010`, console `9011` (`movoor` / `movoorsecret`)

Bootstrap creates:

- `movoor_dev.events_local` on each shard
- `movoor_dev.events` as a distributed table across both shards
- generic `movoor_dev.test_generic_*_local` tables covering the partition patterns movoor needs to handle
- matching distributed `movoor_dev.test_generic_*` tables for discovery/API tests
- deterministic seed data in every watched local table

Pattern tables cover:

- tuple partitions: `(network_id, toYYYYMM(event_time))`, `(chain_id, toYYYYMM(event_time))`, and `(network_id, intDiv(block_number, 100))`
- bare partitions: `toStartOfMonth(event_time)`, `toYYYYMM(event_time)`, and `intDiv(block_number, 100)`
- alternate block columns: `intDiv(start_block_number, 100)`
- string/bucket partitions: `network_id`, `bucket_id`, `(bucket_id, intDiv(block_number, 100))`, `(bucket_id, toStartOfMonth(event_time))`, and `(bucket_id, toYYYYMM(event_time))`
- explicit `PARTITION BY tuple()`, unpartitioned tables, plain `ReplicatedMergeTree`, and `ReplacingMergeTree` with both timestamp and integer version columns

Seeded partition states:

- Every watched partitioned table has deterministic rows on both shards and both replicas.
- Scenario 1: cold optimized, 1 active part per replica, moved to the cold volume.
- Scenario 2: cold unoptimized, 2 active parts per replica, moved to the cold volume.
- Scenario 3: split hot/cold, 1 active part per replica on the cold volume and 1 active part per replica on the hot volume.
- Scenario 4: hot optimized, 1 active part per replica, left on the hot volume.
- Scenarios 5, 6, and 7: hot unoptimized buffer partitions, 2 active parts per replica, left on the hot volume.
- Scenario 8: replica-divergent, 1 active part per replica; `replica1` on each shard is cold and `replica2` on each shard is hot.
- Each stateful partition has 120 rows per replica.
- The `tuple()` and unpartitioned tables cannot represent multiple logical partition states, so they are seeded as two active default-disk parts per replica.
- Watched tables set `max_bytes_to_merge_at_max_space_in_pool = 0`, which disables automatic background merges for this fixture. Optimized partitions are still forced to one part with partition-scoped `OPTIMIZE ... FINAL`.
- Because the cold volume uses `s3_cache`, ClickHouse reports cold parts as `system.parts.disk_name = 's3_cache'`.

## Smoke Test

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml exec clickhouse-shard1-replica1 \
  clickhouse-client --query "
    SELECT cluster, shard_num, replica_num, host_name
    FROM system.clusters
    WHERE cluster = 'movoor_cluster'
    ORDER BY shard_num, replica_num
  "
```

Insert sample data through the distributed table:

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml exec clickhouse-shard1-replica1 \
  clickhouse-client --query "
    INSERT INTO movoor_dev.events
    SELECT
      if(number % 2 = 0, 'mainnet', 'sepolia'),
      1,
      number,
      now() - toIntervalSecond(number),
      number,
      concat('payload-', toString(number)),
      now64(3)
    FROM numbers(10000)
  "
```

Check placement and partition IDs:

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml exec clickhouse-shard1-replica1 \
  clickhouse-client --query "
    SELECT partition, partition_id, disk_name, count() AS parts
    FROM system.parts
    WHERE database = 'movoor_dev'
      AND table = 'events_local'
      AND active
    GROUP BY partition, partition_id, disk_name
    ORDER BY partition_id, disk_name
  "
```

Check seeded partition states:

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml exec clickhouse-shard1-replica1 \
  clickhouse-client --query "
    SELECT
      hostName() AS host,
      partition,
      disk_name,
      count() AS active_parts,
      sum(rows) AS rows
    FROM clusterAllReplicas('movoor_cluster', system.parts)
    WHERE database = 'movoor_dev'
      AND table = 'test_generic_network_month_local'
      AND active
    GROUP BY host, partition, disk_name
    ORDER BY host, partition
  "
```

Move a local partition to the cold volume on shard 1:

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml exec clickhouse-shard1-replica1 \
  clickhouse-client --query "
    ALTER TABLE movoor_dev.events_local
    MOVE PARTITION ('mainnet', 0)
    TO VOLUME 'cold'
  "
```

Move it back to the hot volume:

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml exec clickhouse-shard1-replica1 \
  clickhouse-client --query "
    ALTER TABLE movoor_dev.events_local
    MOVE PARTITION ('mainnet', 0)
    TO VOLUME 'hot'
  "
```

## Reset

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml down -v
```
