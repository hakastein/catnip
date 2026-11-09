// catnip shows and edits files for agents from the shell; every command chains with && by its exit code.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

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
	ok          = 0 // done, checks passed
	notApplied  = 1 // nothing written
	badUsage    = 2 // bad arguments or config
	checkFailed = 3 // written, but a fixer or check failed or the file was written in part
)

const exitCodes = `
exit: 0 done, checks passed; 1 nothing written; 2 bad arguments or config;
  3 written, but a fixer or check failed or the file was written in part
`

// editOps are the edits of catnip edit, as both helps list them.
const editOps = `  --replace OLD NEW [--count N]    replace the text OLD found exactly once, or exactly N times
  --lines FROM#HASH[-TO#HASH] NEW  replace whole lines, addressed as catnip show prints them
  --after LINE#HASH NEW            insert lines after the line
  --before LINE#HASH NEW           insert lines before the line
  an empty NEW deletes OLD or the lines; --after and --before need a non-empty NEW
`

const usage = `catnip shows and edits files from the shell; commands chain with && by their exit codes.

catnip show <path>[:<from>-<to> | :<line>[:<column> | #<hash>] | ::<symbol>] [--outline]
  print the file, its lines <from> to <to>, line <line> with 5 lines on each side, or the
  Go declaration <symbol> (Func, Type, Type.Method) from its doc comment to its end;
  --outline prints the line of each declaration of a Go file; by default each line comes
  after its address <line>#<hash>, which catnip edit takes

catnip edit <path> <edit>...
  apply the edits to the file: all of them, or none if any does not match; every edit
  addresses the file as it was before the command; OLD and NEW may span several lines
` + editOps + `
catnip write <path> < content
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

catnip stats
  open a report on recorded catnip calls in the browser

After edit and write catnip prints what changed and runs the fixers and checks
configured for the file. catnip <command> --help prints all flags of the command.
` + exitCodes

const showHelp = `catnip show prints a file, a range of its lines, the lines around a position or the
declarations of a Go symbol. Without flags each line comes after its address <line>#<hash>, which
catnip edit takes.

usage: catnip show [flags] <path>[:<from>-<to> | :<line>[:<column> | #<hash>] | ::<symbol>]
  <path>                    the whole file
  <path>:<from>-<to>        lines <from> to <to>
  <path>:<line>[:<column>]  line <line> with 5 lines on each side
  <path>:<line>#<hash>      the same by the address of the line; it must not have changed
  <path>::<symbol>          each Go declaration named <symbol> in the file (Func, Type,
                            Type.Method, Type.Field) from its doc comment to its end;
                            declarations are separated by an empty line

flags:
  -C N           lines on each side of a position instead of 5
  --outline      the line of each declaration of a Go file: functions, methods, types,
                 fields, variables and constants
  --gutter line  only the line number, no hash
  --gutter none  no address
  --blame        the commit, author and date of each line

exit: 0 printed; 1 nothing printed: no file, the line changed, no declaration <symbol>,
  not a Go file; 2 bad arguments
`

const editHelp = `catnip edit applies edits to one file: all of them, or none if any does not match.
Every edit addresses the file as it was before the command. catnip prints the changed lines, then
runs the fixers and checks configured for the file.

usage: catnip edit [--show-fix-diff] <path> <edit>...

edits:
` + editOps + `
flags:
  --show-fix-diff  also print what each fixer changed
` + exitCodes

const writeHelp = `catnip write writes stdin to a file, creating it if missing. Overwriting keeps the BOM, line
endings and final newline of the file. Then catnip runs the fixers and checks configured for it.

usage: catnip write [--show-fix-diff] <path> < content

flags:
  --show-fix-diff  also print what each fixer changed
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
of each group by file, the files with most places first; places on one line are one line marked
<path>:<line>#<hash> (<n> places): <text>. Lines left out end the group as "<n> more lines in <m>
files". Blocks are separated by an empty line. Paths are relative to the current directory; a file
outside the project has an absolute path.

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

const statsHelp = `catnip stats serves a report on recorded catnip calls at a local address until interrupted,
prints the address and opens it in the browser. Every other call is recorded in
<user config dir>/catnip/stats.db.

usage: catnip stats

exit: 1 the report failed to start or stopped, 2 bad arguments
`

// helps are the helps of the commands by name.
var helps = map[string]string{"show": showHelp, "edit": editHelp, "write": writeHelp, "def": defHelp, "ref": refHelp, "stats": statsHelp}

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

// run runs the command in args and fills in the file and flags of call.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, call *stats.Call) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return badUsage
	}
	switch args[0] {
	case "show":
		return show(args[1:], stdout, stderr, call)
	case "edit":
		return editCmd(args[1:], stdout, stderr, call)
	case "write":
		return write(args[1:], stdin, stdout, stderr, call)
	case "def":
		return def(args[1:], stdout, stderr, call)
	case "ref":
		return ref(args[1:], stdout, stderr, call)
	case "stats":
		return statsCmd(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		if len(args) == 1 {
			fmt.Fprint(stdout, usage)
			return ok
		}
		if h, found := helps[args[1]]; found {
			fmt.Fprint(stdout, h)
			return ok
		}
		args = args[1:]
	}
	fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
	return badUsage
}

// usageError prints the help of a command: to stdout when err asks for it (ok), otherwise after err to stderr (badUsage).
func usageError(err error, help string, stdout, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, help)
		return ok
	}
	fmt.Fprintf(stderr, "%v\n%s", err, help)
	return badUsage
}

// newFlags is a flag set that leaves printing its errors and help to usageError.
func newFlags(name string) *flag.FlagSet {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	return fl
}

func show(args []string, stdout, stderr io.Writer, call *stats.Call) int {
	fl := newFlags("show")
	gutter := fl.String("gutter", "hash", "before each line: hash (<n>#<hash>), line (<n>) or none")
	blame := fl.Bool("blame", false, "add git blame of each line")
	context := fl.Int("C", 5, "lines of context around a position")
	outline := fl.Bool("outline", false, "the line of each declaration of a Go file")
	var pos []string
	args = slices.Clone(args) // args shares its array with the recorded argv
	for i, a := range args {
		if a == "--" {
			break
		}
		if len(a) > 2 && strings.HasPrefix(a, "-C") && a[2] != '=' {
			args[i] = "-C=" + a[2:] // -C0 as grep takes it; flag reads it as a flag named C0
		}
	}
	err := fl.Parse(args)
	for err == nil && fl.NArg() > 0 { // flags may follow the target
		pos = append(pos, fl.Arg(0))
		err = fl.Parse(fl.Args()[1:])
	}
	switch {
	case err != nil:
	case !slices.Contains(view.Gutters, *gutter):
		err = fmt.Errorf("--gutter %q: want one of %s", *gutter, strings.Join(view.Gutters, ", "))
	case *context < 0:
		err = fmt.Errorf("-C %d: want 0 or more", *context)
	case len(pos) != 1:
		err = fmt.Errorf("show takes one path, got %d", len(pos))
	}
	if err != nil {
		return usageError(err, showHelp, stdout, stderr)
	}
	fl.Visit(func(f *flag.Flag) { call.Flags = append(call.Flags, dashed(f.Name)) })
	t, err := view.ParseTarget(pos[0], *context)
	if err == nil && *outline && (t.From != 0 || t.Symbol != "") {
		err = fmt.Errorf("--outline %s: want a path without lines or a symbol", pos[0])
	}
	if err != nil {
		return usageError(err, showHelp, stdout, stderr)
	}
	call.File = t.Path
	ts := []view.Target{t}
	if *outline || t.Symbol != "" {
		if ts, err = declarations(t, *outline); err != nil {
			fmt.Fprintln(stderr, err)
			return notApplied
		}
	}
	raw, err := os.ReadFile(t.Path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	text, _ := edit.Decode(raw)
	for i, t := range ts {
		if i > 0 && !*outline { // declarations of one name print as blocks
			fmt.Fprintln(stdout)
		}
		var notes []string
		if *blame {
			from, to, err := t.Lines(len(edit.Lines(text)))
			if err == nil {
				notes, err = view.Blame(t.Path, from, to)
			}
			if err != nil {
				fmt.Fprintln(stderr, err)
				return notApplied
			}
		}
		out, err := view.Render(text, t, *gutter, notes)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return notApplied
		}
		fmt.Fprint(stdout, out)
	}
	return ok
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

func editCmd(args []string, stdout, stderr io.Writer, call *stats.Call) int {
	showFixDiff, args, err := fixFlags("edit", args, call)
	if err == nil && (len(args) == 0 || strings.HasPrefix(args[0], "--")) {
		err = errors.New("edit needs the file path first")
	}
	if err != nil {
		return usageError(err, editHelp, stdout, stderr)
	}
	path := args[0]
	call.File = path
	ops, err := edit.ParseArgs(args[1:])
	if err != nil {
		return usageError(err, editHelp, stdout, stderr)
	}
	for _, op := range ops {
		switch {
		case op.Lines != nil:
			call.Flags = append(call.Flags, "--lines")
		case op.Insert != nil && op.Insert.After:
			call.Flags = append(call.Flags, "--after")
		case op.Insert != nil:
			call.Flags = append(call.Flags, "--before")
		case op.Count != 1:
			call.Flags = append(call.Flags, "--replace", "--count")
		default:
			call.Flags = append(call.Flags, "--replace")
		}
	}
	cfg, err := config.Load(filepath.Dir(path))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return badUsage
	}
	before, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	after, fails := edit.Apply(before, ops)
	if fails != nil {
		for _, f := range fails {
			fmt.Fprintf(stderr, "%s: %s\n", path, f)
		}
		fmt.Fprintln(stderr, "nothing written")
		return notApplied
	}
	if string(after) == string(before) {
		fmt.Fprintln(stdout, "no changes")
		return ok
	}
	return commit(cfg, path, before, after, showFixDiff, stdout, stderr)
}

// fixFlags parses the flags of edit and write, which come before the path, and adds them to call.
func fixFlags(name string, args []string, call *stats.Call) (showFixDiff bool, rest []string, err error) {
	fl := newFlags(name)
	fl.BoolVar(&showFixDiff, "show-fix-diff", false, "also print what each fixer changed")
	err = fl.Parse(args)
	fl.Visit(func(f *flag.Flag) { call.Flags = append(call.Flags, dashed(f.Name)) })
	return showFixDiff, fl.Args(), err
}

// dashed is a flag name as usage spells it.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

func write(args []string, stdin io.Reader, stdout, stderr io.Writer, call *stats.Call) int {
	showFixDiff, args, err := fixFlags("write", args, call)
	if err == nil && len(args) != 1 {
		err = fmt.Errorf("write takes one path, got %d", len(args))
	}
	if err != nil {
		return usageError(err, writeHelp, stdout, stderr)
	}
	path := args[0]
	call.File = path
	cfg, err := config.Load(filepath.Dir(path))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return badUsage
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	if old != nil {
		data = edit.Keep(old, data)
	}
	return commit(cfg, path, old, data, showFixDiff, stdout, stderr)
}

// commit writes the file, prints its diff and runs its fixers and checks; a fixer's own diff only with showFixDiff.
// before is nil for a new file.
func commit(cfg *config.Config, path string, before, after []byte, showFixDiff bool, stdout, stderr io.Writer) int {
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
	fix, checks := cfg.For(path)
	outcomes, err := check.Run(cfg.Root, path, fix, checks)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return checkFailed
	}
	code := ok
	for _, o := range outcomes {
		switch {
		case o.Failed != "":
			fmt.Fprintf(stderr, "%s: failed\n%s\n", o.Name, o.Failed)
			code = checkFailed
		case o.Diff == "":
			fmt.Fprintf(stdout, "%s: passed\n", o.Name)
		}
		if o.Diff != "" {
			fmt.Fprintf(stdout, "%s: fixed\n", o.Name)
		}
		if o.Diff != "" && showFixDiff {
			fmt.Fprint(stdout, o.Diff)
		}
	}
	return code
}

func def(args []string, stdout, stderr io.Writer, call *stats.Call) int {
	fl := newFlags("def")
	body := fl.Bool("body", false, "print the whole declaration")
	qs, err := questions(fl, args, call)
	if err != nil {
		return usageError(err, defHelp, stdout, stderr)
	}
	return navigate(stdout, stderr, func(n *nav.Navigator) bool { return n.Def(qs, *body, stdout, stderr) })
}

func ref(args []string, stdout, stderr io.Writer, call *stats.Call) int {
	fl := newFlags("ref")
	limit := fl.Int("limit", 60, "lines of an answer")
	qs, err := questions(fl, args, call)
	if err == nil && *limit < 1 {
		err = fmt.Errorf("--limit %d: want 1 or more", *limit)
	}
	if err != nil {
		return usageError(err, refHelp, stdout, stderr)
	}
	return navigate(stdout, stderr, func(n *nav.Navigator) bool { return n.Ref(qs, *limit, stdout, stderr) })
}

// questions parses the flags and questions of a navigation command and adds the flags and the first path to call.
func questions(fl *flag.FlagSet, args []string, call *stats.Call) ([]nav.Question, error) {
	var questions []string
	err := fl.Parse(args)
	for err == nil && fl.NArg() > 0 { // flags may follow the questions
		questions = append(questions, fl.Arg(0))
		err = fl.Parse(fl.Args()[1:])
	}
	if err != nil {
		return nil, err
	}
	qs, err := nav.Parse(fl.Name(), questions)
	if err != nil {
		return nil, err
	}
	fl.Visit(func(f *flag.Flag) { call.Flags = append(call.Flags, dashed(f.Name)) })
	for _, q := range qs {
		if q.Path != "" {
			call.File = q.Path
			break
		}
	}
	return qs, nil
}

// navigate answers with a navigator in the current directory; answer reports whether every question was answered.
func navigate(stdout, stderr io.Writer, answer func(*nav.Navigator) bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	n := nav.New(cwd)
	answered := answer(n)
	if err := n.Close(); err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	if !answered {
		return notApplied
	}
	return ok
}

func statsCmd(args []string, stdout, stderr io.Writer) int {
	fl := newFlags("stats")
	err := fl.Parse(args)
	if err == nil && fl.NArg() > 0 {
		err = errors.New("stats takes no arguments")
	}
	if err != nil {
		return usageError(err, statsHelp, stdout, stderr)
	}
	db, err := stats.Path()
	if err == nil {
		_, err = os.Stat(db)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	url := "http://" + ln.Addr().String() + "/"
	fmt.Fprintln(stdout, url)
	browse(url)
	fmt.Fprintln(stderr, http.Serve(ln, stats.Handler(db)))
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
