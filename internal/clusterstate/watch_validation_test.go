package clusterstate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsPhysicalMergeTreeEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		engine string
		want   bool
	}{
		{engine: "MergeTree", want: true},
		{engine: "ReplicatedMergeTree", want: true},
		{engine: "ReplacingMergeTree", want: true},
		{engine: "ReplicatedReplacingMergeTree", want: true},
		{engine: "AggregatingMergeTree", want: true},
		{engine: "Distributed", want: false},
		{engine: "View", want: false},
		{engine: "MaterializedView", want: false},
		{engine: "Buffer", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, isPhysicalMergeTreeEngine(tt.engine))
		})
	}
}
