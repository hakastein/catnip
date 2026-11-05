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
	"github.com/hakastein/catnip/internal/stats"
	"github.com/hakastein/catnip/internal/store"
	"github.com/hakastein/catnip/internal/view"
)

// Exit codes.
const (
	ok          = 0 // written, checks passed
	notApplied  = 1 // nothing written
	badUsage    = 2 // bad arguments or config
	checkFailed = 3 // written, but a fixer or check failed or the file was written in part
)

const usage = `usage:
  catnip show [--gutter hash|line|none] [--blame] [-C N] <path>[:<from>-<to> | :<line>[:<column>]]
  catnip edit [--show-fix-diff] <path> <edit>...
  catnip write [--show-fix-diff] <path> < content
  catnip stats

show prints each line after its address <line>#<hash>; --gutter line prints the line number alone,
  --gutter none the text alone
--show-fix-diff also prints what each fixer changed

edits, any number, all or none applied:
  --replace OLD NEW [--count N]    replace exact text that occurs N times (default 1)
  --lines FROM#HASH[-TO#HASH] NEW  replace whole lines; FROM#HASH as catnip show prints it
  --after LINE#HASH NEW            insert whole lines after the line
  --before LINE#HASH NEW           insert whole lines before the line
  an empty NEW deletes; --after and --before need a non-empty NEW

stats serves a report on the recorded calls at a local address until interrupted, prints the
  address and opens it; every other call is recorded in <user config dir>/catnip/stats.db

exit: 0 written and checks passed, 1 nothing written, 2 bad arguments or config,
  3 written but a fixer or check failed, or the file was written in part
`

func main() {
	call := stats.Call{Time: time.Now(), Argv: os.Args[1:]}
	var errs strings.Builder
	code := run(call.Argv, os.Stdin, os.Stdout, io.MultiWriter(os.Stderr, &errs), &call)
	if len(call.Argv) == 0 || call.Argv[0] != "stats" {
		call.Error = strings.Replace(errs.String(), usage, "", 1)
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
	case "stats":
		return statsCmd(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return ok
	}
	fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
	return badUsage
}

func show(args []string, stdout, stderr io.Writer, call *stats.Call) int {
	fl := flag.NewFlagSet("show", flag.ContinueOnError)
	fl.SetOutput(stderr)
	gutter := fl.String("gutter", "hash", "before each line: hash (<n>#<hash>), line (<n>) or none")
	blame := fl.Bool("blame", false, "add git blame of each line")
	context := fl.Int("C", 5, "lines of context around a position")
	var pos []string
	err := fl.Parse(args)
	for err == nil && fl.NArg() > 0 { // flags may follow the target
		pos = append(pos, fl.Arg(0))
		err = fl.Parse(fl.Args()[1:])
	}
	badGutter := !slices.Contains(view.Gutters, *gutter)
	if err == nil && badGutter {
		fmt.Fprintf(stderr, "--gutter %q: want one of %s\n", *gutter, strings.Join(view.Gutters, ", "))
	}
	if err != nil || len(pos) != 1 || *context < 0 || badGutter {
		fmt.Fprint(stderr, usage)
		return badUsage
	}
	fl.Visit(func(f *flag.Flag) { call.Flags = append(call.Flags, dashed(f.Name)) })
	t, err := view.ParseTarget(pos[0], *context)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return badUsage
	}
	call.File = t.Path
	raw, err := os.ReadFile(t.Path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	text, _ := edit.Decode(raw)
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
	return ok
}

func editCmd(args []string, stdout, stderr io.Writer, call *stats.Call) int {
	showFixDiff, args, err := fixFlags("edit", args, stderr, call)
	if err != nil || len(args) == 0 || strings.HasPrefix(args[0], "--") {
		fmt.Fprint(stderr, "edit needs the file path first\n"+usage)
		return badUsage
	}
	path := args[0]
	call.File = path
	ops, err := edit.ParseArgs(args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return badUsage
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
func fixFlags(name string, args []string, stderr io.Writer, call *stats.Call) (showFixDiff bool, rest []string, err error) {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(stderr)
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
	showFixDiff, args, err := fixFlags("write", args, stderr, call)
	if err != nil || len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return badUsage
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

func statsCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprint(stderr, usage)
		return badUsage
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
