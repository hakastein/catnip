//go:build unix

package store_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/store"
)

func TestWriteKeepsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.sh")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o750))
	require.NoError(t, os.Chmod(path, 0o750))

	require.NoError(t, store.Write(path, []byte("new")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), info.Mode().Perm())
}

func TestWriteNewFileUsesUmask(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })
	path := filepath.Join(t.TempDir(), "a.go")

	require.NoError(t, store.Write(path, []byte("x")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}
