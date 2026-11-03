package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		project string
		before  string
		args    []string
		stdin   string
		code    int
		after   string
	}{
		{name: "edit applied", project: `{"*.txt": {"check": ["true"]}}`, before: "a\n", args: []string{"edit", "replace", "a.txt", "a", "b"}, code: ok, after: "b\n"},
		{name: "batch applied", project: `{}`, before: "a\n", args: []string{"edit"}, stdin: "@@@ replace a.txt\na\n@@@ with\nc\n@@@ end\n", code: ok, after: "c\n"},
		{name: "nothing applied", project: `{}`, before: "a\n", args: []string{"edit", "replace", "a.txt", "x", "b"}, code: notApplied, after: "a\n"},
		{name: "bad arguments", project: `{}`, before: "a\n", args: []string{"edit", "swap", "a.txt", "a", "b"}, code: badUsage, after: "a\n"},
		{name: "broken config", project: `{`, before: "a\n", args: []string{"edit", "replace", "a.txt", "a", "b"}, code: badUsage, after: "a\n"},
		{name: "written, check failed", project: `{"*.txt": {"check": ["false"]}}`, before: "a\n", args: []string{"write", "a.txt"}, stdin: "w\n", code: checkFailed, after: "w\n"},
		{name: "show past the end", project: `{}`, before: "a\n", args: []string{"show", "a.txt:5"}, code: notApplied, after: "a\n"},
		{
			name:    "two spellings of one file in a batch edit one file",
			project: `{}`,
			before:  "one\ntwo\n",
			args:    []string{"edit"},
			stdin:   "@@@ replace a.txt\none\n@@@ with\nONE\n@@@ end\n@@@ replace ./a.txt\ntwo\n@@@ with\nTWO\n@@@ end\n",
			code:    ok,
			after:   "ONE\nTWO\n",
		},
		{name: "write keeps BOM and CRLF of the file", project: `{}`, before: "\ufeffa\r\n", args: []string{"write", "a.txt"}, stdin: "b\nc\n", code: ok, after: "\ufeffb\r\nc\r\n"},
		{name: "write keeps a missing final newline", project: `{}`, before: "a", args: []string{"write", "a.txt"}, stdin: "b\n", code: ok, after: "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			t.Setenv("HOME", home)
			t.Setenv("AppData", home)
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir(".git", 0o755))
			require.NoError(t, os.WriteFile(".catnip.json", []byte(tt.project), 0o644))
			require.NoError(t, os.WriteFile("a.txt", []byte(tt.before), 0o644))
			var stdout, stderr bytes.Buffer

			code := run(tt.args, strings.NewReader(tt.stdin), &stdout, &stderr)

			assert.Equal(t, tt.code, code, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
			got, err := os.ReadFile("a.txt")
			require.NoError(t, err)
			assert.Equal(t, tt.after, string(got))
		})
	}
}

func TestEditJoinsSymlinkWithItsTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("AppData", home)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("a.txt", []byte("one\ntwo\n"), 0o644))
	require.NoError(t, os.Symlink("a.txt", "link.txt"))
	batch := "@@@ replace a.txt\none\n@@@ with\nONE\n@@@ end\n@@@ replace link.txt\ntwo\n@@@ with\nTWO\n@@@ end\n"
	var stdout, stderr bytes.Buffer

	code := run([]string{"edit"}, strings.NewReader(batch), &stdout, &stderr)

	assert.Equal(t, ok, code, stderr.String())
	got, err := os.ReadFile("a.txt")
	require.NoError(t, err)
	assert.Equal(t, "ONE\nTWO\n", string(got))
}
