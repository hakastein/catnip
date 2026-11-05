package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/edit"
	"github.com/hakastein/catnip/internal/stats"
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
		tail    string // the end of stdout
	}{
		{name: "edit applied", project: `{"*.txt": {"check": {"ok": "grep -q b {file}"}}}`, before: "a\n", args: []string{"edit", "p/a.txt", "--replace", "a", "b"}, code: ok, after: "b\n"},
		{name: "replace with an empty NEW deletes", project: `{}`, before: "ab\n", args: []string{"edit", "p/a.txt", "--replace", "b", ""}, code: ok, after: "a\n"},
		{name: "replace with an empty OLD", project: `{}`, before: "ab\n", args: []string{"edit", "p/a.txt", "--replace", "", "b"}, code: badUsage, after: "ab\n"},
		{
			name:    "several edits applied",
			project: `{}`,
			before:  "one\ntwo\ntwo\n",
			args:    []string{"edit", "p/a.txt", "--replace", "one", "1", "--replace", "two", "2", "--count", "2"},
			code:    ok,
			after:   "1\n2\n2\n",
		},
		{
			name:    "one edit not found: nothing applied",
			project: `{}`,
			before:  "a\n",
			args:    []string{"edit", "p/a.txt", "--replace", "a", "b", "--replace", "x", "y"},
			code:    notApplied,
			after:   "a\n",
		},
		{name: "bad arguments", project: `{}`, before: "a\n", args: []string{"edit", "p/a.txt", "--before", "a", "b"}, code: badUsage, after: "a\n"},
		{name: "path missing", project: `{}`, before: "a\n", args: []string{"edit", "--replace", "--replace", "a", "b"}, code: badUsage, after: "a\n"},
		{name: "broken config", project: `{`, before: "a\n", args: []string{"edit", "p/a.txt", "--replace", "a", "b"}, code: badUsage, after: "a\n"},
		{name: "written, check failed", project: `{"*.txt": {"check": {"no": "false"}}}`, before: "a\n", args: []string{"write", "p/a.txt"}, stdin: "w\n", code: checkFailed, after: "w\n"},
		{name: "show past the end", project: `{}`, before: "a\n", args: []string{"show", "p/a.txt:5"}, code: notApplied, after: "a\n"},
		{name: "show prints line addresses", project: `{}`, before: "a\n", args: []string{"show", "p/a.txt"}, code: ok, after: "a\n", tail: "1#" + edit.Hash("a") + "\ta\n"},
		{name: "show with an unknown gutter", project: `{}`, before: "a\n", args: []string{"show", "--gutter", "hashes", "p/a.txt"}, code: badUsage, after: "a\n"},
		{name: "insert after a line", project: `{}`, before: "a\n", args: []string{"edit", "p/a.txt", "--after", "1#" + edit.Hash("a"), "b"}, code: ok, after: "a\nb\n"},
		{name: "insert at a changed line: nothing applied", project: `{}`, before: "a\n", args: []string{"edit", "p/a.txt", "--before", "1#" + edit.Hash("x"), "b"}, code: notApplied, after: "a\n"},
		{name: "write keeps BOM and CRLF of the file", project: `{}`, before: "\ufeffa\r\n", args: []string{"write", "p/a.txt"}, stdin: "b\nc\n", code: ok, after: "\ufeffb\r\nc\r\n"},
		{name: "write keeps a missing final newline", project: `{}`, before: "a", args: []string{"write", "p/a.txt"}, stdin: "b\n", code: ok, after: "b"},
		{
			name:    "fixer change is named, its diff hidden",
			project: `{"*.txt": {"fix": {"up": "printf 'B\\n' > a.txt"}}}`,
			before:  "a\n",
			args:    []string{"edit", "p/a.txt", "--replace", "a", "b"},
			code:    ok,
			after:   "B\n",
			tail:    "up: fixed\n",
		},
		{
			name:    "fixer diff on request",
			project: `{"*.txt": {"fix": {"up": "printf 'B\\n' > a.txt"}}}`,
			before:  "a\n",
			args:    []string{"write", "--show-fix-diff", "p/a.txt"},
			stdin:   "b\n",
			code:    ok,
			after:   "B\n",
			tail:    "up: fixed\np/a.txt:1\n-\tb\n+\tB\n",
		},
		{name: "stats with an unknown flag", project: `{}`, before: "a\n", args: []string{"stats", "--all"}, code: badUsage, after: "a\n"},
		{name: "stats without a database", project: `{}`, before: "a\n", args: []string{"stats"}, code: notApplied, after: "a\n"},
		{name: "help of catnip", project: `{}`, before: "a\n", args: []string{"--help"}, code: ok, after: "a\n", tail: usage},
		{name: "no command", project: `{}`, before: "a\n", args: []string{}, code: badUsage, after: "a\n"},
		{name: "help of a command", project: `{}`, before: "a\n", args: []string{"help", "edit"}, code: ok, after: "a\n", tail: editHelp},
		{name: "help of an unknown command", project: `{}`, before: "a\n", args: []string{"help", "cat"}, code: badUsage, after: "a\n"},
		{name: "show --help", project: `{}`, before: "a\n", args: []string{"show", "p/a.txt", "--help"}, code: ok, after: "a\n", tail: showHelp},
		{name: "edit --help", project: `{}`, before: "a\n", args: []string{"edit", "--help"}, code: ok, after: "a\n", tail: editHelp},
		{name: "edit --help after the path", project: `{}`, before: "a\n", args: []string{"edit", "p/a.txt", "--help"}, code: ok, after: "a\n", tail: editHelp},
		{name: "write -h", project: `{}`, before: "a\n", args: []string{"write", "-h"}, code: ok, after: "a\n", tail: writeHelp},
		{name: "stats --help", project: `{}`, before: "a\n", args: []string{"stats", "--help"}, code: ok, after: "a\n", tail: statsHelp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			t.Setenv("HOME", home)
			t.Setenv("AppData", home)
			t.Chdir(t.TempDir()) // a workspace holding the project p
			require.NoError(t, os.Mkdir("p", 0o755))
			require.NoError(t, os.WriteFile("p/.catnip.json", []byte(tt.project), 0o644))
			require.NoError(t, os.WriteFile("p/a.txt", []byte(tt.before), 0o644))
			var stdout, stderr bytes.Buffer

			code := run(tt.args, strings.NewReader(tt.stdin), &stdout, &stderr, &stats.Call{})

			assert.Equal(t, tt.code, code, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
			got, err := os.ReadFile("p/a.txt")
			require.NoError(t, err)
			assert.Equal(t, tt.after, string(got))
			assert.True(t, strings.HasSuffix(stdout.String(), tt.tail), "stdout: %s", stdout.String())
		})
	}
}
