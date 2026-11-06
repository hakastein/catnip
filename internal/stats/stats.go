// Package stats records every catnip call in a SQLite database and serves a report on it for the person running the
// agents.
package stats

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hakastein/catnip/internal/config"
)

// Limit is the database size past which Record drops argv of the oldest calls with codes 0 and 3.
const Limit = 100 << 20

// errorLimit is the most bytes of a call's stderr Record keeps.
const errorLimit = 1 << 10

// Call is one catnip call.
type Call struct {
	Time time.Time
	// Argv is the arguments after the program name; the first one is the command.
	Argv []string
	// File is the file the call is about as given, "" when the arguments name none.
	File string
	// Flags are the recognized flags.
	Flags []string
	// Error is what the call wrote to stderr, without the help text.
	Error string
}

const schema = `
CREATE TABLE IF NOT EXISTS calls (
  id          INTEGER PRIMARY KEY,
  time        TEXT NOT NULL,
  command     TEXT NOT NULL,
  flags       TEXT NOT NULL,
  argv        TEXT,
  exit_code   INTEGER NOT NULL,
  error       TEXT NOT NULL,
  error_group TEXT NOT NULL,
  cwd         TEXT NOT NULL,
  file        TEXT NOT NULL,
  project     TEXT NOT NULL,
  worktree    TEXT NOT NULL,
  branch      TEXT NOT NULL,
  config      TEXT NOT NULL
);`

// timeFormat is UTC that sorts as text and that SQLite date functions read.
const timeFormat = "2006-01-02T15:04:05.000Z"

// Path is <user config dir>/catnip/stats.db.
func Path() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "catnip", "stats.db"), nil
}

// Record adds the call and its exit code to the database at path, creating it. The repository and the project config
// are looked up from the file's directory, or from the working directory without a file. Past limit bytes it drops
// argv of the oldest tenth of calls with codes 0 and 3.
func Record(path string, c Call, code int, limit int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path+"?_busy_timeout=200&_auto_vacuum=incremental&_journal_mode=wal")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	file, dir := "", cwd
	if c.File != "" {
		if file, err = filepath.Abs(c.File); err != nil {
			return err
		}
		dir = filepath.Dir(file)
	}
	command := ""
	if len(c.Argv) > 0 {
		command = c.Argv[0]
	}
	var argv strings.Builder
	enc := json.NewEncoder(&argv)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(append([]string{}, c.Argv...)); err != nil {
		return err
	}
	repo := config.FindRepo(dir)
	if repo.Root == "" { // a file outside git belongs to the repository the call was made in
		repo = config.FindRepo(cwd)
	}
	project := repo.Main
	if project == "" {
		project = cwd
	}
	_, cfg := config.Project(dir)
	text := strings.TrimSpace(c.Error)
	if len(text) > errorLimit {
		text = strings.ToValidUTF8(text[:errorLimit], "")
	}
	if _, err := db.Exec(`INSERT INTO calls (time, command, flags, argv, exit_code, error, error_group, cwd, file, project,
		worktree, branch, config) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Time.UTC().Format(timeFormat), command, strings.Join(slices.Compact(slices.Sorted(slices.Values(c.Flags))), " "),
		strings.TrimSuffix(argv.String(), "\n"), code, text, group(text, c.File), cwd, file, project, repo.Root,
		repo.Branch, cfg); err != nil {
		return err
	}
	var size int64
	if err := db.QueryRow(`SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&size); err != nil {
		return err
	}
	if size <= limit {
		return nil
	}
	if _, err := db.Exec(`UPDATE calls SET argv = NULL WHERE id IN (
		SELECT id FROM calls WHERE argv IS NOT NULL AND exit_code IN (0, 3) ORDER BY id
		LIMIT (SELECT count(*) / 10 + 1 FROM calls WHERE argv IS NOT NULL AND exit_code IN (0, 3)))`); err != nil {
		return err
	}
	_, err = db.Exec(`PRAGMA incremental_vacuum`)
	return err
}

var (
	quoted  = regexp.MustCompile(`"(?:[^"\\]|\\.)*"…?`) // … marks text cut in an error
	address = regexp.MustCompile(`\b\d+#[0-9a-f]{4}\b`)
	number  = regexp.MustCompile(`\d+`)
)

// group is the first line of an error with what differs between calls replaced: the file as given, quoted text,
// line addresses and numbers. The detail after "; " or a tab is dropped.
func group(text, file string) string {
	line, _, _ := strings.Cut(text, "\n")
	if file != "" { // only the whole path: a short name like "e" is also a part of words
		path := regexp.MustCompile(`(^|[\s:"'(])` + regexp.QuoteMeta(file) + `($|[\s:"')])`)
		line = path.ReplaceAllString(line, "${1}<file>${2}")
	}
	line = quoted.ReplaceAllString(line, "<text>")
	line, _, _ = strings.Cut(line, "; ")
	line, _, _ = strings.Cut(line, "\t")
	line = address.ReplaceAllString(line, "<n>#<hash>")
	return number.ReplaceAllString(line, "<n>")
}

// Filter narrows a view to the calls since a time, all of them for the zero time, and to its non-empty fields.
type Filter struct {
	Since                          time.Time
	Project, Worktree, File, Group string
	// Kind is the command and its flags, as the usage view lists them.
	Kind string
	// Code is an exit code, or "failed" for any code but 0.
	Code string
	// Before keeps the calls with a smaller id, 0 every call: the calls view lists the next page from the last id.
	Before int64
	// ID keeps the one call, 0 every call.
	ID int64
}

// PageSize is how many calls the calls view lists at once, and how many files the files view lists.
const PageSize = 100

// views are the report's queries over c, the filtered calls with their kind. Days are local by the current UTC offset.
var views = map[string]string{
	"summary": `SELECT count(*) AS calls, coalesce(sum(exit_code <> 0), 0) AS failed, count(DISTINCT project) AS projects,
		count(DISTINCT nullif(worktree, '')) AS worktrees, count(DISTINCT nullif(file, '')) AS files,
		count(DISTINCT CASE WHEN exit_code <> 0 THEN error_group END) AS groups, min(time) AS first, max(time) AS last
		FROM c`,
	"days": `SELECT day, exit_code AS code, count(*) AS calls FROM c GROUP BY day, code ORDER BY day, code`,
	"projects": `SELECT project, count(*) AS calls, sum(exit_code <> 0) AS failed, count(DISTINCT nullif(worktree, ''))
		AS worktrees, sum(command = 'show') AS show, sum(command = 'edit') AS edit, sum(command = 'write') AS write,
		max(time) AS last FROM c GROUP BY project ORDER BY calls DESC, project`,
	"worktrees": `SELECT project, worktree, branch, config, count(*) AS calls, sum(exit_code <> 0) AS failed,
		max(time) AS last FROM c GROUP BY project, worktree, branch, config ORDER BY calls DESC, worktree`,
	"usage": `SELECT kind, exit_code AS code, count(*) AS calls FROM c GROUP BY kind, code ORDER BY kind, code`,
	"errors": `SELECT error_group AS "group", max(exit_code) AS code, count(*) AS calls, count(DISTINCT project) AS projects,
		count(DISTINCT nullif(file, '')) AS files, min(time) AS first, max(time) AS last FROM c
		WHERE exit_code <> 0 GROUP BY error_group ORDER BY calls DESC, error_group`,
	"files": `SELECT file, project, worktree, count(*) AS calls, sum(exit_code <> 0) AS failed, max(time) AS last FROM c
		WHERE file <> '' GROUP BY file ORDER BY calls DESC, file LIMIT ` + strconv.Itoa(PageSize),
	"calls": `SELECT id, time, kind, exit_code AS code, argv, error, error_group AS "group", cwd, file, project, worktree,
		branch, config FROM c ORDER BY id DESC LIMIT ` + strconv.Itoa(PageSize),
}

// Query reads a view from the database at path: summary, days, projects, worktrees, usage, errors, files or calls.
// Each row maps a column to its value.
func Query(path, view string, f Filter) ([]map[string]any, error) {
	q, ok := views[view]
	if !ok {
		return nil, fmt.Errorf("no view %q", view)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_busy_timeout=200&_query_only=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	since := ""
	if !f.Since.IsZero() {
		since = f.Since.UTC().Format(timeFormat)
	}
	_, offset := time.Now().Zone()
	rs, err := db.Query(`WITH c AS (SELECT *, trim(command || ' ' || flags) AS kind, date(time, ?9) AS day FROM calls
		WHERE time >= ?1 AND (?2 = '' OR project = ?2) AND (?3 = '' OR worktree = ?3) AND (?4 = '' OR file = ?4)
		AND (?5 = '' OR error_group = ?5) AND (?6 = '' OR trim(command || ' ' || flags) = ?6)
		AND (?7 = '' OR ?7 = 'failed' AND exit_code <> 0 OR exit_code = ?7) AND (?8 = 0 OR id < ?8)
		AND (?10 = 0 OR id = ?10)) `+q,
		since, f.Project, f.Worktree, f.File, f.Group, f.Kind, f.Code, f.Before, fmt.Sprintf("%+d seconds", offset), f.ID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = vals[i]
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

//go:embed report.html
var page string

// Handler serves the report page and, at /api/<view>, each view as JSON over the database at path. The query
// parameters days, project, worktree, file, group, kind, code, before and id fill the Filter. Only requests to 127.0.0.1
// or localhost are served, so a web page cannot reach the report through a name that resolves to this machine.
func Handler(path string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, page)
	})
	mux.HandleFunc("GET /api/{view}", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := Filter{Project: q.Get("project"), Worktree: q.Get("worktree"), File: q.Get("file"), Group: q.Get("group"),
			Kind: q.Get("kind"), Code: q.Get("code")}
		if days, err := strconv.Atoi(q.Get("days")); err == nil && days > 0 {
			f.Since = time.Now().AddDate(0, 0, -days)
		}
		f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
		f.ID, _ = strconv.ParseInt(q.Get("id"), 10, 64)
		rows, err := Query(path, r.PathValue("view"), f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rows)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, _, _ := net.SplitHostPort(r.Host); host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
