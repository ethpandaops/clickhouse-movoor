#!/bin/sh
set -eu

CLICKHOUSE_HOST="${CLICKHOUSE_HOST:-clickhouse-shard1-replica1}"
CLICKHOUSE_VERSION="${CLICKHOUSE_VERSION:-26.2.5.45}"

query() {
    clickhouse-client --host "$CLICKHOUSE_HOST" --query "$1"
}

assert_equals() {
    expected="$1"
    name="$2"
    sql="$3"
    actual="$(query "$sql")"
    if [ "$actual" != "$expected" ]; then
        echo "verify failed: $name: expected $expected, got $actual" >&2
        exit 1
    fi
    echo "ok: $name"
}

until query "SELECT 1" >/dev/null 2>&1; do
    sleep 1
done

assert_equals "4" "ClickHouse version on all replicas" "$(cat <<SQL
SELECT count()
FROM clusterAllReplicas('movoor_cluster', system.one)
WHERE version() = '$CLICKHOUSE_VERSION'
SQL
)"

assert_equals "4" "movoor_cluster topology" "$(cat <<'SQL'
SELECT count()
FROM system.clusters
WHERE cluster = 'movoor_cluster'
  AND (
      (shard_num = 1 AND replica_num = 1 AND host_name = 'clickhouse-shard1-replica1')
      OR (shard_num = 1 AND replica_num = 2 AND host_name = 'clickhouse-shard1-replica2')
      OR (shard_num = 2 AND replica_num = 1 AND host_name = 'clickhouse-shard2-replica1')
      OR (shard_num = 2 AND replica_num = 2 AND host_name = 'clickhouse-shard2-replica2')
  )
SQL
)"

assert_equals "4" "disk configuration on all replicas" "$(cat <<'SQL'
SELECT count()
FROM
(
    SELECT hostName() AS host
    FROM clusterAllReplicas('movoor_cluster', system.disks)
    GROUP BY host
    HAVING countIf(name = 'default') = 1
       AND countIf(name = 's3') = 1
       AND countIf(name = 's3_cache') = 1
)
SQL
)"

assert_equals "4" "storage policy on all replicas" "$(cat <<'SQL'
SELECT count()
FROM
(
    SELECT hostName() AS host
    FROM clusterAllReplicas('movoor_cluster', system.storage_policies)
    WHERE policy_name = 'movoor_tiered'
    GROUP BY host
    HAVING countIf(volume_name = 'hot' AND has(disks, 'default')) = 1
       AND countIf(volume_name = 'cold' AND has(disks, 's3_cache')) = 1
)
SQL
)"

LOCAL_TABLE_LIST="'events_local', 'test_generic_network_month_local', 'test_generic_chain_month_local', 'test_generic_network_block_bucket_local', 'test_generic_block_bucket_local', 'test_generic_start_block_bucket_local', 'test_generic_month_start_local', 'test_generic_month_number_local', 'test_generic_network_only_local', 'test_generic_bucket_only_local', 'test_generic_bucket_block_bucket_local', 'test_generic_bucket_month_start_local', 'test_generic_bucket_month_number_local', 'test_generic_tuple_version_local', 'test_generic_unpartitioned_replacing_local', 'test_generic_plain_month_local', 'test_generic_unpartitioned_plain_local', 'test_generic_logdate_local'"

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
test_generic_logdate_local:record_id
"

SINGLE_PARTITION_TABLES="
test_generic_tuple_version_local:entity_id
test_generic_unpartitioned_replacing_local:entity_id
test_generic_unpartitioned_plain_local:sequence
"

table_from_spec() {
    printf '%s' "${1%%:*}"
}

id_col_from_spec() {
    printf '%s' "${1##*:}"
}

sql_string_list() {
    first=1
    for value in $1; do
        if [ "$first" -eq 0 ]; then
            printf ', '
        fi
        first=0
        printf "'%s'" "$value"
    done
}

partition_ids() {
    table="$1"
    id_col="$2"
    scenario="$3"

    query "
        SELECT DISTINCT _partition_id
        FROM movoor_dev.$table
        WHERE intDiv(toUInt64($id_col), 100000) = $scenario
        ORDER BY _partition_id
        FORMAT TSV
    "
}

assert_state_layout() {
    table="$1"
    id_col="$2"
    scenario="$3"
    name="$4"
    expected_per_partition="$5"
    having="$6"

    ids="$(partition_ids "$table" "$id_col" "$scenario")"
    id_count="$(printf '%s\n' "$ids" | sed '/^$/d' | wc -l | tr -d ' ')"
    if [ "$id_count" = "0" ]; then
        echo "verify failed: $table $name: no scenario $scenario partition ids" >&2
        exit 1
    fi

    id_list="$(sql_string_list "$ids")"
    expected=$((id_count * expected_per_partition))

    assert_equals "$expected" "$table $name" "
SELECT count()
FROM
(
    SELECT
        hostName() AS host,
        partition_id,
        disk_name,
        count() AS active_parts,
        sum(rows) AS rows
    FROM clusterAllReplicas('movoor_cluster', system.parts)
    WHERE database = 'movoor_dev'
      AND table = '$table'
      AND active
      AND partition_id IN ($id_list)
    GROUP BY host, partition_id, disk_name
    HAVING $having
)
"
}

assert_equals "0" "dedicated seed tables removed" "$(cat <<'SQL'
SELECT count()
FROM system.tables
WHERE database = 'movoor_dev'
  AND startsWith(name, 'seed_')
SQL
)"

assert_equals "72" "watched local tables on all replicas" "
SELECT count()
FROM
(
    SELECT
        table,
        hostName() AS host,
        sum(rows) AS rows
    FROM clusterAllReplicas('movoor_cluster', system.parts)
    WHERE database = 'movoor_dev'
      AND table IN ($LOCAL_TABLE_LIST)
      AND active
    GROUP BY table, host
    HAVING rows > 0
)
"

for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    id_col="$(id_col_from_spec "$spec")"

    assert_state_layout "$table" "$id_col" "1" "cold optimized" "4" \
        "disk_name = 's3_cache' AND active_parts = 1 AND rows = 120"
    assert_state_layout "$table" "$id_col" "2" "cold unoptimized" "4" \
        "disk_name = 's3_cache' AND active_parts = 2 AND rows = 120"
    assert_state_layout "$table" "$id_col" "3" "split hot/cold" "8" \
        "disk_name IN ('default', 's3_cache') AND active_parts = 1 AND rows = 60"
    assert_state_layout "$table" "$id_col" "4" "hot optimized" "4" \
        "disk_name = 'default' AND active_parts = 1 AND rows = 120"

    for scenario in 5 6 7; do
        assert_state_layout "$table" "$id_col" "$scenario" "hot unoptimized buffer $scenario" "4" \
            "disk_name = 'default' AND active_parts = 2 AND rows = 120"
    done

    assert_state_layout "$table" "$id_col" "8" "replica divergent" "4" \
        "active_parts = 1 AND rows = 120 AND ((host IN ('clickhouse-shard1-replica1', 'clickhouse-shard2-replica1') AND disk_name = 's3_cache') OR (host IN ('clickhouse-shard1-replica2', 'clickhouse-shard2-replica2') AND disk_name = 'default'))"
done

for spec in $SINGLE_PARTITION_TABLES; do
    table="$(table_from_spec "$spec")"

    assert_equals "4" "$table single partition fixture" "
SELECT count()
FROM
(
    SELECT
        hostName() AS host,
        disk_name,
        count() AS active_parts,
        sum(rows) AS rows
    FROM clusterAllReplicas('movoor_cluster', system.parts)
    WHERE database = 'movoor_dev'
      AND table = '$table'
      AND active
    GROUP BY host, disk_name
    HAVING disk_name = 'default'
       AND active_parts = 2
       AND rows = 960
)
"
done

echo "verify passed"
