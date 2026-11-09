package view_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/edit"
	"github.com/hakastein/catnip/internal/view"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in   string
		want view.Target
		err  string
	}{
		{in: "a.go", want: view.Target{Path: "a.go"}},
		{in: "dir/a.go:3-9", want: view.Target{Path: "dir/a.go", From: 3, To: 9}},
		{in: "a.go:10", want: view.Target{Path: "a.go", From: 8, To: 12, Line: 10}},
		{in: "a.go:10:7", want: view.Target{Path: "a.go", From: 8, To: 12, Line: 10, Column: 7}},
		{in: "a.go:1", want: view.Target{Path: "a.go", From: 1, To: 3, Line: 1}},
		{in: "a:b.go", want: view.Target{Path: "a:b.go"}},
		{in: `C:\src\a.go`, want: view.Target{Path: `C:\src\a.go`}},
		{in: `C:\src\a.go:3-9`, want: view.Target{Path: `C:\src\a.go`, From: 3, To: 9}},
		{in: "a.go:0", err: "a.go:0: lines start at 1"},
		{in: "a.go:10#0b1c", want: view.Target{Path: "a.go", From: 8, To: 12, Line: 10, Hash: "0b1c"}},
		{in: `C:\a#b\a.go`, want: view.Target{Path: `C:\a#b\a.go`}},
		{in: "a.go:3#zz", err: `a.go:3#zz: "3#zz" is not LINE#HASH as catnip show prints it`},
		{in: "a.go:3#0b1c-4#0b1c", err: `a.go:3#0b1c-4#0b1c: "3#0b1c-4#0b1c" is a range, want one LINE#HASH`},
		{in: "a.go:9-3", err: "a.go:9-3: range ends before it starts"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := view.ParseTarget(tt.in, 2)
			assert.Equal(t, tt.err, errText(err))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRender(t *testing.T) {
	text := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n"
	tests := []struct {
		name   string
		target view.Target
		gutter string
		blame  []string
		want   string
		err    string
	}{
		{name: "line addresses", target: view.Target{Path: "f", From: 1, To: 2}, gutter: "hash", want: "1#" + edit.Hash("a") + "\ta\n2#" + edit.Hash("b") + "\tb\n"},
		{name: "whole file", target: view.Target{Path: "f"}, gutter: "line", want: " 1\ta\n 2\tb\n 3\tc\n 4\td\n 5\te\n 6\tf\n 7\tg\n 8\th\n 9\ti\n10\tj\n11\tk\n"},
		{name: "range at the end", target: view.Target{Path: "f", From: 10, To: 11}, gutter: "line", want: "10\tj\n11\tk\n"},
		{name: "range past the end", target: view.Target{Path: "f", From: 10, To: 12}, gutter: "line", err: "line 12 is past the end: f has 11 lines"},
		{name: "position context cut at the end", target: view.Target{Path: "f", From: 9, To: 13, Line: 11}, gutter: "line", want: " 9\ti\n10\tj\n11\tk\n"},
		{name: "position past the end", target: view.Target{Path: "f", From: 10, To: 14, Line: 12}, gutter: "line", err: "line 12 is past the end: f has 11 lines"},
		{name: "address", target: view.Target{Path: "f", From: 2, To: 2, Line: 2, Hash: edit.Hash("b")}, gutter: "line", want: "2\tb\n"},
		{name: "changed line", target: view.Target{Path: "f", From: 2, To: 2, Line: 2, Hash: "0000"}, gutter: "line", err: "line 2 changed: 2#" + edit.Hash("b") + "\tb"},
		{
			name:   "hash and blame columns",
			target: view.Target{Path: "f", From: 1, To: 2},
			gutter: "hash",
			blame:  []string{"x", "y"},
			want:   "1#" + edit.Hash("a") + "\tx\ta\n2#" + edit.Hash("b") + "\ty\tb\n",
		},
		{name: "text alone", target: view.Target{Path: "f", From: 9, To: 10}, gutter: "none", want: "i\nj\n"},
		{name: "blame without a gutter", target: view.Target{Path: "f", From: 1, To: 2}, gutter: "none", blame: []string{"x", "y"}, want: "x\ta\ny\tb\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := view.Render(text, tt.target, tt.gutter, tt.blame)
			assert.Equal(t, tt.err, errText(err))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBlame(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("one\ntwo\n"), 0o644))
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Ann", "-c", "user.email=a@x", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_DATE=2026-01-02T12:00:00Z", "GIT_COMMITTER_DATE=2026-01-02T12:00:00Z")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	require.NoError(t, os.WriteFile(file, []byte("one\nTWO\n"), 0o644))

	got, err := view.Blame(file, 1, 2)

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Regexp(t, `^[0-9a-f]{8} Ann               2026-01-0[12]$`, got[0])
	assert.Regexp(t, `^00000000 Not Committed Yet \d{4}-\d\d-\d\d$`, got[1])
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
