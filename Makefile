.DEFAULT_GOAL := help
.PHONY: build build-web generate lint lint-openapi fmt test clean tidy vuln modernize-check audit check-release run test/cover setup-frontend help

BUILD_DIR := ./cmd/clickhouse-movoor
BINARY := clickhouse-movoor
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

## setup-frontend: ensure web/dist/index.html exists for embed
setup-frontend:
	@mkdir -p web/dist
	@test -f web/dist/index.html || echo '<!doctype html><html><head><title>clickhouse-movoor</title></head><body><h1>clickhouse-movoor</h1></body></html>' > web/dist/index.html

## build-web: build embedded frontend assets
build-web: setup-frontend
	@if command -v pnpm >/dev/null 2>&1; then \
		echo "building embedded web assets with pnpm"; \
		cd web && pnpm build; \
	else \
		echo "pnpm not found, skipping web build and using existing web/dist assets"; \
	fi

## build: compile the binary with embedded frontend
build: setup-frontend
	go build -tags=webui -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) $(BUILD_DIR)

## generate: run go generate
generate:
	go generate ./...

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

## run: build and run the binary
run: build
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
