//go:build !webui

package web

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetFSWithoutWebUITag(t *testing.T) {
	t.Parallel()

	fsys, err := GetFS()
	require.NoError(t, err)

	file, err := fsys.Open("index.html")
	require.Nil(t, file)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestEmptyFSOpen(t *testing.T) {
	t.Parallel()

	file, err := emptyFS{}.Open("anything")
	require.Nil(t, file)
	require.ErrorIs(t, err, fs.ErrNotExist)
}
