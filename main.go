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
	ok          = 0 // applied, checks passed
	notApplied  = 1 // nothing written
	badUsage    = 2 // bad arguments or config
	checkFailed = 3 // written, but a check failed or not every file of the batch was written
)

const usage = `usage:
  catnip show [--hash] [--blame] [-C N] <path>[:<from>-<to> | :<line>[:<column>]]
  catnip edit <verb>[*N] <target> <text>...      one op from arguments
  catnip edit < batch                            ops from stdin
  catnip write <path> < content

verbs: replace <old> <new> | block <start> <end> <new> | before <anchor> <new> | after <anchor> <new>
target: <path> for text, <path>:<line>#<hash>[-<line>#<hash>] for lines (no text to find)
batch op:
  @@@ <verb>[*N] <target>
  <text to find; none for a line target>
  @@@ through          (block only)
  <end anchor>
  @@@ with             (replace, block) or @@@ insert (before, after)
  <new text>
  @@@ end
exit: 0 written and checks passed, 1 nothing written, 2 bad arguments or config,
  3 written but a fixer or check failed, or the batch was written only in part
batch text is whole lines: the text to find and the end anchor match at the start of a line;
edit part of a line with arguments
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
		return editCmd(args[1:], stdin, stdout, stderr)
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

func editCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var ops []edit.Op
	if len(args) == 0 {
		in, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return notApplied
		}
		if ops, err = edit.ParseBatch(string(in)); err != nil {
			fmt.Fprintln(stderr, err)
			return badUsage
		}
	} else {
		op, err := edit.ParseArgs(args)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return badUsage
		}
		ops = []edit.Op{op}
	}
	cfg, err := config.Load(".")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return badUsage
	}
	before := map[string][]byte{}
	var paths []string
	var infos []os.FileInfo // infos[i] is the file at paths[i]
ops:
	for i, op := range ops {
		info, err := os.Stat(op.File)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return notApplied
		}
		for j, seen := range infos { // other spellings of a read file: ./a, a symlink, another case
			if os.SameFile(seen, info) {
				ops[i].File = paths[j]
				continue ops
			}
		}
		raw, err := os.ReadFile(op.File)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return notApplied
		}
		before[op.File] = raw
		paths = append(paths, op.File)
		infos = append(infos, info)
	}
	after, fails := edit.Apply(before, ops)
	if fails != nil {
		for _, f := range fails {
			fmt.Fprintln(stderr, f)
		}
		fmt.Fprintln(stderr, "nothing written")
		return notApplied
	}
	var changed []string
	for _, p := range paths {
		if string(after[p]) != string(before[p]) {
			changed = append(changed, p)
		}
	}
	if len(changed) == 0 {
		fmt.Fprintln(stdout, "no changes")
		return ok
	}
	return commit(cfg, changed, before, after, stdout, stderr)
}

func write(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
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
	return commit(cfg, []string{path}, map[string][]byte{path: old}, map[string][]byte{path: data}, stdout, stderr)
}

// commit writes the changed files, prints their diffs and runs their fixers and checks.
func commit(cfg *config.Config, paths []string, before, after map[string][]byte, stdout, stderr io.Writer) int {
	for i, p := range paths {
		if err := store.Write(p, after[p]); err != nil {
			fmt.Fprintln(stderr, err)
			written := paths[:i]
			if errors.Is(err, store.ErrPartial) {
				written = paths[:i+1]
			}
			if len(written) == 0 {
				fmt.Fprintln(stderr, "nothing written")
				return notApplied
			}
			fmt.Fprintln(stderr, "written: "+strings.Join(written, ", "))
			if rest := paths[len(written):]; len(rest) > 0 {
				fmt.Fprintln(stderr, "not written: "+strings.Join(rest, ", "))
			}
			return checkFailed
		}
		if before[p] == nil {
			fmt.Fprintf(stdout, "%s: created, %d lines\n", p, len(edit.Lines(string(after[p]))))
		} else {
			fmt.Fprint(stdout, edit.Diff(p, before[p], after[p]))
		}
	}
	passed, failed := 0, 0
	for _, p := range paths {
		fix, checks := cfg.For(p)
		r, err := check.Run(p, fix, checks)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return checkFailed
		}
		if r.Diff != "" {
			fmt.Fprintf(stdout, "fixers changed %s:\n%s", p, r.Diff)
		}
		for _, f := range r.Failed {
			fmt.Fprintf(stderr, "FAIL %s\n%s\n", f.Cmd, f.Output)
		}
		passed += r.Passed
		failed += len(r.Failed)
	}
	if passed+failed > 0 {
		fmt.Fprintf(stdout, "checks: %d passed, %d failed\n", passed, failed)
	}
	if failed > 0 {
		return checkFailed
	}
	return ok
}
