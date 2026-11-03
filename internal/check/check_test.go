package check_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/check"
	"github.com/hakastein/catnip/internal/config"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name  string
		fix   []string
		check []config.Check
		want  check.Result
		file  string
	}{
		{
			name:  "passing checks are named",
			check: []config.Check{{Name: "t", Cmd: "true"}, {Name: "has a", Cmd: "grep -q a f"}},
			want:  check.Result{Passed: []string{"t", "has a"}},
			file:  "a\n",
		},
		{
			name:  "failing check keeps its output",
			check: []config.Check{{Name: "broken", Cmd: "echo broken; exit 4"}},
			want:  check.Result{Failed: []check.Failure{{Name: "broken", Output: "broken\nexit status 4"}}},
			file:  "a\n",
		},
		{
			name:  "fixer change shows as a diff before checks run",
			fix:   []string{"printf 'A\\n' > f"},
			check: []config.Check{{Name: "has A", Cmd: "grep -q A f"}},
			want:  check.Result{Diff: "f:1\n-\ta\n+\tA\n", Passed: []string{"has A"}},
			file:  "A\n",
		},
		{
			name: "failing fixer",
			fix:  []string{"exit 1"},
			want: check.Result{Failed: []check.Failure{{Name: "exit 1", Output: "exit status 1"}}},
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
