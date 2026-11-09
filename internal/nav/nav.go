// Package nav answers where a symbol is declared and where it is used: by a position in a file with an expression
// from its line, or by a name; and which lines of a file show its declarations. The answers come from the provider
// of the file's language and print as lines with catnip addresses. It also renames a symbol in every file of its
// project and writes them as edit does.
package nav

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	agentlsp "github.com/hakastein/agent-lsp"
	"github.com/hakastein/agent-lsp/golang"

	"github.com/hakastein/catnip/internal/check"
	"github.com/hakastein/catnip/internal/config"
	"github.com/hakastein/catnip/internal/edit"
	"github.com/hakastein/catnip/internal/store"
	"github.com/hakastein/catnip/internal/view"
)

// provider answers questions about the code of one root.
type provider interface {
	Definition(pos agentlsp.Position) ([]agentlsp.Declaration, error)
	References(pos agentlsp.Position) (agentlsp.References, error)
	Rename(pos agentlsp.Position, name string) (agentlsp.Renaming, error)
	Symbols(name string) ([]agentlsp.Symbol, error)
	Outline(file string) ([]agentlsp.Symbol, error)
	Origin(path string) agentlsp.Origin
	Close() error
}

// Question is one question of def or ref: a position with an expression from its line, or a Name.
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
// or <path>:<line>#<hash>, and names, which are the arguments that are not a position. cmd is the command that asks.
func Parse(cmd string, args []string) ([]Question, error) {
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
		return nil, fmt.Errorf("%s needs a position and an expression, or a name", cmd)
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
	return n.answer(qs, out, errw, func(q Question) (string, error) { return n.def(q, body) })
}

// Ref answers the questions in blocks, as Def does. The lines of an answer are the uses of the symbol, then after
// the line "implementations:" the types and methods that implement it. Each group goes by file, the files with most
// lines first; places on one line print as one line. At most limit lines print: first a line of each file, then
// the rest in order; a group with lines left out ends with the line "<n> more lines in <m> files".
func (n *Navigator) Ref(qs []Question, limit int, out, errw io.Writer) bool {
	return n.answer(qs, out, errw, func(q Question) (string, error) { return n.ref(q, limit) })
}

// answer prints the block of each question with the lines from answer, or keeps its line and prints the reason to
// errw; it reports whether every question was answered.
func (n *Navigator) answer(qs []Question, out, errw io.Writer, answer func(Question) (string, error)) bool {
	answered := true
	for i, q := range qs {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, q.Text)
		text, err := answer(q)
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
		_, syms, err := n.symbols(q.Name)
		if err != nil {
			return "", err
		}
		for _, s := range syms {
			decls = append(decls, s.Declaration)
		}
	} else {
		ix, pos, err := n.at(q)
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
		from, to := declLines(d, body)
		lines, err := source(d.File)
		if err != nil {
			return "", err
		}
		for l := from; l <= to; l++ {
			n.line(&b, d.File, lines, l)
		}
	}
	return b.String(), nil
}

func (n *Navigator) ref(q Question, limit int) (string, error) {
	var ix provider
	var pos agentlsp.Position
	var err error
	if q.Name != "" {
		var syms []agentlsp.Symbol
		if ix, syms, err = n.symbols(q.Name); err != nil {
			return "", err
		}
		switch {
		case len(syms) == 0:
			return "", errors.New("no declaration found")
		case len(syms) > 1: // the uses of several symbols in one answer would not say whose they are
			var b strings.Builder
			fmt.Fprintf(&b, "%d declarations; ask about one of them by its address:\n", len(syms))
			for _, s := range syms {
				lines, err := source(s.File)
				if err != nil {
					return "", err
				}
				n.line(&b, s.File, lines, s.Line)
			}
			return "", errors.New(strings.TrimSuffix(b.String(), "\n"))
		}
		pos = agentlsp.Position{File: syms[0].File, Line: syms[0].Line, Column: syms[0].Column}
	} else if ix, pos, err = n.at(q); err != nil {
		return "", err
	}
	refs, err := ix.References(pos)
	if err != nil {
		return "", err
	}
	if len(refs.Uses)+len(refs.Implementations) == 0 {
		return "", errors.New("no uses found")
	}
	groups := []struct {
		head  string
		files []*file
	}{{"", byFile(refs.Uses)}, {"implementations:\n", byFile(refs.Implementations)}}
	shown := map[*file]int{} // the lines of a file that print: first one of each file, then the rest in order
	for _, g := range groups {
		for _, f := range g.files {
			if limit > 0 {
				shown[f], limit = 1, limit-1
			}
		}
	}
	for _, g := range groups {
		for _, f := range g.files {
			more := min(len(f.lines)-shown[f], limit)
			shown[f], limit = shown[f]+more, limit-more
		}
	}
	var b strings.Builder
	for _, g := range groups {
		if len(g.files) > 0 {
			b.WriteString(g.head)
		}
		left, leftFiles := 0, 0
		for _, f := range g.files {
			if k := len(f.lines) - shown[f]; k > 0 {
				left, leftFiles = left+k, leftFiles+1
			}
			if shown[f] == 0 {
				continue
			}
			lines, err := source(f.path)
			if err != nil {
				return "", err
			}
			for _, l := range f.lines[:shown[f]] {
				n.line(&b, f.path, lines, l)
			}
		}
		if left > 0 {
			fmt.Fprintf(&b, "%d more lines in %d files\n", left, leftFiles)
		}
	}
	return b.String(), nil
}

// file is the places of a group of ref in one file.
type file struct {
	path   string
	lines  []int        // the lines with places, in order
	places map[int]bool // the lines with places
}

// byFile is the places of locs by file, the files with most lines first, otherwise in the order of locs.
func byFile(locs []agentlsp.Location) []*file {
	var files []*file
	byPath := map[string]*file{}
	for _, l := range locs {
		f := byPath[l.File]
		if f == nil {
			f = &file{path: l.File, places: map[int]bool{}}
			byPath[l.File] = f
			files = append(files, f)
		}
		if !f.places[l.Line] {
			f.lines = append(f.lines, l.Line)
		}
		f.places[l.Line] = true
	}
	slices.SortStableFunc(files, func(a, b *file) int { return cmp.Compare(len(b.lines), len(a.lines)) })
	return files
}

// Rename renames the symbol of q, a question with a position, to name in every file of its project and returns the
// exit code of catnip. Before any file is written, every edit must replace the old name in the file as it is, and the
// config of every file must load. Then the files are written whole, and for each one its changed lines print as edit
// prints them and the fixers and checks configured for it run. The codes: 0 written, every check passed; 1 nothing
// written: the provider refused, or the position or an edit does not match the file; 2 a config is broken, nothing
// written; 3 written, but a write, a fixer or a check failed, and errw ends with the files written whole.
func (n *Navigator) Rename(q Question, name string, showFixDiff bool, out, errw io.Writer) int {
	files, err := n.renamed(q, name)
	if err != nil {
		fmt.Fprintf(errw, "%s: %v\nnothing written\n", q.Text, err)
		return 1
	}
	cfgs := make([]*config.Config, len(files))
	for i, f := range files {
		if cfgs[i], err = config.Load(filepath.Dir(f.path)); err != nil {
			fmt.Fprintf(errw, "%v\nnothing written\n", err)
			return 2
		}
	}
	var written []string
	for _, f := range files {
		if err = store.Write(f.path, f.after); err != nil {
			break
		}
		written = append(written, f.path)
	}
	if err != nil && len(written) == 0 && !errors.Is(err, store.ErrPartial) {
		fmt.Fprintf(errw, "%v\nnothing written\n", err)
		return 1
	}
	passed := err == nil
	for i, path := range written {
		fmt.Fprint(out, edit.Diff(path, files[i].before, files[i].after))
		passed = check.Report(cfgs[i], path, showFixDiff, out, errw) && passed
	}
	if passed {
		return 0
	}
	if err != nil {
		fmt.Fprintln(errw, err)
	}
	fmt.Fprintf(errw, "written: %s\n", strings.Join(written, ", "))
	return 3
}

// renaming is a file that a rename changes: its content before and after.
type renaming struct {
	path          string
	before, after []byte
}

// renamed is the files that renaming the symbol of q to name changes, by the edits of the provider.
func (n *Navigator) renamed(q Question, name string) ([]renaming, error) {
	ix, pos, err := n.at(q)
	if err != nil {
		return nil, err
	}
	r, err := ix.Rename(pos, name)
	if err != nil {
		return nil, err
	}
	if len(r.Edits) == 0 {
		return nil, errors.New("no symbol to rename")
	}
	w := lastWord(q.Expr)
	old := q.Expr[w[0]:w[1]]
	var files []renaming
	for i, j := 0, 0; i < len(r.Edits); i = j { // the edits come by file
		for j = i; j < len(r.Edits) && r.Edits[j].File == r.Edits[i].File; j++ {
		}
		path := n.rel(r.Edits[i].File)
		before, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		after, err := apply(path, before, r.Edits[i:j], old)
		if err != nil {
			return nil, err
		}
		files = append(files, renaming{path, before, after})
	}
	return files, nil
}

// apply is raw, the content of the file at path, with edits, in order, each of which must replace old on one line.
// Edits address bytes, so the BOM and line endings of the file stay as they are.
func apply(path string, raw []byte, edits []agentlsp.Edit, old string) ([]byte, error) {
	starts := []int{0} // where each line starts
	for i, b := range raw {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	var out []byte
	at := 0
	for _, e := range edits {
		start, end := -1, -1
		if e.Line <= len(starts) && e.EndLine == e.Line {
			start, end = starts[e.Line-1]+e.Column-1, starts[e.Line-1]+e.EndColumn-1
		}
		if start < at || end < start || end > len(raw) || string(raw[start:end]) != old {
			return nil, fmt.Errorf("%s:%d:%d: %q is not there; the file changed after it was read", path, e.Line, e.Column, old)
		}
		out = append(append(out, raw[at:start]...), e.NewText...)
		at = end
	}
	return append(out, raw[at:]...), nil
}

// symbols is the declarations named name in the code of the root of the current directory, with its provider.
func (n *Navigator) symbols(name string) (provider, []agentlsp.Symbol, error) {
	ix, err := n.open(n.cwd) // shortcut: names are looked up in Go code only; PHP adds a way to choose
	if err != nil {
		return nil, nil, err
	}
	syms, err := ix.Symbols(name)
	return ix, syms, err
}

// at is the position of the question's expression and the provider of its file.
func (n *Navigator) at(q Question) (provider, agentlsp.Position, error) {
	if err := goFile(q.Path); err != nil {
		return nil, agentlsp.Position{}, err
	}
	pos, err := locate(q)
	if err != nil {
		return nil, pos, err
	}
	ix, err := n.open(filepath.Dir(pos.File))
	return ix, pos, err
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
	lines, err := source(path)
	if err != nil {
		return agentlsp.Position{}, err
	}
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

// source is the lines of the file at path.
func source(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text, _ := edit.Decode(raw)
	return edit.Lines(text), nil
}

// line writes line l of the file at path, given its lines, as <path>:<l>#<hash>: <text>.
func (n *Navigator) line(b *strings.Builder, path string, lines []string, l int) {
	fmt.Fprintf(b, "%s:%d#%s: %s\n", n.rel(path), l, edit.Hash(lines[l-1]), lines[l-1])
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

// rel is path as an answer prints it: relative to the directory of the navigator for a file of the project (the
// root, the modules of its go.work and its vendor directory, by the Origin of the open provider), otherwise absolute,
// as in the standard library, the module cache or on another drive.
func (n *Navigator) rel(path string) string {
	if o := n.ix.Origin(path); o == agentlsp.OriginRoot || o == agentlsp.OriginVendor {
		if r, err := filepath.Rel(n.cwd, path); err == nil {
			return r
		}
	}
	return path
}
