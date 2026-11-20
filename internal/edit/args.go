package edit

import (
	"errors"
	"flag"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var lineRange = regexp.MustCompile(`^(\d+)#([0-9a-f]{4})(?:-(\d+)#([0-9a-f]{4}))?$`)

// Args are the arguments of catnip edit: [flags] <path> <edit>..., where flags may also stand in place of an edit.
type Args struct {
	Path  string
	Ops   []Op
	Flags []string // the arguments that start with "-" and are not edits, as given, for the command's flag set
}

// ParseArgs reads the path, the edits and the flags from command-line arguments. Edits are:
//
//	--replace OLD NEW [--count N]   replace exact text found N times (default 1)
//	--lines FROM#HASH[-TO#HASH] NEW replace whole lines whose ends still have these hashes
//	--after LINE#HASH NEW           insert whole lines after the line that still has this hash
//	--before LINE#HASH NEW          insert whole lines before it
//
// Values are taken as they are, also when they start with "--". -h or --help outside a value is flag.ErrHelp.
func ParseArgs(args []string) (Args, error) {
	var a Args
	for i := 0; i < len(args); {
		name := args[i]
		if a.Path == "" && (name == "--replace" || name == "--lines" || name == "--after" || name == "--before") {
			return Args{}, fmt.Errorf("%s before the path: edit needs the file path first", name)
		}
		switch name {
		case "--replace":
			if i+2 >= len(args) {
				return Args{}, fmt.Errorf("%s: want OLD and NEW", typed(args[i:]))
			}
			op := Op{Old: args[i+1], New: args[i+2], Count: 1}
			if op.Old == "" {
				return Args{}, fmt.Errorf("%s: OLD, the text to find, is empty; OLD goes first, NEW second", typed(args[i:i+3]))
			}
			i += 3
			if i < len(args) && args[i] == "--count" {
				if i+1 >= len(args) {
					return Args{}, fmt.Errorf("--count needs N")
				}
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 1 {
					return Args{}, fmt.Errorf("--count: bad number %q", args[i+1])
				}
				op.Count = n
				i += 2
			}
			a.Ops = append(a.Ops, op)
		case "--lines":
			if i+2 >= len(args) {
				return Args{}, fmt.Errorf("%s: want FROM#HASH[-TO#HASH] and NEW", typed(args[i:]))
			}
			r, err := ParseRange(name, "FROM#HASH[-TO#HASH]", args[i+1])
			if err != nil {
				return Args{}, err
			}
			a.Ops = append(a.Ops, Op{Lines: &r, New: args[i+2]})
			i += 3
		case "--after", "--before":
			if i+2 >= len(args) {
				return Args{}, fmt.Errorf("%s: want LINE#HASH and NEW", typed(args[i:]))
			}
			if strings.Contains(args[i+1], "-") && lineRange.MatchString(args[i+1]) {
				return Args{}, fmt.Errorf("%s: %q is a range, want one LINE#HASH", name, args[i+1])
			}
			r, err := ParseRange(name, "LINE#HASH", args[i+1])
			if err != nil {
				return Args{}, err
			}
			if args[i+2] == "" {
				return Args{}, fmt.Errorf("%s: empty NEW inserts nothing", typed(args[i:i+3]))
			}
			a.Ops = append(a.Ops, Op{Insert: &Anchor{Line: r.From, Hash: r.FromHash, After: name == "--after"}, New: args[i+2]})
			i += 3
		case "--count":
			return Args{}, fmt.Errorf("--count goes right after a --replace")
		case "--": // the path follows, also when it starts with "-"
			if a.Path != "" || i+1 == len(args) {
				return Args{}, fmt.Errorf("unexpected %q: want --replace, --lines, --after or --before", name)
			}
			a.Path = args[i+1]
			i += 2
		case "-h", "--help":
			return Args{}, flag.ErrHelp
		default:
			switch {
			case len(name) > 1 && strings.HasPrefix(name, "-"):
				a.Flags = append(a.Flags, name)
			case a.Path == "":
				a.Path = name
			default:
				return Args{}, fmt.Errorf("unexpected %q: want --replace, --lines, --after or --before", name)
			}
			i++
		}
	}
	if a.Path == "" {
		return Args{}, errors.New("edit needs the file path first")
	}
	if len(a.Ops) == 0 {
		return Args{}, fmt.Errorf("no edits: want --replace OLD NEW, --lines FROM#HASH[-TO#HASH] NEW, --after LINE#HASH NEW or --before LINE#HASH NEW")
	}
	return a, nil
}

// typed is an edit as it was given, for an error to point at: the name, then each value quoted and cut to 40
// characters.
func typed(edit []string) string {
	s := edit[0]
	for _, v := range edit[1:] {
		if r := []rune(v); len(r) > 40 {
			s += " " + strconv.Quote(string(r[:40])) + "…"
		} else {
			s += " " + strconv.Quote(v)
		}
	}
	return s
}

// ParseRange reads FROM#HASH[-TO#HASH], the line addresses catnip show prints, for the argument named name that
// wants the form want.
func ParseRange(name, want, s string) (LineRange, error) {
	m := lineRange.FindStringSubmatch(s)
	if m == nil {
		return LineRange{}, fmt.Errorf("%s: %q is not %s as catnip show prints it", name, s, want)
	}
	r := LineRange{FromHash: m[2], ToHash: m[2]}
	var err error
	if r.From, err = strconv.Atoi(m[1]); err != nil {
		return LineRange{}, fmt.Errorf("%s: %w", name, err)
	}
	r.To = r.From
	if m[3] != "" {
		if r.To, err = strconv.Atoi(m[3]); err != nil {
			return LineRange{}, fmt.Errorf("%s: %w", name, err)
		}
		r.ToHash = m[4]
	}
	if r.From < 1 {
		return LineRange{}, fmt.Errorf("%s: %q: lines start at 1", name, s)
	}
	if r.To < r.From {
		return LineRange{}, fmt.Errorf("%s: %q: the range ends before it starts", name, s)
	}
	return r, nil
}
