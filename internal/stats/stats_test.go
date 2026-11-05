package stats_test

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/stats"
)

var noon = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// workspace makes a working directory holding files, path to content, and returns it with the database path.
func workspace(t *testing.T, files map[string]string) (dir, db string) {
	dir = t.TempDir()
	for name, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	t.Chdir(dir)
	return dir, filepath.Join(dir, "stats", "stats.db")
}

func open(t *testing.T, db string) *sql.DB {
	d, err := sql.Open("sqlite", db)
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

// column lists one column of a query.
func column[T any](t *testing.T, d *sql.DB, query string, args ...any) []T {
	rows, err := d.Query(query, args...)
	require.NoError(t, err)
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var v T
		require.NoError(t, rows.Scan(&v))
		out = append(out, v)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestRecord(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		cwd   string
		call  stats.Call
		code  int
		want  []string // cwd, file, git root, catnip root with the workspace as ~, then argv
		flags []string
	}{
		{
			name:  "git repository with a project config below its root",
			files: map[string]string{"repo/.git/HEAD": "", "repo/sub/.catnip.json": "{}"},
			call:  stats.Call{Time: noon, Argv: []string{"edit", "repo/sub/a.go", "--replace", "<a>", "b", "--count", "2"}, File: "repo/sub/a.go", Flags: []string{"--replace", "--count"}},
			want:  []string{"~", filepath.Join("~", "repo", "sub", "a.go"), filepath.Join("~", "repo"), filepath.Join("~", "repo", "sub"), `["edit","repo/sub/a.go","--replace","<a>","b","--count","2"]`},
			flags: []string{"--replace", "--count"},
		},
		{
			name:  "worktree, .git is a file",
			files: map[string]string{"repo/.git": "gitdir: elsewhere"},
			call:  stats.Call{Time: noon, Argv: []string{"show", "repo/a.txt:3"}, File: "repo/a.txt"},
			want:  []string{"~", filepath.Join("~", "repo", "a.txt"), filepath.Join("~", "repo"), "", `["show","repo/a.txt:3"]`},
			flags: []string{},
		},
		{
			name:  "no file: roots from the working directory",
			files: map[string]string{"repo/.git/HEAD": ""},
			cwd:   "repo",
			call:  stats.Call{Time: noon, Argv: []string{"grep", "--all", "x"}},
			code:  2,
			want:  []string{filepath.Join("~", "repo"), "", filepath.Join("~", "repo"), "", `["grep","--all","x"]`},
			flags: []string{},
		},
		{
			name:  "no project",
			call:  stats.Call{Time: noon, Argv: []string{}},
			code:  2,
			want:  []string{"~", "", "", "", `[]`},
			flags: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, db := workspace(t, tt.files)
			t.Chdir(filepath.Join(dir, tt.cwd))

			require.NoError(t, stats.Record(db, tt.call, tt.code, stats.Limit))

			d := open(t, db)
			var got [5]string
			var code int
			require.NoError(t, d.QueryRow(`SELECT replace(cwd, ?1, '~'), replace(file, ?1, '~'), replace(git_root, ?1, '~'),
				replace(catnip_root, ?1, '~'), argv, exit_code FROM calls WHERE time = '2026-11-05T12:00:00.000Z'`, dir).
				Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &code))
			assert.Equal(t, tt.want, got[:])
			assert.Equal(t, tt.code, code)
			assert.Equal(t, tt.flags, column[string](t, d, `SELECT name FROM flags ORDER BY rowid`))
		})
	}
}

func TestRecordLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int64
		codes []int
		argv  []int // exit codes of the calls that keep argv
	}{
		{name: "under the limit every call keeps argv", limit: stats.Limit, codes: []int{0, 1, 2, 3}, argv: []int{0, 1, 2, 3}},
		{name: "over the limit only failed calls keep argv", limit: 1, codes: []int{0, 1, 2, 3, 0}, argv: []int{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, db := workspace(t, nil)
			for _, code := range tt.codes {
				require.NoError(t, stats.Record(db, stats.Call{Time: noon, Argv: []string{"show", "a"}, Flags: []string{"--hash"}}, code, tt.limit))
			}

			d := open(t, db)
			assert.Equal(t, tt.argv, column[int](t, d, `SELECT exit_code FROM calls WHERE argv IS NOT NULL ORDER BY id`))
			assert.Equal(t, tt.codes, column[int](t, d, `SELECT exit_code FROM calls ORDER BY id`))
			assert.Len(t, column[string](t, d, `SELECT name FROM flags`), len(tt.codes))
			assert.Equal(t, []int{2}, column[int](t, d, `PRAGMA auto_vacuum`), "incremental")
		})
	}
}

func TestCollect(t *testing.T) {
	dir, db := workspace(t, map[string]string{"repo/.git/HEAD": ""})
	repo, file := filepath.Join(dir, "repo"), filepath.Join(dir, "repo", "a.go")
	for _, c := range []struct {
		call stats.Call
		code int
	}{
		{stats.Call{Time: noon.AddDate(0, 0, -1), Argv: []string{"show", "--hash", "repo/a.go"}, File: "repo/a.go", Flags: []string{"--hash"}}, 0},
		{stats.Call{Time: noon, Argv: []string{"edit", "repo/a.go", "--replace", "x", "y"}, File: "repo/a.go", Flags: []string{"--replace"}}, 1},
		{stats.Call{Time: noon, Argv: []string{"grep", "x"}}, 2},
	} {
		require.NoError(t, stats.Record(db, c.call, c.code, stats.Limit))
	}
	failures := [][]any{
		{"2026-11-05T12:00:00.000Z", "grep", dir, "", int64(2), `["grep","x"]`},
		{"2026-11-05T12:00:00.000Z", "edit", dir, file, int64(1), `["edit","repo/a.go","--replace","x","y"]`},
	}
	tests := []struct {
		name  string
		since time.Time
		want  *stats.Report
	}{
		{
			name: "every call",
			want: &stats.Report{
				Summary:  [][]any{{int64(3), int64(1), "2026-11-04T12:00:00.000Z", "2026-11-05T12:00:00.000Z"}},
				Days:     [][]any{{"2026-11-04", "show", int64(1)}, {"2026-11-05", "edit", int64(1)}, {"2026-11-05", "other", int64(1)}},
				Projects: [][]any{{repo, "", int64(2), int64(1), int64(1), int64(0), int64(1)}, {"", "", int64(1), int64(0), int64(0), int64(0), int64(1)}},
				Dirs:     [][]any{{dir, int64(3)}},
				Codes:    [][]any{{"edit", int64(1), int64(1)}, {"other", int64(2), int64(1)}, {"show", int64(0), int64(1)}},
				Flags:    [][]any{{"--hash", int64(1)}, {"--replace", int64(1)}},
				Failures: failures,
				Commands: [][]any{{"grep", int64(1)}},
			},
		},
		{
			name:  "since a time",
			since: noon,
			want: &stats.Report{
				Summary:  [][]any{{int64(2), int64(1), "2026-11-05T12:00:00.000Z", "2026-11-05T12:00:00.000Z"}},
				Days:     [][]any{{"2026-11-05", "edit", int64(1)}, {"2026-11-05", "other", int64(1)}},
				Projects: [][]any{{"", "", int64(1), int64(0), int64(0), int64(0), int64(1)}, {repo, "", int64(1), int64(0), int64(1), int64(0), int64(1)}},
				Dirs:     [][]any{{dir, int64(2)}},
				Codes:    [][]any{{"edit", int64(1), int64(1)}, {"other", int64(2), int64(1)}},
				Flags:    [][]any{{"--replace", int64(1)}},
				Failures: failures,
				Commands: [][]any{{"grep", int64(1)}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stats.Collect(db, tt.since)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCollectWithoutDatabase(t *testing.T) {
	_, err := stats.Collect(filepath.Join(t.TempDir(), "stats.db"), time.Time{})

	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRender(t *testing.T) {
	r := &stats.Report{
		Summary:  [][]any{{int64(1), int64(0), "2026-11-05T12:00:00.000Z", "2026-11-05T12:00:00.000Z"}},
		Failures: [][]any{{"2026-11-05T12:00:00.000Z", "edit", "/w", "/w/a", int64(1), `["edit","a","--replace","</script><b>x","y"]`}},
	}
	var out bytes.Buffer

	require.NoError(t, stats.Render(&out, r))

	assert.NotContains(t, out.String(), "</script><b>", "argv stays data inside the page script")
	assert.Contains(t, out.String(), `\u003c/script\u003e\u003cb\u003ex`)
}
