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
		fix   []config.Command
		check []config.Command
		want  []check.Outcome
		file  string
	}{
		{
			name:  "passing checks are named",
			check: []config.Command{{Name: "t", Cmd: "true"}, {Name: "has a", Cmd: "grep -q a f"}},
			want:  []check.Outcome{{Name: "t"}, {Name: "has a"}},
			file:  "a\n",
		},
		{
			name:  "failing check keeps its output",
			check: []config.Command{{Name: "broken", Cmd: "echo broken; exit 4"}},
			want:  []check.Outcome{{Name: "broken", Failed: "broken\nexit status 4"}},
			file:  "a\n",
		},
		{
			name: "each fixer shows its own change, then checks run",
			fix: []config.Command{
				{Name: "upper", Cmd: "printf 'A\\n' > f"},
				{Name: "noop", Cmd: "true"},
				{Name: "twice", Cmd: "printf 'A\\nA\\n' > f"},
			},
			check: []config.Command{{Name: "has A", Cmd: "grep -q A f"}},
			want: []check.Outcome{
				{Name: "upper", Diff: "p/f:1\n-\ta\n+\tA\n"},
				{Name: "noop"},
				{Name: "twice", Diff: "p/f:2\n+\tA\n"},
				{Name: "has A"},
			},
			file: "A\nA\n",
		},
		{
			name: "failing fixer keeps what it changed",
			fix:  []config.Command{{Name: "half", Cmd: "printf 'b\\n' > f; exit 1"}},
			want: []check.Outcome{{Name: "half", Failed: "exit status 1", Diff: "p/f:1\n-\ta\n+\tb\n"}},
			file: "b\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir("p", 0o755))
			require.NoError(t, os.WriteFile(filepath.Join("p", "f"), []byte("a\n"), 0o644))

			got, err := check.Run("p", "p/f", tt.fix, tt.check) // commands name the file from p, where they run

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			content, err := os.ReadFile(filepath.Join("p", "f"))
			require.NoError(t, err)
			assert.Equal(t, tt.file, string(content))
		})
	}
}
