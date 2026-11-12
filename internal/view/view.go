// Package view renders file lines with numbers, content hashes and blame.
package view

import (
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hakastein/catnip/internal/edit"
)

// Target is what to show: the whole file, lines From..To, the lines around Line, or the declarations of Symbol.
type Target struct {
	Path     string
	From, To int    // 0, 0 for the whole file
	Line     int    // the asked position; 0 for a range
	Column   int    // the column of the asked position; 0 without one
	Hash     string // the hash Line must have, from an address <line>#<hash>; "" without one
	Symbol   string // the name of a declaration in the file, from <path>::<symbol>; "" without one
}

var suffix = regexp.MustCompile(`^(.+?):(\d+)(?:-(\d+)|:(\d+)|(#.*))?$`)

// ParseTarget reads path, path:from-to, path:line, path:line:column (1-based, as positions in LSP tools print them)
// path:line#hash, the address catnip prints, or path::symbol. A position shows context lines on each side. The symbol
// is Name or Type.Name of Go identifiers after the last ::; otherwise :: is part of the path.
func ParseTarget(s string, context int) (Target, error) {
	if i := strings.LastIndex(s, "::"); i > 0 && isSymbol(s[i+2:]) {
		if suffix.MatchString(s[:i]) {
			return Target{}, fmt.Errorf("%s: want <path>::<symbol>, a symbol takes no line", s)
		}
		return Target{Path: s[:i], Symbol: s[i+2:]}, nil
	}
	m := suffix.FindStringSubmatch(s)
	if m == nil {
		return Target{Path: s}, nil
	}
	a, _ := strconv.Atoi(m[2])
	if a < 1 {
		return Target{}, fmt.Errorf("%s: lines start at 1", s)
	}
	if m[3] != "" {
		b, _ := strconv.Atoi(m[3])
		if b < a {
			return Target{}, fmt.Errorf("%s: range ends before it starts", s)
		}
		return Target{Path: m[1], From: a, To: b}, nil
	}
	t := Target{Path: m[1], From: max(1, a-context), To: a + context, Line: a}
	t.Column, _ = strconv.Atoi(m[4])
	if m[5] != "" {
		addr := m[2] + m[5]
		if strings.Contains(addr, "-") {
			return Target{}, fmt.Errorf("%s: %q is a range, want one LINE#HASH", s, addr)
		}
		r, err := edit.ParseRange(s, "LINE#HASH", addr)
		if err != nil {
			return Target{}, err
		}
		t.Hash = r.FromHash
	}
	return t, nil
}

// isSymbol reports whether s is Name or Type.Name, as show takes a symbol after ::.
func isSymbol(s string) bool {
	typ, name, found := strings.Cut(s, ".")
	return token.IsIdentifier(typ) && (!found || token.IsIdentifier(name))
}

// Gutters are what Render prints before each line: its address <n>#<hash>, its number, or nothing.
var Gutters = []string{"hash", "line", "none"}

// Render prints the target's lines of decoded text as "[<gutter>\t][<blame>\t]<line>", gutter one of Gutters.
// blame, when given, holds one annotation per line of the target.
func Render(text string, t Target, gutter string, blame []string) (string, error) {
	lines := edit.Lines(text)
	from, to, err := t.Lines(len(lines))
	if err != nil {
		return "", err
	}
	if t.Hash != "" {
		if reason := edit.CheckLine(lines, t.Line, t.Hash); reason != "" {
			return "", errors.New(reason)
		}
	}
	width := len(strconv.Itoa(to))
	var b strings.Builder
	for n := from; n <= to; n++ {
		if gutter != "none" {
			fmt.Fprintf(&b, "%*d", width, n)
			if gutter == "hash" {
				b.WriteString("#" + edit.Hash(lines[n-1]))
			}
			b.WriteString("\t")
		}
		if blame != nil {
			b.WriteString(blame[n-from] + "\t")
		}
		b.WriteString(lines[n-1] + "\n")
	}
	return b.String(), nil
}

// Lines is the 1-based range of t in a file of n lines. A position or the start of a range past the end is an error;
// a range and the context of a position are cut at the end.
func (t Target) Lines(n int) (int, int, error) {
	switch {
	case t.From == 0:
		return 1, n, nil
	case t.Line > n:
		return 0, 0, fmt.Errorf("line %d is past the end: %s has %d lines", t.Line, t.Path, n)
	case t.Line == 0 && t.From > n:
		return 0, 0, fmt.Errorf("line %d is past the end: %s has %d lines", t.From, t.Path, n)
	}
	return t.From, min(t.To, n), nil
}

// Cut is the warning that Lines cuts the range t at the end of a file of n lines; "" when it does not.
func (t Target) Cut(n int) string {
	if t.Line != 0 || t.To <= n || t.From > n {
		return ""
	}
	return fmt.Sprintf("line %d is past the end: %s has %d lines, printed %d-%d", t.To, t.Path, n, t.From, n)
}

// Read is the content of path, or of path as it was in the git revision rev when rev is not "".
func Read(path, rev string) ([]byte, error) {
	if rev == "" {
		return os.ReadFile(path)
	}
	out, err := git(path, "show", rev+":./"+filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", rev, path, err)
	}
	return out, nil
}

// git runs git with args in the directory of path and returns its stdout, or its stderr as the error.
func git(path string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", filepath.Dir(path)}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return out, nil
}

// Blame annotates lines from..to of path, as it is or as it was in the git revision rev when rev is not "", as
// "<commit> <author> <date>" from git.
func Blame(path, rev string, from, to int) ([]string, error) {
	if from > to {
		return []string{}, nil
	}
	args := []string{"blame", "--line-porcelain", "-L", fmt.Sprintf("%d,%d", from, to)}
	if rev != "" {
		args = append(args, rev)
	}
	out, err := git(path, append(args, "--", filepath.Base(path))...)
	if err != nil {
		return nil, fmt.Errorf("git blame: %w", err)
	}
	type entry struct{ commit, author, date string }
	var entries []entry
	var cur entry
	authorWidth := 0
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "\t"):
			entries = append(entries, cur)
			authorWidth = max(authorWidth, len([]rune(cur.author)))
			cur = entry{}
		case cur.commit == "":
			cur.commit, _, _ = strings.Cut(l, " ")
		case strings.HasPrefix(l, "author "):
			cur.author = strings.TrimPrefix(l, "author ")
		case strings.HasPrefix(l, "author-time "):
			sec, _ := strconv.ParseInt(strings.TrimPrefix(l, "author-time "), 10, 64)
			cur.date = time.Unix(sec, 0).Format(time.DateOnly)
		}
	}
	res := make([]string, len(entries))
	for i, e := range entries {
		pad := strings.Repeat(" ", authorWidth-len([]rune(e.author)))
		res[i] = fmt.Sprintf("%.8s %s%s %s", e.commit, e.author, pad, e.date)
	}
	return res, nil
}
