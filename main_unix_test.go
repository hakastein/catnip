//go:build unix

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchWrittenInPartListsFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only directories")
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("a.txt", []byte("a\n"), 0o644))
	require.NoError(t, os.Mkdir("ro", 0o755))
	require.NoError(t, os.WriteFile("ro/b.txt", []byte("b\n"), 0o644))
	require.NoError(t, os.Chmod("ro", 0o555))
	t.Cleanup(func() { os.Chmod("ro", 0o755) })
	batch := "@@@ replace a.txt\na\n@@@ with\nA\n@@@ end\n@@@ replace ro/b.txt\nb\n@@@ with\nB\n@@@ end\n"
	var stdout, stderr bytes.Buffer

	code := run([]string{"edit"}, strings.NewReader(batch), &stdout, &stderr)

	assert.Equal(t, checkFailed, code)
	assert.Contains(t, stderr.String(), "written: a.txt\nnot written: ro/b.txt\n")
	got, err := os.ReadFile("a.txt")
	require.NoError(t, err)
	assert.Equal(t, "A\n", string(got))
}
