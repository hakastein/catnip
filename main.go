// catnip shows and edits files for agents from the shell; every command chains with && by its exit code.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/hakastein/catnip/internal/check"
	"github.com/hakastein/catnip/internal/config"
	"github.com/hakastein/catnip/internal/edit"
	"github.com/hakastein/catnip/internal/nav"
	"github.com/hakastein/catnip/internal/stats"
	"github.com/hakastein/catnip/internal/store"
	"github.com/hakastein/catnip/internal/view"
)

// Exit codes.
const (
	ok          = 0 // done, checks passed or skipped
	notApplied  = 1 // nothing written
	badUsage    = 2 // bad arguments or config
	checkFailed = 3 // written, but a fixer or check failed or the file was written in part
)

const exitCodes = `
exit: 0 done, checks passed or skipped; 1 nothing written; 2 bad arguments or config;
  3 written, but a fixer or check failed or the file was written in part
`

// editOps are the edits of catnip edit, as both helps list them.
const editOps = `  --replace OLD NEW [--count N]    replace the text OLD found exactly once, or exactly N times
  --lines FROM#HASH[-TO#HASH] NEW  replace whole lines, addressed as catnip show prints them
  --after LINE#HASH NEW            insert lines after the line
  --before LINE#HASH NEW           insert lines before the line
  an empty NEW deletes OLD or the lines; --after and --before need a non-empty NEW
  a line break ending NEW of --lines, --after or --before ends its last line: one empty line is $'\n'
`

const usage = `catnip shows and edits files from the shell; commands chain with && by their exit codes.

catnip show [--rev <rev>] [--outline] <path>[:<from>-<to> | :<line>[:<column> | #<hash>] | ::<symbol>]...
  print the file, its lines <from> to <to>, line <line> with 5 lines on each side, or the
  Go declaration <symbol> (Func, Type, Type.Method) from its doc comment to its end;
  --outline prints the line of each declaration of a Go file; --rev prints the file as it
  was in a git revision; several targets print one block each; by default each line comes
  after its address <line>#<hash>, which catnip edit takes

catnip edit [--skip-checks] <path> <edit>...
  apply the edits to the file: all of them, or none if any does not match; every edit
  addresses the file as it was before the command; OLD and NEW may span several lines
` + editOps + `
catnip write [--skip-checks] <path> < content
  replace the file with stdin, creating it if missing

catnip def [--body] <path>:<line>[:<column> | #<hash>] <expression>... | <name>...
  print where the Go symbol at the last word of the expression from the line is declared,
  or the one named <name> (Func, Type.Method, pkg.Func): its doc comment and signature,
  with --body the whole declaration, each line after its address; one block per question;
  exit 1 when a question has no answer

catnip ref [--limit <n>] <path>:<line>[:<column> | #<hash>] <expression>... | <name>...
  print the uses of the Go symbol at the last word of the expression from the line, or of
  the one named <name>, and the types and methods that implement it, by file, each line
  after its address; one block per question; exit 1 when a question has no answer

catnip rename [--show-fix-diff] <path>:<line>[:<column> | #<hash>] <expression> <new name>
  rename the Go symbol at the last word of the expression from the line in every file of
  its project; nothing is written unless every edit matches; prints what changed and runs
  the fixers and checks configured for each changed file

catnip stats
  open a report on recorded catnip calls in the browser

After edit, write and rename catnip prints what changed and runs the fixers and checks
configured for the file; --skip-checks of edit and write skips them.
Flags go right after the command name, as above; after all its arguments is accepted too.
catnip <command> --help prints all flags of the command.
` + exitCodes

const showHelp = `catnip show prints a file, a range of its lines, the lines around a position or the
declarations of a Go symbol. Without flags each line comes after its address <line>#<hash>, which
catnip edit takes.

usage: catnip show [flags] <path>[:<from>-<to> | :<line>[:<column> | #<hash>] | ::<symbol>]...
  <path>                    the whole file
  <path>:<from>-<to>        lines <from> to <to>; a range past the end of the file prints to
                            its last line and warns on stderr
  <path>:<line>[:<column>]  line <line> with 5 lines on each side
  <path>:<line>#<hash>      the same by the address of the line; it must not have changed
  <path>::<symbol>          each Go declaration named <symbol> in the file (Func, Type,
                            Type.Method, Type.Field) from its doc comment to its end;
                            declarations are separated by an empty line
Several targets print one after another, each block after a line with the target as given,
blocks separated by an empty line; a target that does not print gives its reason on stderr.

flags:
  -C N           lines on each side of a position instead of 5
  --outline      the line of each declaration of a Go file: functions, methods, types,
                 fields, variables and constants
  --gutter line  only the line number, no hash
  --gutter none  no address
  --blame        the commit, author and date of each line
  --rev <rev>    the file as it was in the git revision <rev>, with its addresses and blame;
                 not with ::<symbol> or --outline

exit: 0 printed; 1 a target not printed: no file, the line changed, no declaration <symbol>,
  not a Go file; 2 bad arguments
`

const editHelp = `catnip edit applies edits to one file: all of them, or none if any does not match.
Every edit addresses the file as it was before the command. catnip prints the changed lines, then
runs the fixers and checks configured for the file.

usage: catnip edit [flags] <path> <edit>...

edits:
` + editOps + `
flags, right after edit; also accepted after the path and between the edits:
  --show-fix-diff  also print what each fixer changed
  --skip-checks    run no fixers and checks: for a file not meant to pass them yet
` + exitCodes

const writeHelp = `catnip write writes stdin to a file, creating it if missing. Overwriting keeps the BOM, line
endings and final newline of the file. Then catnip runs the fixers and checks configured for it.

usage: catnip write [flags] <path> < content

flags:
  --show-fix-diff  also print what each fixer changed
  --skip-checks    run no fixers and checks: for a file not meant to pass them yet
` + exitCodes

const defHelp = `catnip def prints where a Go symbol is declared: its doc comment and signature, each line as
<path>:<line>#<hash>: <text>, the address catnip show and edit take. The symbol is the last word of an
expression from the line of a position, or a name. Each question gets a block: the question, then the
lines of its declarations; blocks are separated by an empty line. Paths are relative to the current
directory; a declaration outside the project has an absolute path.

usage: catnip def [--body] <question>...
  <path>:<line> <expression>           the expression stands on the line once
  <path>:<line>:<column> <expression>  the expression that covers the column, in bytes from 1
  <path>:<line>#<hash> <expression>    the line by its address; it must not have changed
  <name>                               Func, Type.Method, pkg.Func, pkg.Type.Method; a bare
                                       name matches the last segment of qualified names

flags:
  --body  print the whole declaration

exit: 0 every question answered; 1 a question has no answer: the line changed, the expression
  is not on it or stands there more than once, no declaration; the other questions are answered;
  2 bad arguments
`

const refHelp = `catnip ref prints the uses of a Go symbol, each line as <path>:<line>#<hash>: <text>, the
address catnip show and edit take, then after the line "implementations:" the types and methods
that implement an interface type or a method of one. The symbol is the last word of an expression
from the line of a position, or a name. Each question gets a block: the question, then the lines
of each group by file, the files with most lines first; places on one line print as one line.
Lines left out end the group as "<n> more lines in <m> files". Blocks are separated by an empty
line. Paths are relative to the current directory; a file outside the project has an absolute
path.

usage: catnip ref [--limit <n>] <question>...
  <path>:<line> <expression>           the expression stands on the line once
  <path>:<line>:<column> <expression>  the expression that covers the column, in bytes from 1
  <path>:<line>#<hash> <expression>    the line by its address; it must not have changed
  <name>                               Func, Type.Method, pkg.Func, pkg.Type.Method; a bare
                                       name matches the last segment of qualified names and
                                       must name one declaration

flags:
  --limit <n>  lines of an answer instead of 60: first a line of each file, then the rest

exit: 0 every question answered; 1 a question has no answer: the line changed, the expression
  is not on it or stands there more than once, the name names no declaration or several, no
  uses; the other questions are answered; 2 bad arguments
`

const renameHelp = `catnip rename renames a Go symbol and its uses in every file of its project, the
files of every build platform included; renaming an interface method renames the methods of the
types the code converts to the interface. The symbol is the last word of an expression from the
line of a position. Before any file is written every edit must match the file, so a refused
rename writes nothing. Each file is written whole, keeping its BOM and line endings; then catnip
prints its changed lines and runs the fixers and checks configured for it.

usage: catnip rename [flags] <position> <expression> <new name>
  <path>:<line> <expression>           the expression stands on the line once
  <path>:<line>:<column> <expression>  the expression that covers the column, in bytes from 1
  <path>:<line>#<hash> <expression>    the line by its address; it must not have changed

flags:
  --show-fix-diff  also print what each fixer changed

exit: 0 written, checks passed; 1 nothing written: the line changed, the expression is not on
  it, no symbol, the new name conflicts, the symbol is declared outside the project, the old
  name stands in a file no platform builds, a package;
  2 bad arguments or config; 3 written, but a write, fixer or check failed: stderr ends with
  the files written
`

const statsHelp = `catnip stats serves a report on recorded catnip calls at a local address until interrupted,
prints the address and opens it in the browser. Every other call is recorded in
<user config dir>/catnip/stats.db.

usage: catnip stats

exit: 1 the report failed to start or stopped, 2 bad arguments
`

// helps are the helps of the commands by name.
var helps = map[string]string{"show": showHelp, "edit": editHelp, "write": writeHelp, "def": defHelp, "ref": refHelp, "rename": renameHelp, "stats": statsHelp}

func main() {
	call := stats.Call{Time: time.Now(), Argv: os.Args[1:]}
	var errs strings.Builder
	code := run(call.Argv, os.Stdin, os.Stdout, io.MultiWriter(os.Stderr, &errs), &call)
	if len(call.Argv) == 0 || call.Argv[0] != "stats" {
		call.Error = strings.Replace(errs.String(), usage, "", 1)
		for _, h := range helps {
			call.Error = strings.Replace(call.Error, h, "", 1)
		}
		if p, err := stats.Path(); err == nil {
			_ = stats.Record(p, call, code, stats.Limit) // the call's outcome does not depend on its statistics
		}
	}
	os.Exit(code)
}

// env is what a command runs with.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	call           *stats.Call
}

// command is a command of catnip: its fields are its flags and arguments, run gives its exit code.
type command interface {
	run(e env) int
}

// cli is the grammar of catnip.
type cli struct {
	Help    helpFlag  `short:"h"`
	Show    showCmd   `cmd:""`
	Edit    editCmd   `cmd:""`
	Write   writeCmd  `cmd:""`
	Def     defCmd    `cmd:""`
	Ref     refCmd    `cmd:""`
	Rename  renameCmd `cmd:""`
	Stats   statsCmd  `cmd:""`
	HelpCmd helpCmd   `cmd:"" name:"help"`
}

// run runs the command in args and fills in the file and flags of call.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, call *stats.Call) int {
	var c cli
	parser, err := kong.New(&c, kong.Name("catnip"), kong.NoDefaultHelp(), kong.Writers(stdout, stderr),
		kong.Vars{"gutters": strings.Join(view.Gutters, ",")},
		kong.TypeMapper(reflect.TypeFor[editFlag](), editFlags{&c.Edit.ops}))
	if err != nil {
		panic(err) // the grammar is fixed in the code
	}
	ctx, err := parser.Parse(args)
	if errors.Is(err, errHelp) {
		return ok
	}
	if err != nil {
		help := usage
		if perr := (*kong.ParseError)(nil); errors.As(err, &perr) {
			help = helpOf(perr.Context)
		}
		fmt.Fprintf(stderr, "%v\n%s", err, help)
		return badUsage
	}
	for _, p := range ctx.Path {
		if p.Flag != nil {
			call.Flags = append(call.Flags, dashed(p.Flag.Name))
		}
	}
	return ctx.Selected().Target.Addr().Interface().(command).run(env{stdin, stdout, stderr, call})
}

// errHelp is the outcome of a call that asked for help.
var errHelp = errors.New("help requested")

// helpFlag prints the help of the command it is given with, or of catnip, instead of running it.
type helpFlag bool

func (helpFlag) BeforeReset(ctx *kong.Context) error {
	fmt.Fprint(ctx.Stdout, helpOf(ctx))
	return errHelp
}

// helpOf is the help of the command in ctx, or of catnip without one.
func helpOf(ctx *kong.Context) string {
	if ctx != nil && ctx.Selected() != nil {
		if h, found := helps[ctx.Selected().Name]; found {
			return h
		}
	}
	return usage
}

// helpCmd prints the help of catnip or of a command.
type helpCmd struct {
	Command string `arg:"" optional:""`
}

func (c *helpCmd) run(e env) int {
	help, found := helps[c.Command]
	switch {
	case c.Command == "":
		fmt.Fprint(e.stdout, usage)
	case found:
		fmt.Fprint(e.stdout, help)
	default:
		fmt.Fprintf(e.stderr, "unknown command %q\n%s", c.Command, usage)
		return badUsage
	}
	return ok
}

// usageError prints err and the help of a command to stderr.
func usageError(err error, help string, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%v\n%s", err, help)
	return badUsage
}

type showCmd struct {
	Gutter  string `default:"hash" enum:"${gutters}"`
	Blame   bool
	C       int `name:"C" short:"C" default:"5"`
	Outline bool
	Rev     *string
	Targets []string `arg:"" name:"path"`
}

func (s *showCmd) run(e env) int {
	if s.C < 0 {
		return usageError(fmt.Errorf("-C %d: want 0 or more", s.C), showHelp, e.stderr)
	}
	rev := ""
	if s.Rev != nil {
		if rev = *s.Rev; rev == "" || strings.HasPrefix(rev, "-") {
			return usageError(fmt.Errorf("--rev %q: want a git revision", rev), showHelp, e.stderr)
		}
	}
	ts := make([]view.Target, len(s.Targets))
	for i, op := range s.Targets {
		var err error
		ts[i], err = view.ParseTarget(op, s.C)
		switch {
		case err != nil:
		case s.Outline && (ts[i].From != 0 || ts[i].Symbol != ""):
			err = fmt.Errorf("--outline %s: want a path without lines or a symbol", op)
		case rev != "" && (s.Outline || ts[i].Symbol != ""):
			err = fmt.Errorf("--rev %s %s: ::<symbol> and --outline read the working tree", rev, op)
		}
		if err != nil {
			return usageError(err, showHelp, e.stderr)
		}
	}
	e.call.File = ts[0].Path
	code := ok
	prefix := func(i int) string { // with several targets, what a target says on stderr starts with the target
		if len(ts) > 1 {
			return s.Targets[i] + ": "
		}
		return ""
	}
	for i, t := range ts {
		if len(ts) > 1 {
			if i > 0 {
				fmt.Fprintln(e.stdout)
			}
			fmt.Fprintln(e.stdout, s.Targets[i])
		}
		out, warning, err := showTarget(t, rev, s.Outline, s.Gutter, s.Blame)
		if err != nil {
			fmt.Fprintln(e.stderr, prefix(i)+err.Error())
			code = notApplied
			continue
		}
		if warning != "" {
			fmt.Fprintln(e.stderr, prefix(i)+warning)
		}
		fmt.Fprint(e.stdout, out)
	}
	return code
}

// showTarget is what show prints for one target, the file as it was in the git revision rev when rev is not "", and
// the warning that its range is cut at the end of the file.
func showTarget(t view.Target, rev string, outline bool, gutter string, blame bool) (string, string, error) {
	ts := []view.Target{t}
	if outline || t.Symbol != "" {
		var err error
		if ts, err = declarations(t, outline); err != nil {
			return "", "", err
		}
	}
	raw, err := view.Read(t.Path, rev)
	if err != nil {
		return "", "", err
	}
	text, _ := edit.Decode(raw)
	n := len(edit.Lines(text))
	var b strings.Builder
	for i, t := range ts {
		if i > 0 && !outline { // declarations of one name print as blocks
			b.WriteString("\n")
		}
		var notes []string
		if blame {
			from, to, err := t.Lines(n)
			if err == nil {
				notes, err = view.Blame(t.Path, rev, from, to)
			}
			if err != nil {
				return "", "", err
			}
		}
		out, err := view.Render(text, t, gutter, notes)
		if err != nil {
			return "", "", err
		}
		b.WriteString(out)
	}
	return b.String(), t.Cut(n), nil
}

// declarations is what show prints for a target with a symbol, or with outline.
func declarations(t view.Target, outline bool) ([]view.Target, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	n := nav.New(cwd)
	ts, err := n.Show(t, outline)
	if cerr := n.Close(); err == nil {
		err = cerr
	}
	return ts, err
}

// editFlag is an edit of catnip edit, read by editFlags.
type editFlag struct{}

// editFlags reads the edits of catnip edit into ops in the order given: two values each, one for --count.
type editFlags struct{ ops *[]edit.Op }

func (f editFlags) Decode(ctx *kong.DecodeContext, _ reflect.Value) error {
	n := 2
	if ctx.Value.Name == "count" {
		n = 1
	}
	var values []string
	for t := ctx.Scan.Peek(); len(values) < n && !t.IsEOL(); t = ctx.Scan.Peek() {
		values = append(values, ctx.Scan.Pop().String())
	}
	ops, err := edit.Add(*f.ops, ctx.Value.Name, values)
	if err != nil {
		return err
	}
	*f.ops = ops
	return nil
}

type editCmd struct {
	Path        string `arg:""`
	Replace     editFlag
	Count       editFlag
	Lines       editFlag
	After       editFlag
	Before      editFlag
	ShowFixDiff bool
	SkipChecks  bool
	ops         []edit.Op
}

func (c *editCmd) run(e env) int {
	if len(c.ops) == 0 {
		return usageError(errors.New("no edits: want --replace OLD NEW, --lines FROM#HASH[-TO#HASH] NEW, --after LINE#HASH NEW or --before LINE#HASH NEW"), editHelp, e.stderr)
	}
	path := c.Path
	e.call.File = path
	cfg, err := config.Load(filepath.Dir(path))
	if err != nil {
		fmt.Fprintln(e.stderr, err)
		return badUsage
	}
	before, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(e.stderr, err)
		return notApplied
	}
	after, fails := edit.Apply(before, c.ops)
	if fails != nil {
		for _, f := range fails {
			fmt.Fprintf(e.stderr, "%s: %s\n", path, f)
		}
		fmt.Fprintln(e.stderr, "nothing written")
		return notApplied
	}
	if string(after) == string(before) {
		fmt.Fprintln(e.stdout, "no changes")
		return ok
	}
	return commit(cfg, path, before, after, c.ShowFixDiff, c.SkipChecks, e.stdout, e.stderr)
}

// dashed is a flag name as usage spells it.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

type writeCmd struct {
	Path        string `arg:""`
	ShowFixDiff bool
	SkipChecks  bool
}

func (c *writeCmd) run(e env) int {
	path := c.Path
	e.call.File = path
	cfg, err := config.Load(filepath.Dir(path))
	if err != nil {
		fmt.Fprintln(e.stderr, err)
		return badUsage
	}
	data, err := io.ReadAll(e.stdin)
	if err != nil {
		fmt.Fprintln(e.stderr, err)
		return notApplied
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(e.stderr, err)
		return notApplied
	}
	if old != nil {
		data = edit.Keep(old, data)
	}
	return commit(cfg, path, old, data, c.ShowFixDiff, c.SkipChecks, e.stdout, e.stderr)
}

// commit writes the file, prints its diff and, unless skipChecks, runs its fixers and checks; a fixer's own diff only
// with showFixDiff. before is nil for a new file.
func commit(cfg *config.Config, path string, before, after []byte, showFixDiff, skipChecks bool, stdout, stderr io.Writer) int {
	if err := store.Write(path, after); err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, store.ErrPartial) {
			return checkFailed
		}
		fmt.Fprintln(stderr, "nothing written")
		return notApplied
	}
	if before == nil {
		fmt.Fprintf(stdout, "%s: created, %d lines\n", path, len(edit.Lines(string(after))))
	} else {
		fmt.Fprint(stdout, edit.Diff(path, before, after))
	}
	if !skipChecks && !check.Report(cfg, path, showFixDiff, stdout, stderr) {
		return checkFailed
	}
	return ok
}

type defCmd struct {
	Body      bool
	Questions []string `arg:"" name:"question"`
}

func (c *defCmd) run(e env) int {
	qs, err := questions("def", c.Questions, e.call)
	if err != nil {
		return usageError(err, defHelp, e.stderr)
	}
	return navigate(e.stdout, e.stderr, func(n *nav.Navigator) int { return answered(n.Def(qs, c.Body, e.stdout, e.stderr)) })
}

type refCmd struct {
	Limit     int      `default:"60"`
	Questions []string `arg:"" name:"question"`
}

func (c *refCmd) run(e env) int {
	qs, err := questions("ref", c.Questions, e.call)
	if err == nil && c.Limit < 1 {
		err = fmt.Errorf("--limit %d: want 1 or more", c.Limit)
	}
	if err != nil {
		return usageError(err, refHelp, e.stderr)
	}
	return navigate(e.stdout, e.stderr, func(n *nav.Navigator) int { return answered(n.Ref(qs, c.Limit, e.stdout, e.stderr)) })
}

type renameCmd struct {
	ShowFixDiff bool
	Position    string `arg:""`
	Expression  string `arg:""`
	NewName     string `arg:""`
}

func (c *renameCmd) run(e env) int {
	qs, err := nav.Parse("rename", []string{c.Position, c.Expression})
	if err == nil && qs[0].Path == "" {
		err = fmt.Errorf("rename %s: want a position <path>:<line>", c.Position)
	}
	if err != nil {
		return usageError(err, renameHelp, e.stderr)
	}
	e.call.File = qs[0].Path
	return navigate(e.stdout, e.stderr, func(n *nav.Navigator) int { return n.Rename(qs[0], c.NewName, c.ShowFixDiff, e.stdout, e.stderr) })
}

// questions parses the questions of a navigation command and adds the first path to call.
func questions(name string, args []string, call *stats.Call) ([]nav.Question, error) {
	qs, err := nav.Parse(name, args)
	if err != nil {
		return nil, err
	}
	for _, q := range qs {
		if q.Path != "" {
			call.File = q.Path
			break
		}
	}
	return qs, nil
}

// navigate answers with a navigator in the current directory; answer gives the exit code.
func navigate(stdout, stderr io.Writer, answer func(*nav.Navigator) int) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	n := nav.New(cwd)
	code := answer(n)
	if err := n.Close(); err != nil {
		fmt.Fprintln(stderr, err)
		return max(code, notApplied)
	}
	return code
}

// answered is the exit code of def and ref: whether every question was answered.
func answered(all bool) int {
	if all {
		return ok
	}
	return notApplied
}

type statsCmd struct{}

func (statsCmd) run(e env) int {
	db, err := stats.Path()
	if err == nil {
		_, err = os.Stat(db)
	}
	if err != nil {
		fmt.Fprintln(e.stderr, err)
		return notApplied
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(e.stderr, err)
		return notApplied
	}
	url := "http://" + ln.Addr().String() + "/"
	fmt.Fprintln(e.stdout, url)
	browse(url)
	fmt.Fprintln(e.stderr, http.Serve(ln, stats.Handler(db)))
	return notApplied
}

// browse opens url in the browser when the system has a way to; the url is printed either way.
func browse(url string) {
	var cmd *exec.Cmd
	_, wslview := exec.LookPath("wslview")
	_, wslpath := exec.LookPath("wslpath")
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", url)
	case runtime.GOOS == "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case wslview == nil:
		cmd = exec.Command("wslview", url)
	case wslpath == nil:
		explorer := "explorer.exe"
		if _, err := exec.LookPath(explorer); err != nil {
			// WSL with [interop] appendWindowsPath = false has no Windows directories in PATH
			if p, err := exec.Command("wslpath", "-u", `C:\Windows\explorer.exe`).Output(); err == nil {
				explorer = strings.TrimSpace(string(p))
			}
		}
		cmd = exec.Command(explorer, url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}
