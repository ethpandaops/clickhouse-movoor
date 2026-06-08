# clickhouse-movoor

A long-running controller that continuously tiers **cold ClickHouse partitions**
from local disk to object storage (S3), keeping recent ("hot") data local. It
runs as one instance per node, reconciling that node's own local ClickHouse.

> **Status:** early stage. The project scaffolding — Go service, operator web
> UI, CI, and release pipeline — is in place; the tiering logic is being built
> out.

## Layout

```
.
├── cmd/clickhouse-movoor/   # CLI entrypoint (cobra)
├── internal/                # application packages
├── web/                     # operator web UI (React + Vite, embedded in the binary)
├── api/openapi.yaml         # HTTP API spec (source for the web API client)
├── config.go / config.yaml  # configuration
├── movoor.go                # application root that wires everything together
└── .github/workflows/       # CI + release pipelines
```

## Prerequisites

- [Go](https://go.dev/dl/) (see `go.mod`)
- [Node.js](https://nodejs.org/) + [pnpm](https://pnpm.io/) (for the web UI)
- [golangci-lint](https://golangci-lint.run/) (for `make lint`)

Tool versions are pinned in `.tool-versions` (mise/asdf) and `web/.nvmrc`.

## Quick start

```sh
# Build the web assets and the binary (embeds web/dist)
make build-web
make build

# Run it
./clickhouse-movoor --config config.yaml
```

Without `make build-web`, `make build` embeds a placeholder page so the binary
still runs.

### Frontend dev server

```sh
cd web
pnpm install
pnpm dev          # Vite dev server with /api proxied to the local backend
pnpm generate:api # regenerate the typed API client from ../api/openapi.yaml
```

## Common tasks

| Command          | Description                                  |
| ---------------- | -------------------------------------------- |
| `make build`     | Compile the binary with the embedded web UI  |
| `make test`      | Run Go tests with the race detector          |
| `make lint`      | Run golangci-lint                            |
| `make audit`     | lint + test + vuln + modernize + mod verify  |
| `make run`       | Build and run with `config.yaml`             |
| `make help`      | List all targets                             |
