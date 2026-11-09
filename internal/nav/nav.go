// Package nav answers where a symbol is declared: by a position in a file with an expression from its line, or by
// a name; and which lines of a file show its declarations. The answers come from the provider of the file's language
// and print as lines with catnip addresses.
package nav

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	agentlsp "github.com/hakastein/agent-lsp"
	"github.com/hakastein/agent-lsp/golang"

	"github.com/hakastein/catnip/internal/edit"
	"github.com/hakastein/catnip/internal/view"
)

// provider answers questions about the code of one root.
type provider interface {
	Definition(pos agentlsp.Position) ([]agentlsp.Declaration, error)
	Symbols(name string) ([]agentlsp.Symbol, error)
	Outline(file string) ([]agentlsp.Symbol, error)
	Close() error
}

// Question is one question of def: a position with an expression from its line, or a Name.
type Question struct {
	Text   string // the question as given
	Path   string
	Line   int
	Column int    // 0 when the expression stands on the line once
	Hash   string // the hash of the line in an address <line>#<hash>; "" without one
	Expr   string
	Name   string
}

// Parse reads questions: pairs <position> <expression>, where the position is <path>:<line>, <path>:<line>:<column>
// or <path>:<line>#<hash>, and names, which are the arguments that are not a position.
func Parse(args []string) ([]Question, error) {
	var qs []Question
	for i := 0; i < len(args); i++ {
		q, err := position(args[i])
		if err != nil {
			return nil, err
		}
		if q.Path == "" {
			if args[i] == "" {
				return nil, errors.New(`"": the name is empty`)
			}
			qs = append(qs, Question{Text: args[i], Name: args[i]})
			continue
		}
		if i+1 == len(args) {
			return nil, fmt.Errorf("%s: want an expression from the line after the position", args[i])
		}
		i++
		q.Text, q.Expr = args[i-1]+" "+args[i], args[i]
		if lastWord(q.Expr) == nil {
			return nil, fmt.Errorf("%s %q: the expression has no name", args[i-1], args[i])
		}
		qs = append(qs, q)
	}
	if len(qs) == 0 {
		return nil, errors.New("def needs a position and an expression, or a name")
	}
	return qs, nil
}

// position reads a position as show and edit take it; a Question without a Path when s is not a position.
func position(s string) (Question, error) {
	t, err := view.ParseTarget(s, 0)
	if err != nil || t.Line == 0 {
		return Question{}, err
	}
	if t.Column == 0 && strings.Count(s[len(t.Path):], ":") == 2 { // show takes column 0; it covers no expression
		return Question{}, fmt.Errorf("%s: columns start at 1", s)
	}
	return Question{Path: t.Path, Line: t.Line, Column: t.Column, Hash: t.Hash}, nil
}

// Navigator answers questions from the directory cwd; it keeps one provider open, for the root of the last question.
type Navigator struct {
	cwd  string
	root string
	ix   provider
}

// New is a Navigator in cwd, an absolute directory: names are looked up in its code and paths print relative to it.
func New(cwd string) *Navigator { return &Navigator{cwd: cwd} }

// Close closes the open provider.
func (n *Navigator) Close() error {
	if n.ix == nil {
		return nil
	}
	return n.ix.Close()
}

// Def answers the questions in order. Each answer is a block: the question, then the lines of its declarations
// as <path>:<line>#<hash>: <text>, the doc comment and the signature, with body the whole declaration. Blocks are
// separated by an empty line. A question without an answer keeps its line, and the reason goes to errw. Def reports
// whether every question was answered.
func (n *Navigator) Def(qs []Question, body bool, out, errw io.Writer) bool {
	answered := true
	for i, q := range qs {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, q.Text)
		text, err := n.def(q, body)
		if err != nil {
			fmt.Fprintf(errw, "%s: %v\n", q.Text, err)
			answered = false
			continue
		}
		fmt.Fprint(out, text)
	}
	return answered
}

func (n *Navigator) def(q Question, body bool) (string, error) {
	var decls []agentlsp.Declaration
	if q.Name != "" {
		ix, err := n.open(n.cwd) // shortcut: names are looked up in Go code only; PHP adds a way to choose
		if err != nil {
			return "", err
		}
		syms, err := ix.Symbols(q.Name)
		if err != nil {
			return "", err
		}
		for _, s := range syms {
			decls = append(decls, s.Declaration)
		}
	} else {
		if err := goFile(q.Path); err != nil {
			return "", err
		}
		pos, err := locate(q)
		if err != nil {
			return "", err
		}
		ix, err := n.open(filepath.Dir(pos.File))
		if err != nil {
			return "", err
		}
		if decls, err = ix.Definition(pos); err != nil {
			return "", err
		}
	}
	if len(decls) == 0 {
		return "", errors.New("no declaration found")
	}
	var b strings.Builder
	for _, d := range decls {
		if err := n.render(&b, d, body); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// Show is what show prints for t, a target with a Symbol, or the whole file with outline: the declarations named
// t.Symbol in the Go file (Func, Type, Type.Method, Type.Field), each from its doc comment to its end, or the line
// of each declaration in the file.
func (n *Navigator) Show(t view.Target, outline bool) ([]view.Target, error) {
	if err := goFile(t.Path); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(t.Path)
	if err != nil {
		return nil, err
	}
	ix, err := n.open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	syms, err := ix.Outline(path)
	if err != nil {
		return nil, err
	}
	var ts []view.Target
	for _, s := range syms {
		switch {
		case outline && (len(ts) == 0 || ts[len(ts)-1].From != s.Line): // names declared on one line share it
			ts = append(ts, view.Target{Path: t.Path, From: s.Line, To: s.Line})
		case !outline && (t.Symbol == s.Name || t.Symbol == s.Container+"."+s.Name):
			from, to := declLines(s.Declaration, true)
			ts = append(ts, view.Target{Path: t.Path, From: from, To: to})
		}
	}
	if !outline && len(ts) == 0 {
		return nil, fmt.Errorf("no symbol %q in %s; catnip show %s --outline lists its declarations", t.Symbol, t.Path, t.Path)
	}
	return ts, nil
}

// goFile fails for a path that is not of a Go file, the only language navigation knows.
func goFile(path string) error {
	if ext := filepath.Ext(path); ext != ".go" {
		return fmt.Errorf("no navigation in %q files: catnip knows Go (.go)", ext)
	}
	return nil
}

// open is the Go provider of the root of dir; a provider of another root is closed first, as a process has one Go
// index open at a time.
func (n *Navigator) open(dir string) (provider, error) {
	root, err := goRoot(dir)
	if err != nil {
		return nil, err
	}
	if root == n.root {
		return n.ix, nil
	}
	if err := n.Close(); err != nil {
		return nil, err
	}
	n.ix, n.root = nil, ""
	ix, err := golang.Open(root, nil)
	if err != nil {
		return nil, err
	}
	n.ix, n.root = ix, root
	return ix, nil
}

// goRoot is the directory of the go.work nearest above dir, or else of the nearest go.mod, as the go command finds
// them. shortcut: the GOWORK variable is not read; read it when a go.work has to be skipped.
func goRoot(dir string) (string, error) {
	mod := ""
	for d := dir; ; d = filepath.Dir(d) {
		if exists(filepath.Join(d, "go.work")) {
			return d, nil
		}
		if mod == "" && exists(filepath.Join(d, "go.mod")) {
			mod = d
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if mod == "" {
		return "", fmt.Errorf("no go.mod or go.work in %s or above", dir)
	}
	return mod, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// locate is the position of the last word of the question's expression on its line. The line's hash must match;
// the expression must stand on the line once, or once over the column.
func locate(q Question) (agentlsp.Position, error) {
	path, err := filepath.Abs(q.Path)
	if err != nil {
		return agentlsp.Position{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return agentlsp.Position{}, err
	}
	text, _ := edit.Decode(raw)
	lines := edit.Lines(text)
	if q.Hash != "" || q.Line > len(lines) { // CheckLine also names a line past the end
		if reason := edit.CheckLine(lines, q.Line, q.Hash); reason != "" {
			return agentlsp.Position{}, errors.New(reason)
		}
	}
	line := lines[q.Line-1]
	var at []string // the columns of the expression on the line
	for i := 0; ; {
		j := strings.Index(line[i:], q.Expr)
		if j < 0 {
			break
		}
		start := i + j
		if (q.Column == 0 || start < q.Column && q.Column <= start+len(q.Expr)) && whole(line, start, q.Expr) {
			at = append(at, strconv.Itoa(start+1))
		}
		i = start + 1
	}
	where := fmt.Sprintf("on line %d", q.Line)
	if q.Column > 0 {
		where = fmt.Sprintf("at line %d column %d", q.Line, q.Column)
	}
	switch {
	case len(at) == 0:
		return agentlsp.Position{}, fmt.Errorf("%q is not %s: %d#%s\t%s", q.Expr, where, q.Line, edit.Hash(line), line)
	case len(at) > 1:
		return agentlsp.Position{}, fmt.Errorf("%q stands %s %d times, at columns %s; give the column as %s:%d:<column>: %d#%s\t%s",
			q.Expr, where, len(at), strings.Join(at, ", "), q.Path, q.Line, q.Line, edit.Hash(line), line)
	}
	start, _ := strconv.Atoi(at[0])
	return agentlsp.Position{File: path, Line: q.Line, Column: start + lastWord(q.Expr)[0]}, nil
}

var word = regexp.MustCompile(`[\pL\pN_]+`)

// lastWord is the byte range of the last word of expr; nil for none.
func lastWord(expr string) []int {
	all := word.FindAllStringIndex(expr, -1)
	if all == nil {
		return nil
	}
	return all[len(all)-1]
}

// whole reports whether expr at start of line is not a part of a longer word.
func whole(line string, start int, expr string) bool {
	before, _ := utf8.DecodeLastRuneInString(line[:start])
	after, _ := utf8.DecodeRuneInString(line[start+len(expr):])
	first, _ := utf8.DecodeRuneInString(expr)
	last, _ := utf8.DecodeLastRuneInString(expr)
	return !(isWord(first) && isWord(before)) && !(isWord(last) && isWord(after))
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// render writes the lines of d: its doc comment and signature, with body the whole declaration. A declaration
// outside the code of the root prints with its absolute path.
func (n *Navigator) render(b *strings.Builder, d agentlsp.Declaration, body bool) error {
	raw, err := os.ReadFile(d.File)
	if err != nil {
		return err
	}
	text, _ := edit.Decode(raw)
	lines := edit.Lines(text)
	from, to := declLines(d, body)
	path := d.File
	if d.Origin != agentlsp.OriginStd && d.Origin != agentlsp.OriginModule {
		path = n.rel(path)
	}
	for l := from; l <= to; l++ {
		fmt.Fprintf(b, "%s:%d#%s: %s\n", path, l, edit.Hash(lines[l-1]), lines[l-1])
	}
	return nil
}

// declLines is the lines of d: from its doc comment to the end of its signature, with body to its end.
func declLines(d agentlsp.Declaration, body bool) (from, to int) {
	from, to = d.Range.Line, d.Range.Line+strings.Count(d.Signature, "\n")
	if d.Doc != "" {
		from -= strings.Count(d.Doc, "\n") + 1
	}
	if body {
		to = d.Range.EndLine
	}
	return from, to
}

// rel is path relative to the directory of the navigator, or path itself when it has none, as on another drive.
func (n *Navigator) rel(path string) string {
	if r, err := filepath.Rel(n.cwd, path); err == nil {
		return r
	}
	return path
}
