# Contributing to clickhouse-movoor

## Prerequisites

- [Go](https://go.dev/dl/) (see `go.mod` for the minimum version)
- [Node.js](https://nodejs.org/) + [pnpm](https://pnpm.io/) (for the web UI)
- [golangci-lint](https://golangci-lint.run/welcome/install/)
- [goreleaser](https://goreleaser.com/install/) (optional, for release checks)

## Development Workflow

```sh
# Build
make build

# Run tests
make test

# Run linter
make lint

# Run all checks
make audit
```

## Pull Request Guidelines

1. Create a feature branch from `master`.
2. Keep changes focused — one concern per PR.
3. Add tests for new functionality.
4. Ensure `make audit` passes before submitting.
5. Use [Conventional Commits](https://www.conventionalcommits.org/) — the web
   package enforces them via commitlint.

## Code Style

- Follow the conventions enforced by `golangci-lint` and `.golangci.yml`.
- Use `log/slog` for structured logging.
- Wrap errors with context using `fmt.Errorf` and `%w`.
