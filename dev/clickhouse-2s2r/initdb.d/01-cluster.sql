CREATE DATABASE IF NOT EXISTS movoor_dev ON CLUSTER movoor_cluster;

CREATE TABLE IF NOT EXISTS movoor_dev.events_local ON CLUSTER movoor_cluster
(
    network_id LowCardinality(String),
    chain_id UInt32,
    block_number UInt64,
    event_time DateTime,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/events_local', '{replica}', updated_at)
PARTITION BY (network_id, intDiv(block_number, 100))
ORDER BY (network_id, block_number, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.events ON CLUSTER movoor_cluster
AS movoor_dev.events_local
ENGINE = Distributed(movoor_cluster, movoor_dev, events_local, cityHash64(network_id, chain_id, block_number, record_id));
