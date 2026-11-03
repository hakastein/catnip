package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name    string
		project string
		args    []string
		stdin   string
		code    int
		file    string
	}{
		{name: "edit applied", project: `{"*.txt": {"check": ["true"]}}`, args: []string{"edit", "replace", "a.txt", "a", "b"}, code: ok, file: "b\n"},
		{name: "batch applied", project: `{}`, args: []string{"edit"}, stdin: "@@@ replace a.txt\na\n@@@ with\nc\n@@@ end\n", code: ok, file: "c\n"},
		{name: "nothing applied", project: `{}`, args: []string{"edit", "replace", "a.txt", "x", "b"}, code: notApplied, file: "a\n"},
		{name: "bad arguments", project: `{}`, args: []string{"edit", "swap", "a.txt", "a", "b"}, code: badUsage, file: "a\n"},
		{name: "broken config", project: `{`, args: []string{"edit", "replace", "a.txt", "a", "b"}, code: badUsage, file: "a\n"},
		{name: "written, check failed", project: `{"*.txt": {"check": ["false"]}}`, args: []string{"write", "a.txt"}, stdin: "w\n", code: checkFailed, file: "w\n"},
		{name: "show past the end", project: `{}`, args: []string{"show", "a.txt:5"}, code: notApplied, file: "a\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir(".git", 0o755))
			require.NoError(t, os.WriteFile(".catnip.json", []byte(tt.project), 0o644))
			require.NoError(t, os.WriteFile("a.txt", []byte("a\n"), 0o644))
			var stdout, stderr bytes.Buffer

			code := run(tt.args, strings.NewReader(tt.stdin), &stdout, &stderr)

			assert.Equal(t, tt.code, code, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
			got, err := os.ReadFile("a.txt")
			require.NoError(t, err)
			assert.Equal(t, tt.file, string(got))
		})
	}
}
