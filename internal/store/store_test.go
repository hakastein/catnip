package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/store"
)

func TestWriteReplacesBytesAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	require.NoError(t, store.Write(path, []byte("\ufeffnew\r\n")))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "\ufeffnew\r\n", string(got))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestWriteCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "dir", "a.go")

	require.NoError(t, store.Write(path, []byte("x")))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "x", string(got))
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
