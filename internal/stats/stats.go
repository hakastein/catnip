// Package stats records every catnip call in a SQLite database and reports on it for the person running the agents.
package stats

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hakastein/catnip/internal/config"
)

// Limit is the database size past which Record drops argv of the oldest calls with codes 0 and 3.
const Limit = 100 << 20

// Call is one catnip call.
type Call struct {
	Time time.Time
	// Argv is the arguments after the program name; the first one is the command.
	Argv []string
	// File is the file the call is about as given, "" when the arguments name none.
	File string
	// Flags are the recognized flags, one per occurrence.
	Flags []string
}

// Report is what Collect reads for Render, every section as rows of columns.
type Report struct {
	Summary  [][]any // calls, projects, first and last call time in UTC
	Days     [][]any // local day, command (show, edit, write or other), calls
	Projects [][]any // git root, catnip root, calls, show, edit, write, exit code not 0
	Dirs     [][]any // cwd, calls
	Codes    [][]any // command, exit code, calls
	Flags    [][]any // flag, uses
	Failures [][]any // time in UTC, command, cwd, file, exit code, argv: the last 50 calls with codes 1 and 2
	Commands [][]any // command that catnip does not have, calls
}

const schema = `
CREATE TABLE IF NOT EXISTS calls (
  id          INTEGER PRIMARY KEY,
  time        TEXT NOT NULL,
  command     TEXT NOT NULL,
  cwd         TEXT NOT NULL,
  file        TEXT NOT NULL,
  git_root    TEXT NOT NULL,
  catnip_root TEXT NOT NULL,
  exit_code   INTEGER NOT NULL,
  argv        TEXT
);
CREATE TABLE IF NOT EXISTS flags (
  call_id INTEGER NOT NULL REFERENCES calls(id),
  name    TEXT NOT NULL
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

// Record adds the call and its exit code to the database at path, creating it. The project roots are looked up from
// the file's directory, or from the working directory without a file. Past limit bytes it drops argv of the oldest
// tenth of calls with codes 0 and 3.
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
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO calls (time, command, cwd, file, git_root, catnip_root, exit_code, argv)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, c.Time.UTC().Format(timeFormat), command, cwd, file,
		config.Find(dir, ".git"), config.Find(dir, config.ProjectFile), code, strings.TrimSuffix(argv.String(), "\n"))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for _, f := range c.Flags {
		if _, err := tx.Exec(`INSERT INTO flags (call_id, name) VALUES (?, ?)`, id, f); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
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

// Collect reads the report from the database at path over the calls since the given time, all of them for the zero
// time. Days are local by the current UTC offset.
func Collect(path string, since time.Time) (*Report, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_busy_timeout=200&_query_only=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	from := ""
	if !since.IsZero() {
		from = since.UTC().Format(timeFormat)
	}
	_, offset := time.Now().Zone()
	const kind = `CASE WHEN command IN ('show', 'edit', 'write') THEN command ELSE 'other' END`
	r := &Report{}
	for _, q := range []struct {
		rows *[][]any
		sql  string
	}{
		{&r.Summary, `SELECT count(*), count(DISTINCT CASE WHEN git_root || catnip_root <> '' THEN git_root || char(0) || catnip_root END),
			coalesce(min(time), ''), coalesce(max(time), '') FROM calls WHERE time >= ?1`},
		{&r.Days, `SELECT date(time, '` + fmt.Sprintf("%+d", offset) + ` seconds') AS day, ` + kind + ` AS k, count(*) FROM calls
			WHERE time >= ?1 GROUP BY day, k ORDER BY day, k`},
		{&r.Projects, `SELECT git_root, catnip_root, count(*), sum(command = 'show'), sum(command = 'edit'), sum(command = 'write'),
			sum(exit_code <> 0) FROM calls WHERE time >= ?1 GROUP BY git_root, catnip_root ORDER BY count(*) DESC, git_root, catnip_root`},
		{&r.Dirs, `SELECT cwd, count(*) FROM calls WHERE time >= ?1 GROUP BY cwd ORDER BY count(*) DESC, cwd LIMIT 50`},
		{&r.Codes, `SELECT ` + kind + ` AS k, exit_code, count(*) FROM calls WHERE time >= ?1 GROUP BY k, exit_code ORDER BY k, exit_code`},
		{&r.Flags, `SELECT f.name, count(*) FROM flags f JOIN calls c ON c.id = f.call_id WHERE c.time >= ?1
			GROUP BY f.name ORDER BY count(*) DESC, f.name`},
		{&r.Failures, `SELECT time, command, cwd, file, exit_code, coalesce(argv, '') FROM calls
			WHERE time >= ?1 AND exit_code IN (1, 2) ORDER BY id DESC LIMIT 50`},
		{&r.Commands, `SELECT command, count(*) FROM calls WHERE time >= ?1
			AND command NOT IN ('show', 'edit', 'write', 'help', '-h', '--help', 'stats') GROUP BY command ORDER BY count(*) DESC, command`},
	} {
		if *q.rows, err = rows(db, q.sql, from); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func rows(db *sql.DB, query string, args ...any) ([][]any, error) {
	rs, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	out := [][]any{}
	for rs.Next() {
		row := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range row {
			ptrs[i] = &row[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

//go:embed report.html
var page string

var report = template.Must(template.New("report").Parse(page))

// Render writes the report as one HTML page; its charts load Chart.js from jsDelivr.
func Render(w io.Writer, r *Report) error {
	return report.Execute(w, r)
}
