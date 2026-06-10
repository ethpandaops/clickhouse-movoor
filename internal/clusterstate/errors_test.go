package clusterstate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"
)

func TestIsReachabilityError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "context deadline is a query error",
			err:  fmt.Errorf("query: %w", context.DeadlineExceeded),
			want: false,
		},
		{
			name: "server exception means reachable",
			err:  &clickhouse.Exception{Code: 241, Message: "memory limit exceeded"},
			want: false,
		},
		{
			name: "dial refused",
			err:  &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
			want: true,
		},
		{
			name: "dns failure",
			err:  fmt.Errorf("dial: %w", &net.DNSError{Err: "no such host", Name: "ch.example"}),
			want: true,
		},
		{
			name: "stringified network error fallback",
			err:  errors.New("read tcp 10.0.0.1:9000: connection reset by peer"),
			want: true,
		},
		{
			name: "plain query error",
			err:  errors.New("code: 47, message: unknown identifier"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isReachabilityError(tt.err))
		})
	}
}

func TestIsUnknownTableError(t *testing.T) {
	t.Parallel()

	require.True(t, isUnknownTableError(
		fmt.Errorf("query: %w", &clickhouse.Exception{Code: chErrCodeUnknownTable, Message: "Table system.part_log does not exist"}),
	))
	require.False(t, isUnknownTableError(
		&clickhouse.Exception{Code: 81, Message: "Database foo does not exist"},
	))
	require.False(t, isUnknownTableError(errors.New("UNKNOWN_TABLE mentioned in a non-exception error")))
}
