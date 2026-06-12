// Package api contains the OpenAPI specification and the ogen-generated
// server. openapi.yaml is the source of truth for both the Go server (rest/)
// and the typed web client (web/src/api via pnpm generate:api).
package api

//go:generate go run github.com/ogen-go/ogen/cmd/ogen@v1.20.3 --target rest --package rest --clean openapi.yaml
