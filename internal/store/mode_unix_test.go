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
	require.NoError(t, os.Chmod(path, 0o750|os.ModeSetgid))

	require.NoError(t, store.Write(path, []byte("new")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, 0o750|os.ModeSetgid, info.Mode()&(os.ModePerm|os.ModeSetgid))
}

func TestWriteKeepsHardLinks(t *testing.T) {
	dir := t.TempDir()
	path, other := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	require.NoError(t, os.Chmod(path, 0o750|os.ModeSetgid))
	require.NoError(t, os.Link(path, other))

	require.NoError(t, store.Write(path, []byte("new")))

	got, err := os.ReadFile(other)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
	info, err := os.Stat(other)
	require.NoError(t, err)
	assert.Equal(t, 0o750|os.ModeSetgid, info.Mode()&(os.ModePerm|os.ModeSetgid))
}

func TestWriteRefusesReadOnlyHardLinkedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only files")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	require.NoError(t, os.Chmod(path, 0o444))
	require.NoError(t, os.Link(path, filepath.Join(dir, "b")))

	err := store.Write(path, []byte("new"))

	assert.ErrorIs(t, err, os.ErrPermission)
	assert.NotErrorIs(t, err, store.ErrPartial)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "old", string(got))
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
