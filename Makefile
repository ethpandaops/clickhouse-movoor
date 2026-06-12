.DEFAULT_GOAL := help
.PHONY: build build-web build-go generate lint lint-openapi fmt test test-integration clean tidy vuln modernize-check audit check-release run test/cover help

BUILD_DIR := ./cmd/clickhouse-movoor
BINARY := clickhouse-movoor
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

## build-web: build embedded frontend assets
build-web:
	@if ! command -v pnpm >/dev/null 2>&1; then \
		echo "pnpm not found; install pnpm to build embedded web assets" >&2; \
		exit 1; \
	fi
	cd web && pnpm build

## build: build frontend assets and compile the binary with embedded frontend
build: build-web
	go build -tags=webui -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) $(BUILD_DIR)

## build-go: compile the binary without embedded frontend assets (API only)
build-go:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) $(BUILD_DIR)

## generate: run go generate and regenerate the typed web client from api/openapi.yaml
generate:
	go generate ./...
	@if ! command -v pnpm >/dev/null 2>&1; then \
		echo "pnpm not found; install pnpm to regenerate the web API client" >&2; \
		exit 1; \
	fi
	cd web && pnpm generate:api

## lint: run golangci-lint
lint:
	@if git rev-parse --verify --quiet origin/master >/dev/null; then \
		golangci-lint run --new-from-rev="origin/master" ./...; \
	else \
		golangci-lint run ./...; \
	fi

## lint-openapi: lint OpenAPI spec with Redocly
lint-openapi:
	npx -y @redocly/cli@latest lint api/openapi.yaml --config .redocly.yaml

## fmt: format code
fmt:
	golangci-lint fmt ./...

## test: run tests with race detector
test:
	go test -race -shuffle=on -coverprofile=coverage.out -covermode=atomic ./...

## test-integration: run ClickHouse-backed integration tests
test-integration:
	docker compose -f dev/clickhouse-2s2r/docker-compose.yml up -d --wait
	MOVOOR_CLICKHOUSE_INTEGRATION=1 go test ./internal/clusterstate ./internal/server ./internal/tiering -count=1 -v

## clean: remove build outputs
clean:
	rm -f $(BINARY) coverage.out

## tidy: tidy go modules
tidy:
	go mod tidy

## vuln: run govulncheck
vuln:
	go tool govulncheck ./...

## modernize-check: preview Go modernizations without changing files
modernize-check:
	go fix -n ./...

## audit: run all checks
audit: lint test vuln modernize-check
	go mod tidy -diff
	go mod verify

## run: build Go only (no frontend; pair with the Vite dev server) and run with config.yaml
run: build-go
	./$(BINARY) --config config.yaml

## test/cover: open HTML coverage report
test/cover: test
	go tool cover -html=coverage.out

## check-release: validate goreleaser config
check-release:
	goreleaser check -q

## help: show this help
help:
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'
