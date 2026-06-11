# clickhouse-movoor

A long-running controller that observes ClickHouse MergeTree partitions across
configured physical nodes and moves cold partitions onto a configured
ClickHouse target disk.

## Architecture

- `cmd/clickhouse-movoor/main.go` — cobra CLI entrypoint; builds the logger,
  loads config, constructs and runs the app.
- `movoor.go` — application root (`App`). `New` wires dependencies, `Run`
  starts the service and blocks until the context is cancelled.
- `config.go` — `Config` loaded from YAML (`LoadConfig`) with `DefaultConfig`
  and `ResolveDefaults`.
- `internal/` — application packages (the controller's logic lives here, each
  capability in its own package).
- `web/` — operator UI. `embed.go` (build tag `webui`) embeds `web/dist`;
  `embed_stub.go` returns an empty FS otherwise. See `web/CLAUDE.md`.

New subsystems get their own package under `internal/` and are started from
`App.Run`.

## Conventions

- Go: `log/slog` for logging, `fmt.Errorf("...: %w", err)` for error wrapping,
  domain packages expose an interface + `New` constructor + lifecycle methods.
  Linting is governed by `.golangci.yml`.
- Web: see `web/CLAUDE.md`. Colors must use the semantic tokens in
  `web/src/index.css` (enforced by custom ESLint rules).
- The OpenAPI spec in `api/openapi.yaml` is the source of truth for the typed
  web API client (`pnpm --dir web generate:api`).

## Commands

```sh
make build      # binary with embedded web UI
make test       # go test -race
make lint       # golangci-lint
make audit      # lint + test + vuln + modernize + mod verify
```
