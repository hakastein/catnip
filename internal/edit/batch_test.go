package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hakastein/catnip/internal/edit"
)

func TestParseBatch(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []edit.Op
		err  string
	}{
		{
			name: "replace keeps quotes, backslashes, dollars and triple quotes verbatim",
			in:   "@@@ replace a.php\n$x = \"a\\n\";\n@@@ with\n$x = '''b\\t''' . `c`;\n@@@ end\n",
			want: []edit.Op{{File: "a.php", Old: "$x = \"a\\n\";\n", New: "$x = '''b\\t''' . `c`;\n", Count: 1}},
		},
		{
			name: "lines like markers are text",
			in:   "@@@ replace d.diff\n@@ -1,2 +1,3 @@\n  @@@ with\n@@@\n@@@ with\n@@@end\n@@@ end\n",
			want: []edit.Op{{File: "d.diff", Old: "@@ -1,2 +1,3 @@\n  @@@ with\n@@@\n", New: "@@@end\n", Count: 1}},
		},
		{
			name: "count, block, inserts and blank lines between ops",
			in: "@@@ replace*2 a b.go\nx\n@@@ with\n@@@ end\n\n" +
				"@@@ block c.go\nfunc f() {\n@@@ through\n}\n@@@ with\nfunc f() {}\n@@@ end\n" +
				"@@@ before c.go\nfunc g\n@@@ insert\n// g\n@@@ end\n" +
				"@@@ after c.go\n}\n@@@ insert\n\n@@@ end\n",
			want: []edit.Op{
				{File: "a b.go", Old: "x\n", Count: 2},
				{File: "c.go", Old: "func f() {\n", Until: "}\n", New: "func f() {}\n", Count: 1},
				{File: "c.go", Kind: edit.Before, Old: "func g\n", New: "// g\n", Count: 1},
				{File: "c.go", Kind: edit.After, Old: "}\n", New: "\n", Count: 1},
			},
		},
		{
			name: "line targets",
			in:   "@@@ replace a.go:3#00ff-5#abcd\n@@@ with\nx\n@@@ end\n@@@ after a.go:7#1234\n@@@ insert\ny\n@@@ end\n",
			want: []edit.Op{
				{File: "a.go", Lines: &edit.LineRange{From: 3, To: 5, FromHash: "00ff", ToHash: "abcd"}, New: "x\n", Count: 1},
				{File: "a.go", Kind: edit.After, Lines: &edit.LineRange{From: 7, To: 7, FromHash: "1234", ToHash: "1234"}, New: "y\n", Count: 1},
			},
		},
		{name: "empty", in: "\n", err: "empty batch"},
		{name: "text before an op", in: "hello\n@@@ replace a\nx\n@@@ with\n@@@ end\n", err: `line 1: text outside an op: "hello"`},
		{name: "unknown verb", in: "@@@ -1,2 +1,2 @@@\n", err: `line 1: "@@@ -1,2 +1,2 @@@" is not an op header: want @@@ <verb>[*N] <target>`},
		{name: "missing end", in: "@@@ replace a\nx\n@@@ with\ny\n", err: "replace a: missing @@@ end"},
		{name: "wrong section", in: "@@@ replace a\nx\n@@@ insert\ny\n@@@ end\n", err: `line 3: replace a: unexpected marker "@@@ insert"`},
		{name: "missing section", in: "@@@ block a\nx\n@@@ with\ny\n@@@ end\n", err: `line 3: block a: unexpected marker "@@@ with"`},
		{name: "empty text to find", in: "@@@ replace a\n@@@ with\ny\n@@@ end\n", err: "line 4: replace a: empty text to find"},
		{name: "line target with text to find", in: "@@@ replace a:1#0000\nx\n@@@ with\ny\n@@@ end\n", err: "line 5: replace a:1#0000: a line target takes no text to find"},
		{name: "line target with count", in: "@@@ replace*2 a:1#0000\n@@@ with\ny\n@@@ end\n", err: "line 4: replace*2 a:1#0000: a line target takes no block or count"},
		{name: "bad count", in: "@@@ replace*0 a\nx\n@@@ with\n@@@ end\n", err: `line 4: replace*0 a: bad count "0"`},
		{name: "missing target", in: "@@@ replace\nx\n@@@ with\n@@@ end\n", err: "line 4: replace: missing target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := edit.ParseBatch(tt.in)
			assert.Equal(t, tt.err, errText(err))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want edit.Op
		err  string
	}{
		{name: "replace text", args: []string{"replace", "a", "x", "y"}, want: edit.Op{File: "a", Old: "x", New: "y", Count: 1}},
		{name: "block", args: []string{"block", "a", "s", "e", ""}, want: edit.Op{File: "a", Old: "s", Until: "e", Count: 1}},
		{
			name: "line target",
			args: []string{"before", "a:2#beef", "y"},
			want: edit.Op{File: "a", Kind: edit.Before, Lines: &edit.LineRange{From: 2, To: 2, FromHash: "beef", ToHash: "beef"}, New: "y", Count: 1},
		},
		{name: "too few texts", args: []string{"replace", "a", "x"}, err: "want 2 texts, got 1"},
		{name: "unknown verb", args: []string{"swap", "a", "x", "y"}, err: `unknown verb "swap": want replace, block, before or after`},
		{name: "no target", args: []string{"replace"}, err: "usage: edit <verb>[*N] <target> <text>..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := edit.ParseArgs(tt.args)
			assert.Equal(t, tt.err, errText(err))
			assert.Equal(t, tt.want, got)
		})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
