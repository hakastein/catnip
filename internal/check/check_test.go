package check_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/check"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name  string
		fix   []string
		check []string
		want  check.Result
		file  string
	}{
		{name: "passing checks", check: []string{"true", "grep -q a f"}, want: check.Result{Passed: 2}, file: "a\n"},
		{
			name:  "failing check keeps its output",
			check: []string{"echo broken; exit 4"},
			want:  check.Result{Failed: []check.Failure{{Cmd: "echo broken; exit 4", Output: "broken\nexit status 4"}}},
			file:  "a\n",
		},
		{
			name:  "fixer change shows as a diff before checks run",
			fix:   []string{"printf 'A\\n' > f"},
			check: []string{"grep -q A f"},
			want:  check.Result{Diff: "f:1\n-\ta\n+\tA\n", Passed: 1},
			file:  "A\n",
		},
		{
			name: "failing fixer",
			fix:  []string{"exit 1"},
			want: check.Result{Failed: []check.Failure{{Cmd: "exit 1", Output: "exit status 1"}}},
			file: "a\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.WriteFile("f", []byte("a\n"), 0o644))

			got, err := check.Run("f", tt.fix, tt.check)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			content, err := os.ReadFile(filepath.Join(".", "f"))
			require.NoError(t, err)
			assert.Equal(t, tt.file, string(content))
		})
	}
}
