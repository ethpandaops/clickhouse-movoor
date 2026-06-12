#!/bin/sh
set -eu

DB="${DB:-movoor_dev}"
QUERY_HOST="${QUERY_HOST:-clickhouse-shard1-replica1}"
REPLICA1_HOSTS="${REPLICA1_HOSTS:-clickhouse-shard1-replica1 clickhouse-shard2-replica1}"

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

query() {
    clickhouse-client --host "$QUERY_HOST" --query "$1"
}

query_host() {
    host="$1"
    sql="$2"
    clickhouse-client --host "$host" --query "$sql"
}

table_from_spec() {
    printf '%s' "${1%%:*}"
}

id_col_from_spec() {
    printf '%s' "${1##*:}"
}

partition_ids() {
    table="$1"
    id_col="$2"

    query "
        SELECT DISTINCT _partition_id
        FROM $DB.$table
        WHERE intDiv(toUInt64($id_col), 100000) = 8
        ORDER BY _partition_id
        FORMAT TSV
    "
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

echo "waiting for replica-divergent partitions to replicate"
for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    id_col="$(id_col_from_spec "$spec")"
    ids="$(partition_ids "$table" "$id_col")"
    id_list="$(sql_string_list "$ids")"
    expected=$(( $(printf '%s\n' "$ids" | sed '/^$/d' | wc -l) * 4 ))

    attempts=0
    while :; do
        replicated="$(query "
SELECT count()
FROM
(
    SELECT
        hostName() AS host,
        partition_id,
        count() AS active_parts,
        sum(rows) AS rows
    FROM clusterAllReplicas('movoor_cluster', system.parts)
    WHERE database = '$DB'
      AND table = '$table'
      AND active
      AND partition_id IN ($id_list)
    GROUP BY host, partition_id
    HAVING active_parts = 1
       AND rows = 120
)
")"

        if [ "$replicated" = "$expected" ]; then
            break
        fi

        attempts=$((attempts + 1))
        if [ "$attempts" -ge 120 ]; then
            echo "timed out waiting for $table replica-divergent partitions; got $replicated/$expected" >&2
            exit 1
        fi

        sleep 1
    done
done

echo "moving replica1 partitions to cold"
for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    id_col="$(id_col_from_spec "$spec")"
    ids="$(partition_ids "$table" "$id_col")"

    for host in $REPLICA1_HOSTS; do
        for id in $ids; do
            query_host "$host" "
                ALTER TABLE $DB.$table
                MOVE PARTITION ID '$id'
                TO VOLUME 'cold'
            "
        done
    done
done

echo "checking replica-divergent disk layout"
for spec in $STATE_TABLES; do
    table="$(table_from_spec "$spec")"
    id_col="$(id_col_from_spec "$spec")"
    ids="$(partition_ids "$table" "$id_col")"
    id_list="$(sql_string_list "$ids")"
    expected=$(( $(printf '%s\n' "$ids" | sed '/^$/d' | wc -l) * 4 ))

    layout="$(query "
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
    WHERE database = '$DB'
      AND table = '$table'
      AND active
      AND partition_id IN ($id_list)
    GROUP BY host, partition_id, disk_name
    HAVING active_parts = 1
       AND rows = 120
       AND (
           (host IN ('clickhouse-shard1-replica1', 'clickhouse-shard2-replica1') AND disk_name = 's3_cache')
           OR (host IN ('clickhouse-shard1-replica2', 'clickhouse-shard2-replica2') AND disk_name = 'default')
       )
)
")"

    if [ "$layout" != "$expected" ]; then
        echo "unexpected $table replica-divergent disk layout; got $layout/$expected" >&2
        exit 1
    fi
done
