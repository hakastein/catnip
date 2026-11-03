package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hakastein/catnip/internal/edit"
)

func lines(from int, fromLine string, to int, toLine string) *edit.LineRange {
	return &edit.LineRange{From: from, To: to, FromHash: edit.Hash(fromLine), ToHash: edit.Hash(toLine)}
}

func fail(op int, reason string) []edit.Failure {
	return []edit.Failure{{Op: op, Reason: reason}}
}

func TestApply(t *testing.T) {
	const src = "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}\n"
	tests := []struct {
		name  string
		file  string
		ops   []edit.Op
		want  string
		fails []edit.Failure
	}{
		{
			name: "replaces the only match",
			file: src,
			ops:  []edit.Op{{Old: "return 1", New: "return 10", Count: 1}},
			want: "func a() {\n\treturn 10\n}\n\nfunc b() {\n\treturn 2\n}\n",
		},
		{
			name: "replaces inside a line",
			file: "x := f(a)\n",
			ops:  []edit.Op{{Old: "f(", New: "g(", Count: 1}},
			want: "x := g(a)\n",
		},
		{
			name: "several edits of one file",
			file: src,
			ops: []edit.Op{
				{Old: "return 1", New: "return 10", Count: 1},
				{Old: "func b()", New: "func c()", Count: 1},
			},
			want: "func a() {\n\treturn 10\n}\n\nfunc c() {\n\treturn 2\n}\n",
		},
		{
			name: "count edits every match",
			file: src,
			ops:  []edit.Op{{Old: "return", New: "yield", Count: 2}},
			want: "func a() {\n\tyield 1\n}\n\nfunc b() {\n\tyield 2\n}\n",
		},
		{
			name: "empty new text deletes",
			file: src,
			ops:  []edit.Op{{Old: "\nfunc b() {\n\treturn 2\n}\n", Count: 1}},
			want: "func a() {\n\treturn 1\n}\n",
		},
		{
			name:  "text not found names similar lines",
			file:  src,
			ops:   []edit.Op{{Old: "  return 1\n", New: "x", Count: 1}},
			fails: fail(0, "not found: \"  return 1\"; similar lines:\n  2: \treturn 1\n  6: \treturn 2"),
		},
		{
			name:  "lines that begin with the text come first among similar lines",
			file:  "{\n\t}\n\t}\n\t}\n    } // f\n}\n",
			ops:   []edit.Op{{Old: "    }\n}\nx", New: "", Count: 1}},
			fails: fail(0, "not found: \"    }…\"; similar lines:\n  2: \t}\n  3: \t}\n  5:     } // f"),
		},
		{
			name:  "multi-line text not found names the first line that differs",
			file:  src,
			ops:   []edit.Op{{Old: "func b() {\n\treturn 3\n}\n", New: "x", Count: 1}},
			fails: fail(0, "not found: \"func b() {…\"; lines 5-5 match its first 1 lines, then the file has:\n  6: \treturn 2"),
		},
		{
			name:  "multi-line text runs past the end of the file",
			file:  "a\nb\n",
			ops:   []edit.Op{{Old: "a\nb\nc\n", New: "x", Count: 1}},
			fails: fail(0, "not found: \"a…\"; lines 1-2 match its first 2 lines, then the file ends"),
		},
		{
			name:  "more matches than expected lists their lines",
			file:  src,
			ops:   []edit.Op{{Old: "return", New: "yield", Count: 1}},
			fails: fail(0, `found 2 times, expected 1: "return" at lines 2, 6`),
		},
		{
			name:  "fewer matches than expected",
			file:  src,
			ops:   []edit.Op{{Old: "return 1", New: "x", Count: 2}},
			fails: fail(0, `found 1 times, expected 2: "return 1" at lines 2`),
		},
		{
			name:  "empty text to find",
			file:  src,
			ops:   []edit.Op{{Old: "", New: "x", Count: 1}},
			fails: fail(0, "empty text to find"),
		},
		{
			name: "overlapping edits",
			file: src,
			ops: []edit.Op{
				{Old: "func a() {\n\treturn 1\n", New: "x", Count: 1},
				{Old: "return 1\n}", New: "y", Count: 1},
			},
			fails: fail(1, "overlaps edit 1"),
		},
		{
			name: "one failing edit leaves the file unchanged and every failure is reported",
			file: src,
			ops: []edit.Op{
				{Old: "nope", New: "y", Count: 1},
				{Old: "return 1", New: "return 10", Count: 1},
				{Old: "return", New: "y", Count: 1},
			},
			fails: []edit.Failure{
				{Op: 0, Reason: `not found: "nope"`},
				{Op: 2, Reason: `found 2 times, expected 1: "return" at lines 2, 6`},
			},
		},
		{
			name: "edits out of file order and adjacent both apply",
			file: "a\nb\nc\n",
			ops: []edit.Op{
				{Lines: lines(2, "b", 2, "b"), New: "B"},
				{Old: "a\n", New: "A\n", Count: 1},
			},
			want: "A\nB\nc\n",
		},
		{
			name: "failures are reported in edit order",
			file: "abc\n",
			ops: []edit.Op{
				{Old: "bc", New: "x", Count: 1},
				{Old: "ab", New: "y", Count: 1},
				{Old: "nope", New: "z", Count: 1},
			},
			fails: []edit.Failure{{Op: 0, Reason: "overlaps edit 2"}, {Op: 2, Reason: `not found: "nope"`}},
		},
		{
			name: "an overlapping counted edit is reported once",
			file: "xaxa\n",
			ops: []edit.Op{
				{Lines: lines(1, "xaxa", 1, "xaxa"), New: "y"},
				{Old: "a", New: "b", Count: 2},
			},
			fails: fail(1, "overlaps edit 1"),
		},
		{
			name:  "short text not found names the line holding its longest prefix",
			file:  "func f() {\n    return $a->renamed(20);\n}\n",
			ops:   []edit.Op{{Old: "renamed(200)", New: "renamed(30)", Count: 1}},
			fails: fail(0, "not found: \"renamed(200)\"; similar lines:\n  2:     return $a->renamed(20);"),
		},
		{
			name:  "CRLF text does not match an LF line of a mixed file",
			file:  "a\r\nb\nc\r\n",
			ops:   []edit.Op{{Old: "b\r\n", New: "B\r\n", Count: 1}},
			fails: fail(0, "not found: \"b\\r\"; similar lines:\n  2: b"),
		},
		{
			name: "deleting an unterminated last line keeps the file unterminated",
			file: "a\nb",
			ops:  []edit.Op{{Lines: lines(2, "b", 2, "b")}},
			want: "a",
		},
		{
			name: "every edit resolves against the original content",
			file: "a\nb\nc\n",
			ops: []edit.Op{
				{Old: "a\n", New: "a1\na2\n", Count: 1},
				{Lines: lines(3, "c", 3, "c"), New: "C"},
			},
			want: "a1\na2\nb\nC\n",
		},
		{
			name: "line range replaced",
			file: src,
			ops:  []edit.Op{{Lines: lines(5, "func b() {", 7, "}"), New: "func b() {}"}},
			want: "func a() {\n\treturn 1\n}\n\nfunc b() {}\n",
		},
		{
			name: "line range deleted",
			file: "a\nb\nc\n",
			ops:  []edit.Op{{Lines: lines(1, "a", 2, "b")}},
			want: "c\n",
		},
		{
			name: "replace an unterminated last line",
			file: "a\nb",
			ops:  []edit.Op{{Lines: lines(2, "b", 2, "b"), New: "B\n"}},
			want: "a\nB",
		},
		{
			name:  "line hash mismatch shows the current line",
			file:  "a\nb\n",
			ops:   []edit.Op{{Lines: lines(2, "old b", 2, "old b"), New: "x"}},
			fails: fail(0, "line 2 changed: 2#"+edit.Hash("b")+"\tb"),
		},
		{
			name:  "hash mismatch at the end of a range",
			file:  "a\nb\nc\n",
			ops:   []edit.Op{{Lines: lines(1, "a", 3, "old c"), New: "x"}},
			fails: fail(0, "line 3 changed: 3#"+edit.Hash("c")+"\tc"),
		},
		{
			name:  "line past the end",
			file:  "a\n",
			ops:   []edit.Op{{Lines: lines(2, "", 2, ""), New: "x"}},
			fails: fail(0, "line 2 is past the end: the file has 1 lines"),
		},
		{
			name: "CRLF is matched as LF and kept",
			file: "a\r\nb\r\n",
			ops:  []edit.Op{{Old: "a\nb\n", New: "a\nx\ny\n", Count: 1}},
			want: "a\r\nx\r\ny\r\n",
		},
		{
			name: "CRLF text for a CRLF file",
			file: "a\r\nb\r\n",
			ops:  []edit.Op{{Old: "a\r\n", New: "A\r\n", Count: 1}},
			want: "A\r\nb\r\n",
		},
		{
			name: "CRLF file keeps a stray CR in an untouched line",
			file: "a\r\r\nb\r\n",
			ops:  []edit.Op{{Old: "b", New: "B", Count: 1}},
			want: "a\r\r\nB\r\n",
		},
		{
			name: "mixed line terminators stay as they were",
			file: "a\r\nb\nc\r\n",
			ops:  []edit.Op{{Old: "b\n", New: "B\n", Count: 1}},
			want: "a\r\nB\nc\r\n",
		},
		{
			name: "BOM is kept",
			file: "\ufeffa\nb\n",
			ops:  []edit.Op{{Lines: lines(1, "a", 1, "a"), New: "A"}},
			want: "\ufeffA\nb\n",
		},
		{
			name: "missing final newline is kept",
			file: "a\nb",
			ops:  []edit.Op{{Old: "a", New: "A", Count: 1}},
			want: "A\nb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fails := edit.Apply([]byte(tt.file), tt.ops)

			assert.Equal(t, tt.fails, fails)
			assert.Equal(t, tt.want, string(got))
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
		{"hunk below an inserted line", "a\nb\nc\n", "z\na\nb\nC\n", "f:1\n+\tz\nf:4\n-\tc\n+\tC\n"},
		{"no change", "a\n", "a\n", ""},
		{"BOM and CRLF dropped along with a change", "\ufeffa\r\nb\r\n", "a\nB\n", "f:2\n-\tb\n+\tB\nf: BOM removed\nf: line terminators CRLF → LF\n"},
		{"BOM and CRLF added", "a\n", "\ufeffa\r\n", "f: BOM added\nf: line terminators LF → CRLF\n"},
		{"final newline only", "a", "a\n", "f: only line terminators changed\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, edit.Diff("f", []byte(tt.before), []byte(tt.after)))
		})
	}
}
