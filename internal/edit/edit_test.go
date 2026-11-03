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
	return []edit.Failure{{Op: op, File: "f", Reason: reason}}
}

func TestApply(t *testing.T) {
	const src = "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}\n"
	tests := []struct {
		name  string
		files map[string][]byte
		ops   []edit.Op
		want  map[string][]byte
		fails []edit.Failure
	}{
		{
			name:  "replaces the only match",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "return 1", New: "return 10", Count: 1}},
			want:  map[string][]byte{"f": []byte("func a() {\n\treturn 10\n}\n\nfunc b() {\n\treturn 2\n}\n")},
		},
		{
			name:  "text not found names similar lines",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "  return 1\n", New: "x", Count: 1}},
			fails: fail(0, "not found: \"  return 1\"; similar lines:\n  2: \treturn 1\n  6: \treturn 2"),
		},
		{
			name:  "multi-line text not found names the first line that differs",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "func b() {\n\treturn 3\n}\n", New: "x", Count: 1, LineStart: true}},
			fails: fail(0, "not found: \"func b() {…\"; lines 5-5 match its first 1 lines, then the file has:\n  6: \treturn 2"),
		},
		{
			name:  "more matches than expected lists their lines",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "return", New: "yield", Count: 1}},
			fails: fail(0, `found 2 times, expected 1: "return" at lines 2, 6`),
		},
		{
			name:  "fewer matches than expected",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "return 1", New: "x", Count: 2}},
			fails: fail(0, `found 1 times, expected 2: "return 1" at lines 2`),
		},
		{
			name:  "count edits every match",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "return", New: "yield", Count: 2}},
			want:  map[string][]byte{"f": []byte("func a() {\n\tyield 1\n}\n\nfunc b() {\n\tyield 2\n}\n")},
		},
		{
			name:  "line start skips matches inside lines",
			files: map[string][]byte{"f": []byte("x := f()\nf()\n")},
			ops:   []edit.Op{{File: "f", Old: "f()\n", New: "g()\n", Count: 1, LineStart: true}},
			want:  map[string][]byte{"f": []byte("x := f()\ng()\n")},
		},
		{
			name:  "text only inside lines points to editing by arguments",
			files: map[string][]byte{"f": []byte("x := f()\n")},
			ops:   []edit.Op{{File: "f", Old: "f()\n", New: "g()\n", Count: 1, LineStart: true}},
			fails: fail(0, `not found at the start of a line: "f()"; batch text is whole lines, edit part of a line with arguments: catnip edit replace <path> <old> <new>`),
		},
		{
			name:  "less indentation than the file names the indented line",
			files: map[string][]byte{"f": []byte("{\n    foo();\n}\n")},
			ops:   []edit.Op{{File: "f", Old: "  foo();\n", New: "bar();\n", Count: 1, LineStart: true}},
			fails: fail(0, "not found at the start of a line: \"  foo();\"; batch text is whole lines, edit part of a line with arguments: catnip edit replace <path> <old> <new>; similar lines:\n  2:     foo();"),
		},
		{
			name:  "end anchor that only begins a line names that line first",
			files: map[string][]byte{"f": []byte("class A {\n    function f()\n    {\n        if (x) {\n        }\n    } // f\n}\n")},
			ops:   []edit.Op{{File: "f", Old: "    function f()\n", Until: "    }\n", Count: 1, LineStart: true}},
			fails: fail(0, "end anchor \"    }\" not found after line 2; similar lines:\n  5:         }\n  6:     } // f\n  7: }"),
		},
		{
			name:  "block from start anchor through end anchor",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "func b() {\n", Until: "}\n", New: "func b() {}\n", Count: 1}},
			want:  map[string][]byte{"f": []byte("func a() {\n\treturn 1\n}\n\nfunc b() {}\n")},
		},
		{
			name:  "block end anchor at line start skips a nested closing brace",
			files: map[string][]byte{"f": []byte("f() {\n\tif x {\n\t}\n\tb()\n}\nnext\n")},
			ops:   []edit.Op{{File: "f", Old: "f() {\n", Until: "}\n", Count: 1, LineStart: true}},
			want:  map[string][]byte{"f": []byte("next\n")},
		},
		{
			name:  "repeated block: every start runs to its own end anchor",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "func", Until: "}\n", New: "-\n", Count: 2}},
			want:  map[string][]byte{"f": []byte("-\n\n-\n")},
		},
		{
			name:  "block start anchor missing",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "func c() {\n", Until: "}\n", New: "", Count: 1}},
			fails: fail(0, "not found: \"func c() {\"; similar lines:\n  1: func a() {\n  5: func b() {"),
		},
		{
			name:  "block end anchor only before start names similar lines",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "\treturn 2\n", Until: "func a", New: "", Count: 1}},
			fails: fail(0, "end anchor \"func a\" not found after line 6; similar lines:\n  1: func a() {"),
		},
		{
			name:  "block start anchor found again without an end anchor",
			files: map[string][]byte{"f": []byte("start\nbody\nend\nstart\ntail\n")},
			ops:   []edit.Op{{File: "f", Old: "start\n", Until: "end\n", Count: 1}},
			fails: fail(0, `found at least 2 times, expected 1: "start" at lines 1, 4`),
		},
		{
			name:  "insert before and after a text anchor",
			files: map[string][]byte{"f": []byte(src)},
			ops: []edit.Op{
				{File: "f", Kind: edit.Before, Old: "func b", New: "// b\n", Count: 1},
				{File: "f", Kind: edit.After, Old: "\treturn 2\n", New: "\t// unreachable\n", Count: 1},
			},
			want: map[string][]byte{"f": []byte("func a() {\n\treturn 1\n}\n\n// b\nfunc b() {\n\treturn 2\n\t// unreachable\n}\n")},
		},
		{
			name:  "insert before a replaced text",
			files: map[string][]byte{"f": []byte("a\nb\n")},
			ops: []edit.Op{
				{File: "f", Old: "b\n", New: "B\n", Count: 1},
				{File: "f", Kind: edit.Before, Old: "b\n", New: "x\n", Count: 1},
			},
			want: map[string][]byte{"f": []byte("a\nx\nB\n")},
		},
		{
			name:  "empty new text deletes",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Old: "\nfunc b() {\n", Until: "}\n", Count: 1}},
			want:  map[string][]byte{"f": []byte("func a() {\n\treturn 1\n}\n")},
		},
		{
			name:  "empty insert is refused",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Kind: edit.After, Old: "func a", Count: 1}},
			fails: fail(0, "nothing to insert"),
		},
		{
			name:  "overlapping ops",
			files: map[string][]byte{"f": []byte(src)},
			ops: []edit.Op{
				{File: "f", Old: "func a() {\n\treturn 1\n", New: "x", Count: 1},
				{File: "f", Old: "return 1\n}", New: "y", Count: 1},
			},
			fails: fail(1, "overlaps op 1"),
		},
		{
			name:  "insert at the edge of a replaced span is not an overlap",
			files: map[string][]byte{"f": []byte("ab")},
			ops: []edit.Op{
				{File: "f", Old: "a", New: "A", Count: 1},
				{File: "f", Kind: edit.After, Old: "a", New: "+", Count: 1},
			},
			want: map[string][]byte{"f": []byte("A+b")},
		},
		{
			name:  "one failing op leaves every file of the batch unchanged",
			files: map[string][]byte{"f": []byte(src), "g": []byte("x\n")},
			ops: []edit.Op{
				{File: "g", Old: "x", New: "y", Count: 1},
				{File: "f", Old: "nope", New: "y", Count: 1},
			},
			fails: fail(1, `not found: "nope"`),
		},
		{
			name:  "ops across files",
			files: map[string][]byte{"f": []byte(src), "g": []byte("x\n")},
			ops: []edit.Op{
				{File: "g", Old: "x", New: "y", Count: 1},
				{File: "f", Old: "return 2", New: "return 3", Count: 1},
			},
			want: map[string][]byte{"g": []byte("y\n"), "f": []byte("func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 3\n}\n")},
		},
		{
			name:  "every op resolves against the original content",
			files: map[string][]byte{"f": []byte("a\nb\nc\n")},
			ops: []edit.Op{
				{File: "f", Old: "a\n", New: "a1\na2\n", Count: 1},
				{File: "f", Lines: lines(3, "c", 3, "c"), New: "C"},
			},
			want: map[string][]byte{"f": []byte("a1\na2\nb\nC\n")},
		},
		{
			name:  "line range replaced",
			files: map[string][]byte{"f": []byte(src)},
			ops:   []edit.Op{{File: "f", Lines: lines(5, "func b() {", 7, "}"), New: "func b() {}"}},
			want:  map[string][]byte{"f": []byte("func a() {\n\treturn 1\n}\n\nfunc b() {}\n")},
		},
		{
			name:  "line range deleted",
			files: map[string][]byte{"f": []byte("a\nb\nc\n")},
			ops:   []edit.Op{{File: "f", Lines: lines(1, "a", 2, "b")}},
			want:  map[string][]byte{"f": []byte("c\n")},
		},
		{
			name:  "insert before a line",
			files: map[string][]byte{"f": []byte("a\nb\n")},
			ops:   []edit.Op{{File: "f", Kind: edit.Before, Lines: lines(2, "b", 2, "b"), New: "x"}},
			want:  map[string][]byte{"f": []byte("a\nx\nb\n")},
		},
		{
			name:  "insert after an unterminated last line keeps it unterminated",
			files: map[string][]byte{"f": []byte("a\nb")},
			ops:   []edit.Op{{File: "f", Kind: edit.After, Lines: lines(2, "b", 2, "b"), New: "c\n"}},
			want:  map[string][]byte{"f": []byte("a\nb\nc")},
		},
		{
			name:  "replace an unterminated last line",
			files: map[string][]byte{"f": []byte("a\nb")},
			ops:   []edit.Op{{File: "f", Lines: lines(2, "b", 2, "b"), New: "B\n"}},
			want:  map[string][]byte{"f": []byte("a\nB")},
		},
		{
			name:  "line hash mismatch shows the current line",
			files: map[string][]byte{"f": []byte("a\nb\n")},
			ops:   []edit.Op{{File: "f", Lines: lines(2, "old b", 2, "old b"), New: "x"}},
			fails: fail(0, "line 2 changed: 2#"+edit.Hash("b")+"\tb"),
		},
		{
			name:  "hash mismatch at the end of a range",
			files: map[string][]byte{"f": []byte("a\nb\nc\n")},
			ops:   []edit.Op{{File: "f", Lines: lines(1, "a", 3, "old c"), New: "x"}},
			fails: fail(0, "line 3 changed: 3#"+edit.Hash("c")+"\tc"),
		},
		{
			name:  "line past the end",
			files: map[string][]byte{"f": []byte("a\n")},
			ops:   []edit.Op{{File: "f", Lines: lines(2, "", 2, ""), New: "x"}},
			fails: fail(0, "line 2 is past the end: the file has 1 lines"),
		},
		{
			name:  "CRLF is matched as LF and kept",
			files: map[string][]byte{"f": []byte("a\r\nb\r\n")},
			ops:   []edit.Op{{File: "f", Old: "a\nb\n", New: "a\nx\ny\n", Count: 1}},
			want:  map[string][]byte{"f": []byte("a\r\nx\r\ny\r\n")},
		},
		{
			name:  "CRLF text for a CRLF file",
			files: map[string][]byte{"f": []byte("a\r\nb\r\n")},
			ops:   []edit.Op{{File: "f", Old: "a\r\n", New: "A\r\n", Count: 1}},
			want:  map[string][]byte{"f": []byte("A\r\nb\r\n")},
		},
		{
			name:  "mixed line terminators stay as they were",
			files: map[string][]byte{"f": []byte("a\r\nb\nc\r\n")},
			ops:   []edit.Op{{File: "f", Old: "b\n", New: "B\n", Count: 1}},
			want:  map[string][]byte{"f": []byte("a\r\nB\nc\r\n")},
		},
		{
			name:  "BOM is kept",
			files: map[string][]byte{"f": []byte("\ufeffa\nb\n")},
			ops:   []edit.Op{{File: "f", Lines: lines(1, "a", 1, "a"), New: "A"}},
			want:  map[string][]byte{"f": []byte("\ufeffA\nb\n")},
		},
		{
			name:  "missing final newline is kept",
			files: map[string][]byte{"f": []byte("a\nb")},
			ops:   []edit.Op{{File: "f", Old: "a", New: "A", Count: 1}},
			want:  map[string][]byte{"f": []byte("A\nb")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fails := edit.Apply(tt.files, tt.ops)

			assert.Equal(t, tt.fails, fails)
			assert.Equal(t, tt.want, got)
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
