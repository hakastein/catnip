// catnip shows and edits files for agents from the shell; every command chains with && by its exit code.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/hakastein/catnip/internal/check"
	"github.com/hakastein/catnip/internal/config"
	"github.com/hakastein/catnip/internal/edit"
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
  catnip show [--hash] [--blame] [-C N] <path>[:<from>-<to> | :<line>[:<column>]]
  catnip edit [--show-fix-diff] <path> <edit>...
  catnip write [--show-fix-diff] <path> < content

--show-fix-diff also prints what each fixer changed

edits, any number, all or none applied:
  --replace OLD NEW [--count N]    replace exact text that occurs N times (default 1)
  --lines FROM#HASH[-TO#HASH] NEW  replace whole lines; FROM#HASH as catnip show --hash prints it
  an empty NEW deletes

exit: 0 written and checks passed, 1 nothing written, 2 bad arguments or config,
  3 written but a fixer or check failed, or the file was written in part
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return badUsage
	}
	switch args[0] {
	case "show":
		return show(args[1:], stdout, stderr)
	case "edit":
		return editCmd(args[1:], stdout, stderr)
	case "write":
		return write(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return ok
	}
	fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
	return badUsage
}

func show(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("show", flag.ContinueOnError)
	fl.SetOutput(stderr)
	hash := fl.Bool("hash", false, "add the content hash of each line")
	blame := fl.Bool("blame", false, "add git blame of each line")
	context := fl.Int("C", 5, "lines of context around a position")
	var pos []string
	err := fl.Parse(args)
	for err == nil && fl.NArg() > 0 { // flags may follow the target
		pos = append(pos, fl.Arg(0))
		err = fl.Parse(fl.Args()[1:])
	}
	if err != nil || len(pos) != 1 || *context < 0 {
		fmt.Fprint(stderr, usage)
		return badUsage
	}
	t, err := view.ParseTarget(pos[0], *context)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return badUsage
	}
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
	out, err := view.Render(text, t, *hash, notes)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return notApplied
	}
	fmt.Fprint(stdout, out)
	return ok
}

func editCmd(args []string, stdout, stderr io.Writer) int {
	showFixDiff, args, err := fixFlags("edit", args, stderr)
	if err != nil || len(args) == 0 || strings.HasPrefix(args[0], "--") {
		fmt.Fprint(stderr, "edit needs the file path first\n"+usage)
		return badUsage
	}
	path := args[0]
	ops, err := edit.ParseArgs(args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return badUsage
	}
	cfg, err := config.Load(".")
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

// fixFlags parses the flags of edit and write, which come before the path.
func fixFlags(name string, args []string, stderr io.Writer) (showFixDiff bool, rest []string, err error) {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.BoolVar(&showFixDiff, "show-fix-diff", false, "also print what each fixer changed")
	err = fl.Parse(args)
	return showFixDiff, fl.Args(), err
}

func write(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	showFixDiff, args, err := fixFlags("write", args, stderr)
	if err != nil || len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return badUsage
	}
	path := args[0]
	cfg, err := config.Load(".")
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
	outcomes, err := check.Run(path, fix, checks)
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
