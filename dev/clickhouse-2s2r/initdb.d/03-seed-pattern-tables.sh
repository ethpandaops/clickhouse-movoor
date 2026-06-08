#!/bin/sh
set -eu

DB="${DB:-movoor_dev}"
CLUSTER="${CLUSTER:-movoor_cluster}"
QUERY_HOST="${QUERY_HOST:-clickhouse-shard1-replica1}"
SHARD_LEADERS="${SHARD_LEADERS:-clickhouse-shard1-replica1 clickhouse-shard2-replica1}"
ALL_REPLICAS="${ALL_REPLICAS:-clickhouse-shard1-replica1 clickhouse-shard1-replica2 clickhouse-shard2-replica1 clickhouse-shard2-replica2}"

STATE_TABLES="
events_local:record_id
test_generic_network_month_local:record_id
test_generic_chain_month_local:record_id
test_generic_network_block_bucket_local:record_id
test_generic_block_bucket_local:record_id
test_generic_start_block_bucket_local:record_id
test_generic_month_start_local:record_id
test_generic_month_number_local:record_id
test_generic_network_only_local:entity_id
test_generic_bucket_only_local:entity_id
test_generic_bucket_block_bucket_local:record_id
test_generic_bucket_month_start_local:record_id
test_generic_bucket_month_number_local:record_id
test_generic_plain_month_local:record_id
"

SINGLE_PARTITION_TABLES="
test_generic_tuple_version_local:entity_id
test_generic_unpartitioned_replacing_local:entity_id
test_generic_unpartitioned_plain_local:sequence
"

query() {
    clickhouse-client --host "$QUERY_HOST" --joined_subquery_requires_alias=0 --query "$1"
}

query_host() {
    host="$1"
    sql="$2"
    clickhouse-client --host "$host" --joined_subquery_requires_alias=0 --query "$sql"
}

table_from_spec() {
    printf '%s' "${1%%:*}"
}

id_col_from_spec() {
    printf '%s' "${1##*:}"
}

insert_sql() {
    table="$1"
    scenario_filter="$2"
    row_start="$3"
    row_count="$4"
    label="$5"

    case "$table" in
        events_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    network_id,
    if(network_id = 'mainnet', toUInt32(1), toUInt32(11155111)) AS chain_id,
    toUInt64((scenario_id - 1) * 100 + (row_number % 100)) AS block_number,
    base_time + toIntervalSecond(row_number) AS event_time,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', network_id, '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        tupleElement(network, 1) AS dimension_index,
        tupleElement(network, 2) AS network_id,
        toUInt64(scenario_id * 100000 + dimension_index * 10000 + row_number) AS synthetic_id,
        toDateTime('2026-01-01 00:00:00') + toIntervalMonth(scenario_id - 1) AS base_time,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT arrayJoin([(0, 'mainnet'), (1, 'sepolia')]) AS network
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_network_month_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    network_id,
    base_time + toIntervalSecond(row_number) AS event_time,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', network_id, '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        tupleElement(network, 1) AS dimension_index,
        tupleElement(network, 2) AS network_id,
        toUInt64(scenario_id * 100000 + dimension_index * 10000 + row_number) AS synthetic_id,
        toDateTime('2026-01-01 00:00:00') + toIntervalMonth(scenario_id - 1) AS base_time,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT arrayJoin([(0, 'mainnet'), (1, 'sepolia')]) AS network
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_chain_month_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    chain_id,
    base_time + toIntervalSecond(row_number) AS event_time,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', toString(chain_id), '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        tupleElement(chain, 1) AS dimension_index,
        tupleElement(chain, 2) AS chain_id,
        toUInt64(scenario_id * 100000 + dimension_index * 10000 + row_number) AS synthetic_id,
        toDateTime('2026-01-01 00:00:00') + toIntervalMonth(scenario_id - 1) AS base_time,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT arrayJoin([(0, toUInt32(1)), (1, toUInt32(11155111))]) AS chain
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_network_block_bucket_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    network_id,
    toUInt64((scenario_id - 1) * 100 + (row_number % 100)) AS block_number,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', network_id, '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        tupleElement(network, 1) AS dimension_index,
        tupleElement(network, 2) AS network_id,
        toUInt64(scenario_id * 100000 + dimension_index * 10000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT arrayJoin([(0, 'mainnet'), (1, 'sepolia')]) AS network
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_block_bucket_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    toUInt64((scenario_id - 1) * 100 + (row_number % 100)) AS block_number,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_start_block_bucket_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    toUInt64((scenario_id - 1) * 100 + (row_number % 100)) AS start_block_number,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_month_start_local|test_generic_month_number_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    base_time + toIntervalSecond(row_number) AS event_time,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        toDateTime('2026-01-01 00:00:00') + toIntervalMonth(scenario_id - 1) AS base_time,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_network_only_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    network_id,
    synthetic_id AS entity_id,
    concat('$label-', '$table', '-', network_id, '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        tupleElement(network, 1) AS scenario_id,
        tupleElement(network, 2) AS network_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([
            (1, 'mainnet'),
            (2, 'sepolia'),
            (3, 'holesky'),
            (4, 'hoodi'),
            (5, 'gnosis'),
            (6, 'arbitrum'),
            (7, 'optimism'),
            (8, 'polygon')
        ]) AS network
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_bucket_only_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    bucket_id,
    synthetic_id AS entity_id,
    concat('$label-', '$table', '-', bucket_id, '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        tupleElement(bucket, 1) AS scenario_id,
        tupleElement(bucket, 2) AS bucket_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([
            (1, 'bucket-a'),
            (2, 'bucket-b'),
            (3, 'bucket-c'),
            (4, 'bucket-d'),
            (5, 'bucket-e'),
            (6, 'bucket-f'),
            (7, 'bucket-g'),
            (8, 'bucket-h')
        ]) AS bucket
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_bucket_block_bucket_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    bucket_id,
    toUInt64((scenario_id - 1) * 100 + (row_number % 100)) AS block_number,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', bucket_id, '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        tupleElement(bucket, 1) AS dimension_index,
        tupleElement(bucket, 2) AS bucket_id,
        toUInt64(scenario_id * 100000 + dimension_index * 10000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT arrayJoin([(0, 'bucket-a'), (1, 'bucket-b')]) AS bucket
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_bucket_month_start_local|test_generic_bucket_month_number_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    bucket_id,
    base_time + toIntervalSecond(row_number) AS event_time,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', bucket_id, '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        tupleElement(bucket, 1) AS dimension_index,
        tupleElement(bucket, 2) AS bucket_id,
        toUInt64(scenario_id * 100000 + dimension_index * 10000 + row_number) AS synthetic_id,
        toDateTime('2026-01-01 00:00:00') + toIntervalMonth(scenario_id - 1) AS base_time,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT arrayJoin([(0, 'bucket-a'), (1, 'bucket-b')]) AS bucket
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_plain_month_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    network_id,
    base_time + toIntervalSecond(row_number) AS event_time,
    synthetic_id AS record_id,
    concat('$label-', '$table', '-', network_id, '-', toString(scenario_id), '-', toString(row_number)) AS payload
FROM
(
    SELECT
        scenario_id,
        tupleElement(network, 1) AS dimension_index,
        tupleElement(network, 2) AS network_id,
        toUInt64(scenario_id * 100000 + dimension_index * 10000 + row_number) AS synthetic_id,
        toDateTime('2026-01-01 00:00:00') + toIntervalMonth(scenario_id - 1) AS base_time,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT arrayJoin([(0, 'mainnet'), (1, 'sepolia')]) AS network
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_tuple_version_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    synthetic_id AS entity_id,
    concat('state-', toString(scenario_id)) AS state,
    toUInt32(row_number + 1) AS version,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_unpartitioned_replacing_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    synthetic_id AS entity_id,
    concat('$label-', '$table', '-', toString(scenario_id), '-', toString(row_number)) AS payload,
    now64(3) AS updated_at
FROM
(
    SELECT
        scenario_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        test_generic_unpartitioned_plain_local)
            cat <<SQL
INSERT INTO $DB.$table
SELECT
    synthetic_id AS sequence,
    concat('$label-', '$table', '-', toString(scenario_id), '-', toString(row_number)) AS payload
FROM
(
    SELECT
        scenario_id,
        toUInt64(scenario_id * 100000 + row_number) AS synthetic_id,
        row_number
    FROM
    (
        SELECT arrayJoin([1, 2, 3, 4, 5, 6, 7, 8]) AS scenario_id
    )
    CROSS JOIN
    (
        SELECT number + $row_start AS row_number
        FROM numbers($row_count)
    )
    WHERE scenario_id IN ($scenario_filter)
)
SQL
            ;;
        *)
            echo "unknown seed table: $table" >&2
            exit 1
            ;;
    esac
}

insert_batch_on_shards() {
    table="$1"
    scenario_filter="$2"
    row_start="$3"
    row_count="$4"
    label="$5"

    for host in $SHARD_LEADERS; do
        echo "inserting $label into $table on $host"
        query_host "$host" "$(insert_sql "$table" "$scenario_filter" "$row_start" "$row_count" "$label")"
    done
}

sync_table() {
    table="$1"

    for host in $ALL_REPLICAS; do
        query_host "$host" "SYSTEM SYNC REPLICA $DB.$table"
    done
}

sync_specs() {
    specs="$1"

    for spec in $specs; do
        sync_table "$(table_from_spec "$spec")"
    done
}

partition_ids() {
    table="$1"
    id_col="$2"
    scenario_filter="$3"

    query "
        SELECT DISTINCT _partition_id
        FROM $DB.$table
        WHERE intDiv(toUInt64($id_col), 100000) IN ($scenario_filter)
        ORDER BY _partition_id
        FORMAT TSV
    "
}

for spec in $STATE_TABLES $SINGLE_PARTITION_TABLES; do
    table="$(table_from_spec "$spec")"
    echo "truncating $table"
    query "TRUNCATE TABLE $DB.$table ON CLUSTER $CLUSTER SYNC"
done

for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    insert_batch_on_shards "$table" "1, 4, 8" "0" "120" "optimized"
    insert_batch_on_shards "$table" "2, 3, 5, 6, 7" "0" "60" "unoptimized-a"
done

sync_specs "$STATE_TABLES"

for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    id_col="$(id_col_from_spec "$spec")"

    for scenario in 1 4 8; do
        ids="$(partition_ids "$table" "$id_col" "$scenario")"
        for id in $ids; do
            echo "optimizing $table scenario $scenario partition $id"
            query "OPTIMIZE TABLE $DB.$table ON CLUSTER $CLUSTER PARTITION ID '$id' FINAL"
        done
    done

    ids="$(partition_ids "$table" "$id_col" "3")"
    for id in $ids; do
        echo "moving split first half for $table partition $id to cold"
        query "ALTER TABLE $DB.$table ON CLUSTER $CLUSTER MOVE PARTITION ID '$id' TO VOLUME 'cold'"
    done
done

for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    insert_batch_on_shards "$table" "2, 3, 5, 6, 7" "60" "60" "unoptimized-b"
done

sync_specs "$STATE_TABLES"

for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    id_col="$(id_col_from_spec "$spec")"

    for scenario in 1 2; do
        ids="$(partition_ids "$table" "$id_col" "$scenario")"
        for id in $ids; do
            echo "moving cold scenario $scenario for $table partition $id to cold"
            query "ALTER TABLE $DB.$table ON CLUSTER $CLUSTER MOVE PARTITION ID '$id' TO VOLUME 'cold'"
        done
    done
done

for spec in $SINGLE_PARTITION_TABLES; do
    table="$(table_from_spec "$spec")"
    insert_batch_on_shards "$table" "1, 2, 3, 4, 5, 6, 7, 8" "0" "60" "single-a"
    insert_batch_on_shards "$table" "1, 2, 3, 4, 5, 6, 7, 8" "60" "60" "single-b"
done

sync_specs "$SINGLE_PARTITION_TABLES"

echo "pattern table seeding complete"
