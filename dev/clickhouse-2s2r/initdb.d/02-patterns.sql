CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_network_month_local ON CLUSTER movoor_cluster
(
    network_id LowCardinality(String),
    event_time DateTime,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_network_month_local', '{replica}', updated_at)
PARTITION BY (network_id, toYYYYMM(event_time))
ORDER BY (network_id, event_time, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_network_month ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_network_month_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_network_month_local, cityHash64(network_id, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_chain_month_local ON CLUSTER movoor_cluster
(
    chain_id UInt32,
    event_time DateTime,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_chain_month_local', '{replica}', updated_at)
PARTITION BY (chain_id, toYYYYMM(event_time))
ORDER BY (chain_id, event_time, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_chain_month ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_chain_month_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_chain_month_local, cityHash64(chain_id, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_network_block_bucket_local ON CLUSTER movoor_cluster
(
    network_id LowCardinality(String),
    block_number UInt64,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_network_block_bucket_local', '{replica}', updated_at)
PARTITION BY (network_id, intDiv(block_number, 100))
ORDER BY (network_id, block_number, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_network_block_bucket ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_network_block_bucket_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_network_block_bucket_local, cityHash64(network_id, block_number, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_block_bucket_local ON CLUSTER movoor_cluster
(
    block_number UInt64,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_block_bucket_local', '{replica}', updated_at)
PARTITION BY intDiv(block_number, 100)
ORDER BY (block_number, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_block_bucket ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_block_bucket_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_block_bucket_local, cityHash64(block_number, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_start_block_bucket_local ON CLUSTER movoor_cluster
(
    start_block_number UInt64,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_start_block_bucket_local', '{replica}', updated_at)
PARTITION BY intDiv(start_block_number, 100)
ORDER BY (start_block_number, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_start_block_bucket ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_start_block_bucket_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_start_block_bucket_local, cityHash64(start_block_number, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_month_start_local ON CLUSTER movoor_cluster
(
    event_time DateTime,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_month_start_local', '{replica}', updated_at)
PARTITION BY toStartOfMonth(event_time)
ORDER BY (event_time, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_month_start ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_month_start_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_month_start_local, cityHash64(record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_month_number_local ON CLUSTER movoor_cluster
(
    event_time DateTime,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_month_number_local', '{replica}', updated_at)
PARTITION BY toYYYYMM(event_time)
ORDER BY (event_time, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_month_number ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_month_number_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_month_number_local, cityHash64(record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_network_only_local ON CLUSTER movoor_cluster
(
    network_id LowCardinality(String),
    entity_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_network_only_local', '{replica}', updated_at)
PARTITION BY network_id
ORDER BY (network_id, entity_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_network_only ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_network_only_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_network_only_local, cityHash64(network_id, entity_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_only_local ON CLUSTER movoor_cluster
(
    bucket_id LowCardinality(String),
    entity_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_bucket_only_local', '{replica}', updated_at)
PARTITION BY bucket_id
ORDER BY (bucket_id, entity_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_only ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_bucket_only_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_bucket_only_local, cityHash64(bucket_id, entity_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_block_bucket_local ON CLUSTER movoor_cluster
(
    bucket_id LowCardinality(String),
    block_number UInt64,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_bucket_block_bucket_local', '{replica}', updated_at)
PARTITION BY (bucket_id, intDiv(block_number, 100))
ORDER BY (bucket_id, block_number, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_block_bucket ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_bucket_block_bucket_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_bucket_block_bucket_local, cityHash64(bucket_id, block_number, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_month_start_local ON CLUSTER movoor_cluster
(
    bucket_id LowCardinality(String),
    event_time DateTime,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_bucket_month_start_local', '{replica}', updated_at)
PARTITION BY (bucket_id, toStartOfMonth(event_time))
ORDER BY (bucket_id, event_time, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_month_start ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_bucket_month_start_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_bucket_month_start_local, cityHash64(bucket_id, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_month_number_local ON CLUSTER movoor_cluster
(
    bucket_id LowCardinality(String),
    event_time DateTime,
    record_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_bucket_month_number_local', '{replica}', updated_at)
PARTITION BY (bucket_id, toYYYYMM(event_time))
ORDER BY (bucket_id, event_time, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_bucket_month_number ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_bucket_month_number_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_bucket_month_number_local, cityHash64(bucket_id, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_tuple_version_local ON CLUSTER movoor_cluster
(
    entity_id UInt64,
    state LowCardinality(String),
    version UInt32,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_tuple_version_local', '{replica}', version)
PARTITION BY tuple()
ORDER BY (entity_id, state)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_tuple_version ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_tuple_version_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_tuple_version_local, cityHash64(entity_id, state));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_unpartitioned_replacing_local ON CLUSTER movoor_cluster
(
    entity_id UInt64,
    payload String,
    updated_at DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_unpartitioned_replacing_local', '{replica}', updated_at)
ORDER BY entity_id
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_unpartitioned_replacing ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_unpartitioned_replacing_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_unpartitioned_replacing_local, cityHash64(entity_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_plain_month_local ON CLUSTER movoor_cluster
(
    network_id LowCardinality(String),
    event_time DateTime,
    record_id UInt64,
    payload String
)
ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_plain_month_local', '{replica}')
PARTITION BY (network_id, toYYYYMM(event_time))
ORDER BY (network_id, event_time, record_id)
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_plain_month ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_plain_month_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_plain_month_local, cityHash64(network_id, record_id));

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_unpartitioned_plain_local ON CLUSTER movoor_cluster
(
    sequence UInt64,
    payload String
)
ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/movoor_dev/test_generic_unpartitioned_plain_local', '{replica}')
ORDER BY sequence
SETTINGS
    storage_policy = 'movoor_tiered',
    max_bytes_to_merge_at_max_space_in_pool = 0;

CREATE TABLE IF NOT EXISTS movoor_dev.test_generic_unpartitioned_plain ON CLUSTER movoor_cluster
AS movoor_dev.test_generic_unpartitioned_plain_local
ENGINE = Distributed(movoor_cluster, movoor_dev, test_generic_unpartitioned_plain_local, cityHash64(sequence));
