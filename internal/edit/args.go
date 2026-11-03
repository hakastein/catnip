package edit

import (
	"fmt"
	"regexp"
	"strconv"
)

var lineRange = regexp.MustCompile(`^(\d+)#([0-9a-f]{4})(?:-(\d+)#([0-9a-f]{4}))?$`)

// ParseArgs reads edits from command-line arguments:
//
//	--replace OLD NEW [--count N]   replace exact text found N times (default 1)
//	--lines FROM#HASH[-TO#HASH] NEW replace whole lines whose ends still have these hashes
//
// Values are taken as they are, also when they start with "--".
func ParseArgs(args []string) ([]Op, error) {
	var ops []Op
	for i := 0; i < len(args); {
		switch args[i] {
		case "--replace":
			if i+2 >= len(args) {
				return nil, fmt.Errorf("--replace needs OLD and NEW")
			}
			op := Op{Old: args[i+1], New: args[i+2], Count: 1}
			if op.Old == "" {
				return nil, fmt.Errorf("--replace: empty text to find")
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
			m := lineRange.FindStringSubmatch(args[i+1])
			if m == nil {
				return nil, fmt.Errorf("--lines: %q is not FROM#HASH[-TO#HASH] as catnip show --hash prints it", args[i+1])
			}
			r := LineRange{FromHash: m[2], ToHash: m[2]}
			var err error
			if r.From, err = strconv.Atoi(m[1]); err != nil {
				return nil, fmt.Errorf("--lines: %w", err)
			}
			r.To = r.From
			if m[3] != "" {
				if r.To, err = strconv.Atoi(m[3]); err != nil {
					return nil, fmt.Errorf("--lines: %w", err)
				}
				r.ToHash = m[4]
			}
			if r.From < 1 || r.To < r.From {
				return nil, fmt.Errorf("--lines: %q: lines start at 1 and the range ends after it starts", args[i+1])
			}
			ops = append(ops, Op{Lines: &r, New: args[i+2]})
			i += 3
		case "--count":
			return nil, fmt.Errorf("--count goes right after a --replace")
		default:
			return nil, fmt.Errorf("unexpected %q: want --replace or --lines", args[i])
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no edits: want --replace OLD NEW or --lines FROM#HASH[-TO#HASH] NEW")
	}
	return ops, nil
}
