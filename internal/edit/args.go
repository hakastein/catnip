package edit

import (
	"flag"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var lineRange = regexp.MustCompile(`^(\d+)#([0-9a-f]{4})(?:-(\d+)#([0-9a-f]{4}))?$`)

// ParseArgs reads edits from command-line arguments:
//
//	--replace OLD NEW [--count N]   replace exact text found N times (default 1)
//	--lines FROM#HASH[-TO#HASH] NEW replace whole lines whose ends still have these hashes
//	--after LINE#HASH NEW           insert whole lines after the line that still has this hash
//	--before LINE#HASH NEW          insert whole lines before it
//
// Values are taken as they are, also when they start with "--". -h or --help in place of an edit is flag.ErrHelp.
func ParseArgs(args []string) ([]Op, error) {
	var ops []Op
	for i := 0; i < len(args); {
		switch name := args[i]; name {
		case "--replace":
			if i+2 >= len(args) {
				return nil, fmt.Errorf("--replace needs OLD and NEW")
			}
			op := Op{Old: args[i+1], New: args[i+2], Count: 1}
			if op.Old == "" {
				return nil, fmt.Errorf("--replace: OLD is empty; the text to find goes first: --replace OLD NEW")
			}
			i += 3
			if i < len(args) && args[i] == "--count" {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("--count needs N")
				}
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 1 {
					return nil, fmt.Errorf("--count: bad number %q", args[i+1])
				}
				op.Count = n
				i += 2
			}
			ops = append(ops, op)
		case "--lines":
			if i+2 >= len(args) {
				return nil, fmt.Errorf("--lines needs FROM#HASH[-TO#HASH] and NEW")
			}
			r, err := parseRange(name, "FROM#HASH[-TO#HASH]", args[i+1])
			if err != nil {
				return nil, err
			}
			ops = append(ops, Op{Lines: &r, New: args[i+2]})
			i += 3
		case "--after", "--before":
			if i+2 >= len(args) {
				return nil, fmt.Errorf("%s needs LINE#HASH and NEW", name)
			}
			if strings.Contains(args[i+1], "-") && lineRange.MatchString(args[i+1]) {
				return nil, fmt.Errorf("%s: %q is a range, want one LINE#HASH", name, args[i+1])
			}
			r, err := parseRange(name, "LINE#HASH", args[i+1])
			if err != nil {
				return nil, err
			}
			if args[i+2] == "" {
				return nil, fmt.Errorf("%s: empty NEW inserts nothing", name)
			}
			ops = append(ops, Op{Insert: &Anchor{Line: r.From, Hash: r.FromHash, After: name == "--after"}, New: args[i+2]})
			i += 3
		case "--count":
			return nil, fmt.Errorf("--count goes right after a --replace")
		case "-h", "--help":
			return nil, flag.ErrHelp
		default:
			return nil, fmt.Errorf("unexpected %q: want --replace, --lines, --after or --before", args[i])
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no edits: want --replace OLD NEW, --lines FROM#HASH[-TO#HASH] NEW, --after LINE#HASH NEW or --before LINE#HASH NEW")
	}
	return ops, nil
}

// parseRange reads FROM#HASH[-TO#HASH], the line addresses catnip show prints, for the edit named name that
// wants the form want.
func parseRange(name, want, s string) (LineRange, error) {
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
