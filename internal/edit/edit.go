// Package edit applies batches of text edits to file contents in memory.
package edit

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Kind is what an operation does at its target.
type Kind int

const (
	// Replace swaps the target for New; an empty New deletes the target.
	Replace Kind = iota
	// Before inserts New right before the target.
	Before
	// After inserts New right after the target.
	After
)

// Op is one edit. The target is either text (Old, optionally through Until) or Lines.
type Op struct {
	File string
	Kind Kind
	// Old is the exact text to find; with Until it is the start anchor of a block.
	Old string
	// Until is the end anchor: the block runs from Old to the first Until after it, both included.
	Until string
	// Count is how many times Old must occur; every occurrence is edited.
	Count int
	// Lines addresses whole lines instead of text.
	Lines *LineRange
	New   string
}

// LineRange is lines From..To (1-based, inclusive), each end checked against its Hash.
type LineRange struct {
	From, To         int
	FromHash, ToHash string
}

// Failure is why the operation with index Op could not be applied.
type Failure struct {
	Op     int
	File   string
	Reason string
}

func (f Failure) Error() string { return fmt.Sprintf("op %d (%s): %s", f.Op+1, f.File, f.Reason) }

// Encoding is what Decode removed from a file and Encode puts back.
type Encoding struct{ BOM, CRLF bool }

const bom = "\ufeff"

// Decode strips a UTF-8 BOM and turns CRLF into LF when every line ends with CRLF.
func Decode(raw []byte) (string, Encoding) {
	s := string(raw)
	var e Encoding
	if strings.HasPrefix(s, bom) {
		e.BOM, s = true, s[len(bom):]
	}
	if n := strings.Count(s, "\n"); n > 0 && strings.Count(s, "\r\n") == n {
		e.CRLF, s = true, strings.ReplaceAll(s, "\r\n", "\n")
	}
	return s, e
}

// Encode is the inverse of Decode.
func (e Encoding) Encode(s string) []byte {
	if e.CRLF {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	if e.BOM {
		s = bom + s
	}
	return []byte(s)
}

// Lines splits decoded text into lines without terminators.
func Lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Hash is the 4-hex-digit content hash of one line without its terminator.
func Hash(line string) string {
	h := fnv.New32a()
	h.Write([]byte(line))
	s := h.Sum32()
	return fmt.Sprintf("%04x", (s>>16^s)&0xffff)
}

type change struct {
	start, end, op int
	text           string
}

// Apply applies ops to files (path → raw content). Every target is resolved against the original content, so
// line numbers and anchors are the ones the caller saw. Any failure means no file changes: the result is nil and
// every failing operation is reported.
func Apply(files map[string][]byte, ops []Op) (map[string][]byte, []Failure) {
	type doc struct {
		text    string
		enc     Encoding
		changes []change
	}
	docs := map[string]*doc{}
	var fails []Failure
	for i, op := range ops {
		raw, ok := files[op.File]
		if !ok {
			fails = append(fails, Failure{i, op.File, "file was not read"})
			continue
		}
		d := docs[op.File]
		if d == nil {
			d = &doc{}
			d.text, d.enc = Decode(raw)
			docs[op.File] = d
		}
		cs, reason := resolve(d.text, op)
		if reason != "" {
			fails = append(fails, Failure{i, op.File, reason})
			continue
		}
		for j := range cs {
			cs[j].op = i
		}
		d.changes = append(d.changes, cs...)
	}
	out := map[string][]byte{}
	for path, d := range docs {
		sort.SliceStable(d.changes, func(a, b int) bool {
			ca, cb := d.changes[a], d.changes[b]
			if ca.start != cb.start {
				return ca.start < cb.start
			}
			return ca.end < cb.end
		})
		var b strings.Builder
		pos, owner := 0, -1
		for _, c := range d.changes {
			if c.start < pos {
				fails = append(fails, Failure{c.op, path, fmt.Sprintf("overlaps op %d", owner+1)})
				continue
			}
			b.WriteString(d.text[pos:c.start])
			b.WriteString(c.text)
			pos, owner = c.end, c.op
		}
		b.WriteString(d.text[pos:])
		out[path] = d.enc.Encode(b.String())
	}
	if len(fails) > 0 {
		sort.SliceStable(fails, func(a, b int) bool { return fails[a].Op < fails[b].Op })
		return nil, fails
	}
	return out, nil
}

func resolve(text string, op Op) ([]change, string) {
	if op.Kind != Replace && op.New == "" {
		return nil, "nothing to insert"
	}
	if op.Lines != nil {
		return resolveLines(text, op)
	}
	if op.Old == "" {
		return nil, "empty text to find"
	}
	var cs []change
	for from := 0; ; {
		i := strings.Index(text[from:], op.Old)
		if i < 0 {
			break
		}
		start, end := from+i, from+i+len(op.Old)
		if op.Until != "" {
			j := strings.Index(text[end:], op.Until)
			if j < 0 {
				return nil, fmt.Sprintf("end anchor %s not found after line %d", quote(op.Until), lineOf(text, start))
			}
			end += j + len(op.Until)
		}
		cs = append(cs, change{start: start, end: end})
		from = end
	}
	if len(cs) == 0 {
		return nil, "not found: " + quote(op.Old) + similar(text, op.Old)
	}
	if len(cs) != op.Count {
		lines := make([]string, len(cs))
		for i, c := range cs {
			lines[i] = fmt.Sprint(lineOf(text, c.start))
		}
		return nil, fmt.Sprintf("found %d times, expected %d: %s at lines %s",
			len(cs), op.Count, quote(op.Old), strings.Join(lines, ", "))
	}
	for i, c := range cs {
		switch op.Kind {
		case Before:
			cs[i] = change{start: c.start, end: c.start, text: op.New}
		case After:
			cs[i] = change{start: c.end, end: c.end, text: op.New}
		default:
			cs[i].text = op.New
		}
	}
	return cs, ""
}

func resolveLines(text string, op Op) ([]change, string) {
	r := *op.Lines
	lines := Lines(text)
	if r.From < 1 || r.To < r.From {
		return nil, fmt.Sprintf("bad line range %d-%d", r.From, r.To)
	}
	if r.To > len(lines) {
		return nil, fmt.Sprintf("line %d is past the end: the file has %d lines", r.To, len(lines))
	}
	for _, end := range []struct {
		n    int
		hash string
	}{{r.From, r.FromHash}, {r.To, r.ToHash}} {
		if got := Hash(lines[end.n-1]); got != end.hash {
			return nil, fmt.Sprintf("line %d changed: %d#%s\t%s", end.n, end.n, got, lines[end.n-1])
		}
	}
	start := 0
	for _, l := range lines[:r.From-1] {
		start += len(l) + 1
	}
	end := start
	for _, l := range lines[r.From-1 : r.To] {
		end += len(l) + 1
	}
	eol := end <= len(text) // false when the last line has no terminator
	if !eol {
		end = len(text)
	}
	// New text is whole lines: it gets a terminator, except where it lands after an unterminated last line.
	newText := strings.TrimSuffix(op.New, "\n")
	if op.New != "" {
		newText += "\n"
	}
	switch {
	case op.Kind == Before:
		return []change{{start: start, end: start, text: newText}}, ""
	case op.Kind == After && eol:
		return []change{{start: end, end: end, text: newText}}, ""
	case op.Kind == After:
		return []change{{start: end, end: end, text: "\n" + strings.TrimSuffix(newText, "\n")}}, ""
	case !eol:
		newText = strings.TrimSuffix(newText, "\n")
	}
	return []change{{start: start, end: end, text: newText}}, ""
}

func lineOf(text string, offset int) int { return strings.Count(text[:offset], "\n") + 1 }

func quote(s string) string {
	first, _, more := strings.Cut(s, "\n")
	if more && strings.TrimSpace(s[len(first):]) != "" {
		first += "…"
	}
	return fmt.Sprintf("%q", first)
}

// similar lists up to three lines most like the first non-blank line of needle, ignoring indentation.
func similar(text, needle string) string {
	var probe string
	for _, l := range strings.Split(needle, "\n") {
		if probe = strings.TrimSpace(l); probe != "" {
			break
		}
	}
	if probe == "" {
		return ""
	}
	type hit struct {
		n     int
		score float64
	}
	var hits []hit
	m := difflib.NewMatcher(nil, strings.Split(probe, ""))
	lines := Lines(text)
	for i, l := range lines {
		m.SetSeq1(strings.Split(strings.TrimSpace(l), ""))
		if s := m.Ratio(); s >= 0.75 {
			hits = append(hits, hit{i + 1, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	if len(hits) > 3 {
		hits = hits[:3]
	}
	if len(hits) == 0 {
		return ""
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].n < hits[b].n })
	var b strings.Builder
	b.WriteString("; similar lines:")
	for _, h := range hits {
		fmt.Fprintf(&b, "\n  %d: %s", h.n, lines[h.n-1])
	}
	return b.String()
}
