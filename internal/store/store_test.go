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
	dir := t.TempDir()
	path := filepath.Join(dir, "run.sh")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o750))
	require.NoError(t, os.Chmod(path, 0o750))

	require.NoError(t, store.Write(path, []byte("new\r\n")))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new\r\n", string(got))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), info.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file is left")
}

func TestWriteNewFileUsesUmask(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })
	path := filepath.Join(t.TempDir(), "new", "dir", "a.go")

	require.NoError(t, store.Write(path, []byte("x")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

func TestWriteThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))
	require.NoError(t, os.Symlink("target", link))

	require.NoError(t, store.Write(link, []byte("new")))

	dest, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, "target", dest)
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
}
