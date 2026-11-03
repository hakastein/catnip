// Package edit applies edits to a file's content in memory.
package edit

import (
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Op is one replacement. The target is either text (Old) or whole lines (Lines); an empty New deletes it.
type Op struct {
	// Old is the exact text to find.
	Old string
	// Count is how many times Old must occur; every occurrence is replaced.
	Count int
	// Lines addresses whole lines instead of text.
	Lines *LineRange
	New   string
}

// LineRange is lines From..To (1-based, From <= To), each end checked against its Hash.
type LineRange struct {
	From, To         int
	FromHash, ToHash string
}

// Failure is why the edit with index Op could not be applied.
type Failure struct {
	Op     int
	Reason string
}

func (f Failure) Error() string { return fmt.Sprintf("edit %d: %s", f.Op+1, f.Reason) }

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
		e.CRLF, s = true, lf(s)
	}
	return s, e
}

func lf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

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

// Keep gives data the BOM, line terminator style and final newline of the existing content old.
// An old content without a line break has no terminator style, so data keeps its own.
func Keep(old, data []byte) []byte {
	if len(old) == 0 || len(data) == 0 {
		return data
	}
	oldText, enc := Decode(old)
	text, own := Decode(data)
	if !strings.Contains(oldText, "\n") {
		enc.CRLF = own.CRLF
	}
	if enc.CRLF && !own.CRLF {
		text = lf(text) // mixed input: Decode left its CRLF in place
	}
	if oldText != "" && text != "" {
		text = strings.TrimSuffix(text, "\n")
		if strings.HasSuffix(oldText, "\n") {
			text += "\n"
		}
	}
	return enc.Encode(text)
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
	// chop drops the line break before the change: deleting an unterminated last line keeps the file unterminated.
	chop bool
}

// Apply applies ops to the raw content of one file. Every target is resolved against the original content, so line
// numbers and text are the ones the caller saw. Any failure means no change: the result is nil and every failing
// edit is reported.
func Apply(raw []byte, ops []Op) ([]byte, []Failure) {
	text, enc := Decode(raw)
	var changes []change
	var fails []Failure
	for i, op := range ops {
		if enc.CRLF { // the caller may send CRLF text for a CRLF file
			op.Old, op.New = lf(op.Old), lf(op.New)
		}
		cs, reason := resolve(text, op)
		if reason != "" {
			fails = append(fails, Failure{i, reason})
			continue
		}
		for j := range cs {
			cs[j].op = i
		}
		changes = append(changes, cs...)
	}
	sort.SliceStable(changes, func(a, b int) bool { return changes[a].start < changes[b].start })
	var b strings.Builder
	pos, owner, chop := 0, -1, false
	for _, c := range changes {
		if c.start < pos {
			f := Failure{c.op, fmt.Sprintf("overlaps edit %d", owner+1)}
			if !slices.Contains(fails, f) { // every match of a --count edit overlaps alike
				fails = append(fails, f)
			}
			continue
		}
		b.WriteString(text[pos:c.start])
		b.WriteString(c.text)
		pos, owner, chop = c.end, c.op, c.chop
	}
	if len(fails) > 0 {
		sort.SliceStable(fails, func(a, b int) bool { return fails[a].Op < fails[b].Op })
		return nil, fails
	}
	b.WriteString(text[pos:])
	out := b.String()
	if chop {
		out = strings.TrimSuffix(out, "\n")
	}
	return enc.Encode(out), nil
}

func resolve(text string, op Op) ([]change, string) {
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
		start := from + i
		from = start + len(op.Old)
		cs = append(cs, change{start: start, end: from, text: op.New})
	}
	if len(cs) == 0 {
		return nil, "not found: " + quote(op.Old) + diagnose(text, op.Old)
	}
	if len(cs) != op.Count {
		lines := make([]string, len(cs))
		for i, c := range cs {
			lines[i] = fmt.Sprint(lineOf(text, c.start))
		}
		return nil, fmt.Sprintf("found %d times, expected %d: %s at lines %s",
			len(cs), op.Count, quote(op.Old), strings.Join(lines, ", "))
	}
	return cs, ""
}

func resolveLines(text string, op Op) ([]change, string) {
	r := *op.Lines
	lines := Lines(text)
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
	// New text is whole lines: it ends with a terminator unless it replaces an unterminated last line, and deleting
	// that line also drops the terminator before it, so the file still ends without one.
	newText := strings.TrimSuffix(op.New, "\n")
	if end > len(text) {
		return []change{{start: start, end: len(text), text: newText, chop: op.New == ""}}, ""
	}
	if op.New != "" {
		newText += "\n"
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

// diagnose explains a text that was not found: where a multi-line text stops matching the file, or else the lines
// most like its first line.
func diagnose(text, needle string) string {
	want, lines := Lines(needle), Lines(text)
	bestAt, best := 0, 0
	for i := range lines {
		k := 0
		for k < len(want) && i+k < len(lines) && lines[i+k] == want[k] {
			k++
		}
		if k > best {
			bestAt, best = i, k
		}
	}
	if len(want) < 2 || best == 0 || best == len(want) {
		return similar(text, needle)
	}
	n := bestAt + best + 1
	if n > len(lines) {
		return fmt.Sprintf("; lines %d-%d match its first %d lines, then the file ends", bestAt+1, n-1, best)
	}
	return fmt.Sprintf("; lines %d-%d match its first %d lines, then the file has:\n  %d: %s",
		bestAt+1, n-1, best, n, lines[n-1])
}

// similar lists up to three lines most like the first non-blank line of needle, ignoring indentation. Lines that
// begin with that line exactly, indentation included, come first: they only have more text after it.
func similar(text, needle string) string {
	var raw, probe string
	for _, l := range strings.Split(needle, "\n") {
		if raw, probe = l, strings.TrimSpace(l); probe != "" {
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
		if strings.HasPrefix(l, raw) {
			hits = append(hits, hit{i + 1, 2})
			continue
		}
		m.SetSeq1(strings.Split(strings.TrimSpace(l), ""))
		if s := m.Ratio(); s >= 0.75 {
			hits = append(hits, hit{i + 1, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	if len(hits) == 0 { // a short text: look for lines holding its longest prefix
		r := []rune(probe)
		for k := len(r) - 1; k >= max(4, len(r)/2) && len(hits) == 0; k-- {
			for i, l := range lines {
				if strings.Contains(l, string(r[:k])) {
					hits = append(hits, hit{i + 1, 0})
				}
			}
		}
	}
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
