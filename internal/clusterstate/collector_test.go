package clusterstate

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetDiskCapacityKnown(t *testing.T) {
	t.Parallel()

	var disk Disk
	setDiskCapacity(&disk, 10, 20, 30)

	require.True(t, disk.CapacityKnown)
	require.NotNil(t, disk.FreeSpaceBytes)
	require.NotNil(t, disk.TotalSpaceBytes)
	require.NotNil(t, disk.UnreservedSpaceBytes)
	require.Equal(t, uint64(10), *disk.FreeSpaceBytes)
	require.Equal(t, uint64(20), *disk.TotalSpaceBytes)
	require.Equal(t, uint64(30), *disk.UnreservedSpaceBytes)
}

func TestSetDiskCapacityUnknownSentinel(t *testing.T) {
	t.Parallel()

	var disk Disk
	setDiskCapacity(&disk, math.MaxUint64, math.MaxUint64, math.MaxUint64)

	require.False(t, disk.CapacityKnown)
	require.Nil(t, disk.FreeSpaceBytes)
	require.Nil(t, disk.TotalSpaceBytes)
	require.Nil(t, disk.UnreservedSpaceBytes)
}
