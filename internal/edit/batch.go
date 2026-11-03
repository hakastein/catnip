package edit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Marker starts every control line of a batch.
const Marker = "@@@ "

// Sections a verb takes after its target, in order; a batch op closes with "end".
var verbs = map[string][]string{
	"replace": {"with"},
	"block":   {"through", "with"},
	"before":  {"insert"},
	"after":   {"insert"},
}

var kinds = map[string]Kind{"replace": Replace, "block": Replace, "before": Before, "after": After}

var lineTarget = regexp.MustCompile(`^(.+):(\d+)#([0-9a-f]{4})(?:-(\d+)#([0-9a-f]{4}))?$`)

// ParseArgs builds one op from command-line arguments: verb[*N] target text...
// A text target takes the text to find first; a line target does not.
func ParseArgs(args []string) (Op, error) {
	if len(args) < 2 {
		return Op{}, fmt.Errorf("usage: edit <verb>[*N] <target> <text>...")
	}
	return build(args[0], args[1], args[2:], false)
}

// ParseBatch reads ops from a batch:
//
//	@@@ <verb>[*N] <target>
//	<text to find, omitted for a line target>
//	@@@ <section>
//	<text>
//	@@@ end
//
// Section text is taken verbatim, line terminators included, so it is whole lines: the text to find and the end
// anchor match only at the start of a line. Lines between ops must be blank.
func ParseBatch(in string) ([]Op, error) {
	var ops []Op
	var head string
	var texts []string
	for n, line := range strings.SplitAfter(in, "\n") {
		if !strings.HasPrefix(line, Marker) {
			switch {
			case head != "":
				texts[len(texts)-1] += line
			case strings.TrimSpace(line) != "":
				return nil, fmt.Errorf("line %d: text outside an op: %q", n+1, strings.TrimSuffix(line, "\n"))
			}
			continue
		}
		word := strings.TrimSpace(strings.TrimPrefix(line, Marker))
		switch {
		case head == "":
			verb, _, _ := strings.Cut(word, " ")
			verb, _, _ = strings.Cut(verb, "*")
			if _, ok := verbs[verb]; !ok {
				return nil, fmt.Errorf("line %d: %q is not an op header: want %s<verb>[*N] <target>", n+1, strings.TrimSpace(line), Marker)
			}
			head, texts = word, []string{""}
		case word == "end":
			verb, target, _ := strings.Cut(head, " ")
			if lineTarget.MatchString(target) {
				if texts[0] != "" {
					return nil, fmt.Errorf("line %d: %s: a line target takes no text to find", n+1, head)
				}
				texts = texts[1:]
			}
			op, err := build(verb, target, texts, true)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", n+1, head, err)
			}
			ops = append(ops, op)
			head = ""
		default:
			verb, _, _ := strings.Cut(head, " ")
			verb, _, _ = strings.Cut(verb, "*")
			want := verbs[verb]
			if len(texts) > len(want) || want[len(texts)-1] != word {
				return nil, fmt.Errorf("line %d: %s: unexpected marker %q", n+1, head, strings.TrimSpace(line))
			}
			texts = append(texts, "")
		}
	}
	if head != "" {
		return nil, fmt.Errorf("%s: missing %send", head, Marker)
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("empty batch")
	}
	return ops, nil
}

func build(verbCount, target string, texts []string, lineStart bool) (Op, error) {
	verb, count, counted := strings.Cut(verbCount, "*")
	want, ok := verbs[verb]
	if !ok {
		return Op{}, fmt.Errorf("unknown verb %q: want replace, block, before or after", verb)
	}
	op := Op{File: target, Kind: kinds[verb], Count: 1, LineStart: lineStart}
	if target == "" {
		return Op{}, fmt.Errorf("missing target")
	}
	if counted {
		n, err := strconv.Atoi(count)
		if err != nil || n < 1 {
			return Op{}, fmt.Errorf("bad count %q", count)
		}
		op.Count = n
	}
	if m := lineTarget.FindStringSubmatch(target); m != nil {
		if verb == "block" || counted {
			return Op{}, fmt.Errorf("a line target takes no block or count")
		}
		from, _ := strconv.Atoi(m[2])
		r := LineRange{From: from, To: from, FromHash: m[3], ToHash: m[3]}
		if m[4] != "" {
			r.To, _ = strconv.Atoi(m[4])
			r.ToHash = m[5]
		}
		op.File, op.Lines = m[1], &r
		if len(texts) != len(want) {
			return Op{}, fmt.Errorf("want %d texts, got %d", len(want), len(texts))
		}
		op.New = texts[0]
		return op, nil
	}
	if len(texts) != len(want)+1 {
		return Op{}, fmt.Errorf("want %d texts, got %d", len(want)+1, len(texts))
	}
	if texts[0] == "" {
		return Op{}, fmt.Errorf("empty text to find")
	}
	op.Old, op.New = texts[0], texts[len(texts)-1]
	if verb == "block" {
		if texts[1] == "" {
			return Op{}, fmt.Errorf("empty end anchor")
		}
		op.Until = texts[1]
	}
	return op, nil
}
