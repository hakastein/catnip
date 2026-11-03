package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hakastein/catnip/internal/edit"
)

func lines(from int, fromLine string, to int, toLine string) *edit.LineRange {
	return &edit.LineRange{From: from, To: to, FromHash: edit.Hash(fromLine), ToHash: edit.Hash(toLine)}
}

func TestApply(t *testing.T) {
	const src = "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}\n"
	tests := []struct {
		name  string
		files map[string]string
		ops   []edit.Op
		want  map[string]string
		fails []string
	}{
		{
			name:  "replaces the only match",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "return 1", New: "return 10", Count: 1}},
			want:  map[string]string{"f": "func a() {\n\treturn 10\n}\n\nfunc b() {\n\treturn 2\n}\n"},
		},
		{
			name:  "text not found names similar lines",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "  return 1\n", New: "x", Count: 1}},
			fails: []string{"op 1 (f): not found: \"  return 1\"; similar lines:\n  2: \treturn 1\n  6: \treturn 2"},
		},
		{
			name:  "more matches than expected lists their lines",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "return", New: "yield", Count: 1}},
			fails: []string{`op 1 (f): found 2 times, expected 1: "return" at lines 2, 6`},
		},
		{
			name:  "fewer matches than expected",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "return 1", New: "x", Count: 2}},
			fails: []string{`op 1 (f): found 1 times, expected 2: "return 1" at lines 2`},
		},
		{
			name:  "count edits every match",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "return", New: "yield", Count: 2}},
			want:  map[string]string{"f": "func a() {\n\tyield 1\n}\n\nfunc b() {\n\tyield 2\n}\n"},
		},
		{
			name:  "block from start anchor through end anchor",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "func b() {\n", Until: "}\n", New: "func b() {}\n", Count: 1}},
			want:  map[string]string{"f": "func a() {\n\treturn 1\n}\n\nfunc b() {}\n"},
		},
		{
			name:  "block start anchor missing",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "func c() {\n", Until: "}\n", New: "", Count: 1}},
			fails: []string{"op 1 (f): not found: \"func c() {\"; similar lines:\n  1: func a() {\n  5: func b() {"},
		},
		{
			name:  "block end anchor only before start",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "\treturn 2\n", Until: "func a", New: "", Count: 1}},
			fails: []string{`op 1 (f): end anchor "func a" not found after line 6`},
		},
		{
			name:  "insert before and after a text anchor",
			files: map[string]string{"f": src},
			ops: []edit.Op{
				{File: "f", Kind: edit.Before, Old: "func b", New: "// b\n", Count: 1},
				{File: "f", Kind: edit.After, Old: "\treturn 2\n", New: "\t// unreachable\n", Count: 1},
			},
			want: map[string]string{"f": "func a() {\n\treturn 1\n}\n\n// b\nfunc b() {\n\treturn 2\n\t// unreachable\n}\n"},
		},
		{
			name:  "empty new text deletes",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Old: "\nfunc b() {\n", Until: "}\n", Count: 1}},
			want:  map[string]string{"f": "func a() {\n\treturn 1\n}\n"},
		},
		{
			name:  "empty insert is refused",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Kind: edit.After, Old: "func a", Count: 1}},
			fails: []string{"op 1 (f): nothing to insert"},
		},
		{
			name:  "overlapping ops",
			files: map[string]string{"f": src},
			ops: []edit.Op{
				{File: "f", Old: "func a() {\n\treturn 1\n", New: "x", Count: 1},
				{File: "f", Old: "return 1\n}", New: "y", Count: 1},
			},
			fails: []string{"op 2 (f): overlaps op 1"},
		},
		{
			name:  "insert at the edge of a replaced span is not an overlap",
			files: map[string]string{"f": "ab"},
			ops: []edit.Op{
				{File: "f", Old: "a", New: "A", Count: 1},
				{File: "f", Kind: edit.After, Old: "a", New: "+", Count: 1},
			},
			want: map[string]string{"f": "A+b"},
		},
		{
			name:  "one failing op leaves every file of the batch unchanged",
			files: map[string]string{"f": src, "g": "x\n"},
			ops: []edit.Op{
				{File: "g", Old: "x", New: "y", Count: 1},
				{File: "f", Old: "nope", New: "y", Count: 1},
			},
			fails: []string{`op 2 (f): not found: "nope"`},
		},
		{
			name:  "ops across files",
			files: map[string]string{"f": src, "g": "x\n"},
			ops: []edit.Op{
				{File: "g", Old: "x", New: "y", Count: 1},
				{File: "f", Old: "return 2", New: "return 3", Count: 1},
			},
			want: map[string]string{"g": "y\n", "f": "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 3\n}\n"},
		},
		{
			name:  "every op resolves against the original content",
			files: map[string]string{"f": "a\nb\nc\n"},
			ops: []edit.Op{
				{File: "f", Old: "a\n", New: "a1\na2\n", Count: 1},
				{File: "f", Lines: lines(3, "c", 3, "c"), New: "C"},
			},
			want: map[string]string{"f": "a1\na2\nb\nC\n"},
		},
		{
			name:  "line range replaced",
			files: map[string]string{"f": src},
			ops:   []edit.Op{{File: "f", Lines: lines(5, "func b() {", 7, "}"), New: "func b() {}"}},
			want:  map[string]string{"f": "func a() {\n\treturn 1\n}\n\nfunc b() {}\n"},
		},
		{
			name:  "line range deleted",
			files: map[string]string{"f": "a\nb\nc\n"},
			ops:   []edit.Op{{File: "f", Lines: lines(1, "a", 2, "b")}},
			want:  map[string]string{"f": "c\n"},
		},
		{
			name:  "insert before a line",
			files: map[string]string{"f": "a\nb\n"},
			ops:   []edit.Op{{File: "f", Kind: edit.Before, Lines: lines(2, "b", 2, "b"), New: "x"}},
			want:  map[string]string{"f": "a\nx\nb\n"},
		},
		{
			name:  "insert after an unterminated last line keeps it unterminated",
			files: map[string]string{"f": "a\nb"},
			ops:   []edit.Op{{File: "f", Kind: edit.After, Lines: lines(2, "b", 2, "b"), New: "c\n"}},
			want:  map[string]string{"f": "a\nb\nc"},
		},
		{
			name:  "replace an unterminated last line",
			files: map[string]string{"f": "a\nb"},
			ops:   []edit.Op{{File: "f", Lines: lines(2, "b", 2, "b"), New: "B\n"}},
			want:  map[string]string{"f": "a\nB"},
		},
		{
			name:  "line hash mismatch shows the current line",
			files: map[string]string{"f": "a\nb\n"},
			ops:   []edit.Op{{File: "f", Lines: lines(2, "old b", 2, "old b"), New: "x"}},
			fails: []string{"op 1 (f): line 2 changed: 2#" + edit.Hash("b") + "\tb"},
		},
		{
			name:  "line past the end",
			files: map[string]string{"f": "a\n"},
			ops:   []edit.Op{{File: "f", Lines: lines(2, "", 2, ""), New: "x"}},
			fails: []string{"op 1 (f): line 2 is past the end: the file has 1 lines"},
		},
		{
			name:  "CRLF is matched as LF and kept",
			files: map[string]string{"f": "a\r\nb\r\n"},
			ops:   []edit.Op{{File: "f", Old: "a\nb\n", New: "a\nx\ny\n", Count: 1}},
			want:  map[string]string{"f": "a\r\nx\r\ny\r\n"},
		},
		{
			name:  "BOM is kept",
			files: map[string]string{"f": "\ufeffa\nb\n"},
			ops:   []edit.Op{{File: "f", Lines: lines(1, "a", 1, "a"), New: "A"}},
			want:  map[string]string{"f": "\ufeffA\nb\n"},
		},
		{
			name:  "missing final newline is kept",
			files: map[string]string{"f": "a\nb"},
			ops:   []edit.Op{{File: "f", Old: "a", New: "A", Count: 1}},
			want:  map[string]string{"f": "A\nb"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string][]byte{}
			for p, c := range tt.files {
				files[p] = []byte(c)
			}
			got, fails := edit.Apply(files, tt.ops)
			msgs := []string(nil)
			for _, f := range fails {
				msgs = append(msgs, f.Error())
			}
			assert.Equal(t, tt.fails, msgs)
			var want map[string][]byte
			if tt.want != nil {
				want = map[string][]byte{}
				for p, c := range tt.want {
					want[p] = []byte(c)
				}
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name          string
		before, after string
		want          string
	}{
		{"changed lines with the new line number", "a\nb\nc\nd\n", "a\nB\nc\nd\ne\n", "f:2\n-\tb\n+\tB\nf:5\n+\te\n"},
		{"no change", "a\n", "a\n", ""},
		{"only terminators", "a\n", "a\r\n", "f: only line terminators or BOM changed\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, edit.Diff("f", []byte(tt.before), []byte(tt.after)))
		})
	}
}
