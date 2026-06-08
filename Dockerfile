# ---- Web build stage ----
FROM node:24-bookworm-slim AS web

WORKDIR /web

RUN corepack enable

COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build

# ---- Go build stage ----
FROM golang:1.26.4 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .
COPY --from=web /web/dist ./web/dist

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -tags=webui -trimpath -ldflags='-s -w' \
        -o /clickhouse-movoor ./cmd/clickhouse-movoor

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /clickhouse-movoor /usr/local/bin/clickhouse-movoor

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/clickhouse-movoor"]
