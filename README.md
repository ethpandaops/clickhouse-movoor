<img align="left" height="50px" src="web/public/favicon.svg">
  <h1>clickhouse-movoor</h1>
</img>

`clickhouse-movoor` watches ClickHouse MergeTree partitions across configured
physical nodes and moves cold partitions to a configured ClickHouse disk.

It is a long-running operator with:

- an embedded UI and JSON API on `frontend.addr` (`/api/v1`)
- health and Prometheus endpoints on `healthCheckAddr` and `metricsAddr`
- optional OTLP tracing
- tiering modes: `off`, `plan`, and `enforce`

Movoor does not create ClickHouse disks or storage policies. Create them in
ClickHouse first, then set `targetDisk` to the disk name reported by
`system.parts.disk_name`.

## Getting Started

Start from the example config:

```sh
cp config.example.yaml config.yaml
$EDITOR config.yaml
make build
./clickhouse-movoor --config config.yaml
```

Open `http://localhost:8080` when `frontend.enabled` is true.

If `--config` is omitted, movoor looks for
`~/.clickhouse-movoor/config.yaml`.

Rules that matter:

- `clickhouse.nodes[]` must be physical ClickHouse servers using native
  `clickhouse://` DSNs.
- Do not use load-balanced or comma-separated failover DSNs; movoor keeps node
  identity in plans and API responses.
- A watch without `tier:` is observe-only.
- A watch with `tier:` can be planned or enforced, depending on its effective
  mode.

Minimal tiered watch:

```yaml
tiering:
  mode: plan
  interval: 5m
  defaults:
    targetDisk: s3_cache

watches:
  - database: default
    table: events_local
    tier:
      age:
        basis: partitionTime
        olderThan: 35d
```

See `config.example.yaml` for all safety limits, age modes, optimize settings,
and resplit behavior.

## Dev Fixture

Run the local 2-shard, 2-replica ClickHouse fixture:

```sh
pnpm --dir web install
docker compose -f dev/clickhouse-2s2r/docker-compose.yml up -d --wait
make build
./clickhouse-movoor --config dev/clickhouse-2s2r/movoor.config.yaml
```

Open `http://localhost:8080`.

Useful local endpoints:

- UI/API: `http://localhost:8080`
- health: `http://localhost:8081/healthz`
- metrics: `http://localhost:9090/metrics`
- MinIO console: `http://localhost:9011` (`movoor` / `movoorsecret`)

Reset the fixture:

```sh
docker compose -f dev/clickhouse-2s2r/docker-compose.yml down -v
```

## Commands

```sh
make build              # build web/dist and the embedded binary
make run                # build and run with ./config.yaml
make test               # Go tests with the race detector
make test-integration   # ClickHouse-backed integration tests
make lint               # golangci-lint
make lint-openapi       # lint api/openapi.yaml
make audit              # lint, test, vuln, modernize, mod verify
```

Frontend:

```sh
pnpm --dir web dev
pnpm --dir web generate:api
pnpm --dir web test
```

Requirements are pinned in `go.mod`, `.tool-versions`, `web/.nvmrc`, and
`web/package.json`.
