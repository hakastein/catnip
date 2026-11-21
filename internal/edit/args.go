package edit

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var lineRange = regexp.MustCompile(`^(\d+)#([0-9a-f]{4})(?:-(\d+)#([0-9a-f]{4}))?$`)

// Add reads one edit of catnip edit, its name without dashes and its values as given, into ops:
//
//	replace OLD NEW               replace exact text found once
//	count N                       find the OLD of the last edit, a replace, N times
//	lines FROM#HASH[-TO#HASH] NEW replace whole lines whose ends still have these hashes
//	after LINE#HASH NEW           insert whole lines after the line that still has this hash
//	before LINE#HASH NEW          insert whole lines before it
//
// Values are taken as they are. An error does not repeat the name: the caller prints it before the error.
func Add(ops []Op, name string, values []string) ([]Op, error) {
	switch name {
	case "replace":
		if len(values) != 2 {
			return nil, fmt.Errorf("want OLD and NEW, got %s", quoted(values))
		}
		if values[0] == "" {
			return nil, fmt.Errorf("%s: OLD, the text to find, is empty; OLD goes first, NEW second", quoted(values))
		}
		return append(ops, Op{Old: values[0], New: values[1], Count: 1}), nil
	case "count":
		if len(values) != 1 {
			return nil, errors.New("want N")
		}
		if len(ops) == 0 || ops[len(ops)-1].Old == "" {
			return nil, errors.New("goes after a --replace")
		}
		n, err := strconv.Atoi(values[0])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("bad number %q", values[0])
		}
		ops[len(ops)-1].Count = n
		return ops, nil
	case "lines":
		if len(values) != 2 {
			return nil, fmt.Errorf("want FROM#HASH[-TO#HASH] and NEW, got %s", quoted(values))
		}
		r, err := ParseRange("FROM#HASH[-TO#HASH]", values[0])
		if err != nil {
			return nil, err
		}
		return append(ops, Op{Lines: &r, New: values[1]}), nil
	case "after", "before":
		if len(values) != 2 {
			return nil, fmt.Errorf("want LINE#HASH and NEW, got %s", quoted(values))
		}
		if strings.Contains(values[0], "-") && lineRange.MatchString(values[0]) {
			return nil, fmt.Errorf("%q is a range, want one LINE#HASH", values[0])
		}
		r, err := ParseRange("LINE#HASH", values[0])
		if err != nil {
			return nil, err
		}
		if values[1] == "" {
			return nil, fmt.Errorf("%s: empty NEW inserts nothing", quoted(values))
		}
		return append(ops, Op{Insert: &Anchor{Line: r.From, Hash: r.FromHash, After: name == "after"}, New: values[1]}), nil
	}
	return nil, fmt.Errorf("unknown edit %q", name)
}

// quoted is the values of an edit as given, for an error to point at: each quoted and cut to 40 characters.
func quoted(values []string) string {
	if len(values) == 0 {
		return "nothing"
	}
	s := make([]string, len(values))
	for i, v := range values {
		if r := []rune(v); len(r) > 40 {
			s[i] = strconv.Quote(string(r[:40])) + "…"
		} else {
			s[i] = strconv.Quote(v)
		}
	}
	return strings.Join(s, " ")
}

// ParseRange reads FROM#HASH[-TO#HASH], the line addresses catnip show prints, where the form want is expected.
func ParseRange(want, s string) (LineRange, error) {
	m := lineRange.FindStringSubmatch(s)
	if m == nil {
		return LineRange{}, fmt.Errorf("%q is not %s as catnip show prints it", s, want)
	}
	r := LineRange{FromHash: m[2], ToHash: m[2]}
	var err error
	if r.From, err = strconv.Atoi(m[1]); err != nil {
		return LineRange{}, err
	}
	r.To = r.From
	if m[3] != "" {
		if r.To, err = strconv.Atoi(m[3]); err != nil {
			return LineRange{}, err
		}
		r.ToHash = m[4]
	}
	if r.From < 1 {
		return LineRange{}, fmt.Errorf("%q: lines start at 1", s)
	}
	if r.To < r.From {
		return LineRange{}, fmt.Errorf("%q: the range ends before it starts", s)
	}
	return r, nil
}
