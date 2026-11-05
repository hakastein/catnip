package stats_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/stats"
)

var noon = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// workspace makes a working directory holding files, path to content, and returns it with the database path; "~" in
// a content stands for the directory.
func workspace(t *testing.T, files map[string]string) (dir, db string) {
	dir = t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(content, "~", dir)), 0o644))
	}
	t.Chdir(dir)
	return dir, filepath.Join(dir, "stats", "stats.db")
}

// git files of a main working tree "main" on branch main with a linked worktree "wt" on branch feature/x.
func worktree(more ...string) map[string]string {
	files := map[string]string{
		"main/.git/HEAD":                   "ref: refs/heads/main\n",
		"main/.git/worktrees/wt/HEAD":      "ref: refs/heads/feature/x\n",
		"main/.git/worktrees/wt/commondir": "../..\n",
		"wt/.git":                          "gitdir: ~/main/.git/worktrees/wt\n",
	}
	for i := 0; i < len(more); i += 2 {
		files[more[i]] = more[i+1]
	}
	return files
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

// recorded is a recorded call with the workspace as "~" in paths.
type recorded struct {
	Command, Flags, Argv, Cwd, File, Project, Worktree, Branch, Config string
}

func TestRecord(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		cwd   string
		call  stats.Call
		want  recorded
	}{
		{
			name:  "main working tree, project config below its root",
			files: worktree("main/sub/.catnip.json", "{}"),
			call: stats.Call{Time: noon, Argv: []string{"edit", "main/sub/a.go", "--replace", "<a>", "b", "--replace", "c", "d", "--count", "2"},
				File: "main/sub/a.go", Flags: []string{"--replace", "--replace", "--count"}},
			want: recorded{Command: "edit", Flags: "--count --replace", Argv: `["edit","main/sub/a.go","--replace","<a>","b","--replace","c","d","--count","2"]`,
				Cwd: "~", File: "~/main/sub/a.go", Project: "~/main", Worktree: "~/main", Branch: "main", Config: "~/main/sub/.catnip.json"},
		},
		{
			name:  "linked worktree without a config: project and config of the main tree",
			files: worktree("main/.catnip.json", "{}"),
			call:  stats.Call{Time: noon, Argv: []string{"show", "wt/a.txt:3"}, File: "wt/a.txt"},
			want: recorded{Command: "show", Argv: `["show","wt/a.txt:3"]`, Cwd: "~", File: "~/wt/a.txt", Project: "~/main",
				Worktree: "~/wt", Branch: "feature/x", Config: "~/main/.catnip.json"},
		},
		{
			name:  "no file: repository from the working directory",
			files: worktree(),
			cwd:   "wt",
			call:  stats.Call{Time: noon, Argv: []string{"grep", "--all", "x"}},
			want:  recorded{Command: "grep", Argv: `["grep","--all","x"]`, Cwd: "~/wt", Project: "~/main", Worktree: "~/wt", Branch: "feature/x"},
		},
		{
			name: "outside git the project is the working directory",
			call: stats.Call{Time: noon, Argv: []string{}},
			want: recorded{Argv: `[]`, Cwd: "~", Project: "~"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, db := workspace(t, tt.files)
			t.Chdir(filepath.Join(dir, tt.cwd))

			require.NoError(t, stats.Record(db, tt.call, 0, stats.Limit))

			var got recorded
			// paths with "/" on every platform
			require.NoError(t, open(t, db).QueryRow(`SELECT command, flags, argv, replace(replace(cwd, ?1, '~'), '\', '/'),
				replace(replace(file, ?1, '~'), '\', '/'), replace(replace(project, ?1, '~'), '\', '/'),
				replace(replace(worktree, ?1, '~'), '\', '/'), branch, replace(replace(config, ?1, '~'), '\', '/') FROM calls`, dir).
				Scan(&got.Command, &got.Flags, &got.Argv, &got.Cwd, &got.File, &got.Project, &got.Worktree, &got.Branch, &got.Config))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRecordError(t *testing.T) {
	tests := []struct {
		name         string
		err, file    string
		stored, want string // error and its group
	}{
		{name: "no error", stored: "", want: ""},
		{
			name:   "changed line: file, numbers, address and the line after a tab",
			err:    "p/a1.txt: edit 2: line 17 changed: 17#ab12\tx := 42\nnothing written\n",
			file:   "p/a1.txt",
			stored: "p/a1.txt: edit 2: line 17 changed: 17#ab12\tx := 42\nnothing written",
			want:   "<file>: edit <n>: line <n> changed: <n>#<hash>",
		},
		{
			name:   "text not found: quoted text holding \"; \", detail after \"; \"",
			err:    `p/a.txt: edit 1: not found: "a; \"b\""; similar lines:` + "\n  3: a",
			file:   "p/a.txt",
			stored: `p/a.txt: edit 1: not found: "a; \"b\""; similar lines:` + "\n  3: a",
			want:   "<file>: edit <n>: not found: <text>",
		},
		{name: "unknown command", err: "unknown command \"grep\"\n", stored: `unknown command "grep"`, want: "unknown command <text>"},
		{name: "invented flag stays in the group", err: "flag provided but not defined: -all\n", stored: "flag provided but not defined: -all", want: "flag provided but not defined: -all"},
		{name: "short file name only as a whole path", err: "open e: no such file or directory", file: "e", stored: "open e: no such file or directory", want: "open <file>: no such file or directory"},
		{
			name:   "long error cut to 1 KB on a rune boundary",
			err:    "x" + strings.Repeat("é", 1000),
			stored: "x" + strings.Repeat("é", 511),
			want:   "x" + strings.Repeat("é", 511),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, db := workspace(t, nil)

			require.NoError(t, stats.Record(db, stats.Call{Time: noon, Argv: []string{"edit"}, File: tt.file, Error: tt.err}, 1, stats.Limit))

			var stored, group string
			require.NoError(t, open(t, db).QueryRow(`SELECT error, error_group FROM calls`).Scan(&stored, &group))
			assert.Equal(t, []string{tt.stored, tt.want}, []string{stored, group})
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
				require.NoError(t, stats.Record(db, stats.Call{Time: noon, Argv: []string{"show", "a"}}, code, tt.limit))
			}

			d := open(t, db)
			assert.Equal(t, tt.argv, column[int](t, d, `SELECT exit_code FROM calls WHERE argv IS NOT NULL ORDER BY id`))
			assert.Equal(t, tt.codes, column[int](t, d, `SELECT exit_code FROM calls ORDER BY id`))
			assert.Equal(t, []int{2}, column[int](t, d, `PRAGMA auto_vacuum`), "incremental")
		})
	}
}

// history records five calls, ids 1 to 5, in a workspace with the main tree "main", its linked worktree "wt" and a
// repository "other", and returns the workspace and the database.
func history(t *testing.T) (dir, db string) {
	dir, db = workspace(t, worktree("main/.catnip.json", "{}", "other/.git/HEAD", "ref: refs/heads/main\n"))
	for _, c := range []struct {
		call stats.Call
		code int
	}{
		{stats.Call{Time: noon.AddDate(0, 0, -1), Argv: []string{"show", "main/a.txt"}, File: "main/a.txt"}, 0},
		{stats.Call{Time: noon, Argv: []string{"edit", "wt/a.txt", "--lines", "3#abcd", "x"}, File: "wt/a.txt", Flags: []string{"--lines"},
			Error: "wt/a.txt: edit 1: line 3 changed: 3#ab12\tx\nnothing written"}, 1},
		{stats.Call{Time: noon, Argv: []string{"edit", "wt/a.txt", "--lines", "3#ab12", "x"}, File: "wt/a.txt", Flags: []string{"--lines"}}, 0},
		{stats.Call{Time: noon, Argv: []string{"grep", "x"}, Error: `unknown command "grep"`}, 2},
		{stats.Call{Time: noon.Add(time.Minute), Argv: []string{"show", "other/b.txt"}, File: "other/b.txt",
			Error: "open other/b.txt: no such file or directory"}, 1},
	} {
		require.NoError(t, stats.Record(db, c.call, c.code, stats.Limit))
	}
	return dir, db
}

func TestQuery(t *testing.T) {
	dir, db := history(t)
	main, wt, other := filepath.Join(dir, "main"), filepath.Join(dir, "wt"), filepath.Join(dir, "other")
	config := filepath.Join(main, ".catnip.json")
	day0, day1, later := "2026-11-04T12:00:00.000Z", "2026-11-05T12:00:00.000Z", "2026-11-05T12:01:00.000Z"
	_, offset := time.Now().Zone() // days are local by the current offset
	today := noon.Add(time.Duration(offset) * time.Second).Format(time.DateOnly)
	type row = map[string]any
	tests := []struct {
		name   string
		view   string
		filter stats.Filter
		want   []row
	}{
		{
			name: "projects, by calls",
			view: "projects",
			want: []row{
				{"project": main, "calls": int64(3), "failed": int64(1), "show": int64(1), "edit": int64(2), "write": int64(0), "last": day1},
				{"project": dir, "calls": int64(1), "failed": int64(1), "show": int64(0), "edit": int64(0), "write": int64(0), "last": day1},
				{"project": other, "calls": int64(1), "failed": int64(1), "show": int64(1), "edit": int64(0), "write": int64(0), "last": later},
			},
		},
		{
			name:   "worktrees of a project, latest first",
			view:   "worktrees",
			filter: stats.Filter{Project: main},
			want: []row{
				{"worktree": wt, "branch": "feature/x", "config": config, "calls": int64(2), "failed": int64(1), "last": day1},
				{"worktree": main, "branch": "main", "config": config, "calls": int64(1), "failed": int64(0), "last": day0},
			},
		},
		{
			name:   "usage: command with its flags by exit code",
			view:   "usage",
			filter: stats.Filter{Project: main},
			want: []row{
				{"kind": "edit --lines", "code": int64(0), "calls": int64(1)},
				{"kind": "edit --lines", "code": int64(1), "calls": int64(1)},
				{"kind": "show", "code": int64(0), "calls": int64(1)},
			},
		},
		{
			name: "error groups",
			view: "errors",
			want: []row{
				{"group": "<file>: edit <n>: line <n> changed: <n>#<hash>", "code": int64(1), "calls": int64(1), "last": day1},
				{"group": "open <file>: no such file or directory", "code": int64(1), "calls": int64(1), "last": later},
				{"group": "unknown command <text>", "code": int64(2), "calls": int64(1), "last": day1},
			},
		},
		{
			name:   "days since a time",
			view:   "days",
			filter: stats.Filter{Since: noon},
			want: []row{
				{"day": today, "code": int64(0), "calls": int64(1)},
				{"day": today, "code": int64(1), "calls": int64(2)},
				{"day": today, "code": int64(2), "calls": int64(1)},
			},
		},
		{
			name:   "call of an error group, every column",
			view:   "calls",
			filter: stats.Filter{Group: "<file>: edit <n>: line <n> changed: <n>#<hash>"},
			want: []row{{
				"id": int64(2), "time": day1, "kind": "edit --lines", "code": int64(1), "argv": `["edit","wt/a.txt","--lines","3#abcd","x"]`,
				"error": "wt/a.txt: edit 1: line 3 changed: 3#ab12\tx\nnothing written", "group": "<file>: edit <n>: line <n> changed: <n>#<hash>",
				"cwd": dir, "file": filepath.Join(wt, "a.txt"), "project": main, "worktree": wt, "branch": "feature/x", "config": config,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stats.Query(db, tt.view, tt.filter)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestQueryCalls(t *testing.T) {
	dir, db := history(t)
	tests := []struct {
		name   string
		filter stats.Filter
		ids    []int64
	}{
		{name: "every call, newest first", ids: []int64{5, 4, 3, 2, 1}},
		{name: "failed", filter: stats.Filter{Code: "failed"}, ids: []int64{5, 4, 2}},
		{name: "exit code", filter: stats.Filter{Code: "0"}, ids: []int64{3, 1}},
		{name: "kind and exit code", filter: stats.Filter{Kind: "edit --lines", Code: "1"}, ids: []int64{2}},
		{name: "command without flags", filter: stats.Filter{Kind: "show"}, ids: []int64{5, 1}},
		{name: "file", filter: stats.Filter{File: filepath.Join(dir, "wt", "a.txt")}, ids: []int64{3, 2}},
		{name: "worktree", filter: stats.Filter{Worktree: filepath.Join(dir, "main")}, ids: []int64{1}},
		{name: "since a time", filter: stats.Filter{Since: noon.Add(time.Minute)}, ids: []int64{5}},
		{name: "next page", filter: stats.Filter{Before: 3}, ids: []int64{2, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := stats.Query(db, "calls", tt.filter)
			require.NoError(t, err)

			ids := []int64{}
			for _, r := range rows {
				ids = append(ids, r["id"].(int64))
			}
			assert.Equal(t, tt.ids, ids)
		})
	}
}

func TestQueryUnknownView(t *testing.T) {
	_, db := history(t)

	_, err := stats.Query(db, "flags", stats.Filter{})

	assert.EqualError(t, err, `no view "flags"`)
}

func TestQueryWithoutDatabase(t *testing.T) {
	_, err := stats.Query(filepath.Join(t.TempDir(), "stats.db"), "calls", stats.Filter{})

	assert.ErrorIs(t, err, os.ErrNotExist)
}
