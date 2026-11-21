package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hakastein/catnip/internal/edit"
)

func TestAdd(t *testing.T) {
	replace := edit.Op{Old: "a", New: "b", Count: 1}
	lines := edit.Op{Lines: &edit.LineRange{From: 1, To: 1, FromHash: "0000", ToHash: "0000"}}
	tests := []struct {
		name   string
		ops    []edit.Op
		edit   string
		values []string
		want   []edit.Op
		err    string
	}{
		{name: "replace kept as given", ops: []edit.Op{replace}, edit: "replace", values: []string{"--old \"$x\" \\", "--skip-checks"}, want: []edit.Op{replace, {Old: "--old \"$x\" \\", New: "--skip-checks", Count: 1}}},
		{name: "replace with an empty NEW", edit: "replace", values: []string{"a", ""}, want: []edit.Op{{Old: "a", Count: 1}}},
		{name: "replace without NEW", edit: "replace", values: []string{"a"}, err: `want OLD and NEW, got "a"`},
		{name: "replace without values", edit: "replace", err: "want OLD and NEW, got nothing"},
		{name: "empty text to find", edit: "replace", values: []string{"", "b"}, err: `"" "b": OLD, the text to find, is empty; OLD goes first, NEW second`},
		{name: "long text cut in the error", edit: "replace", values: []string{"", "0123456789012345678901234567890123456789+"}, err: `"" "0123456789012345678901234567890123456789"…: OLD, the text to find, is empty; OLD goes first, NEW second`},
		{name: "count of the last replace", ops: []edit.Op{replace}, edit: "count", values: []string{"2"}, want: []edit.Op{{Old: "a", New: "b", Count: 2}}},
		{name: "count without number", ops: []edit.Op{replace}, edit: "count", err: "want N"},
		{name: "bad count", ops: []edit.Op{replace}, edit: "count", values: []string{"0"}, err: `bad number "0"`},
		{name: "count after lines", ops: []edit.Op{lines}, edit: "count", values: []string{"2"}, err: "goes after a --replace"},
		{name: "count first", edit: "count", values: []string{"2"}, err: "goes after a --replace"},
		{name: "lines range", edit: "lines", values: []string{"3#00ff-5#abcd", "x"}, want: []edit.Op{{Lines: &edit.LineRange{From: 3, To: 5, FromHash: "00ff", ToHash: "abcd"}, New: "x"}}},
		{name: "lines deleted", edit: "lines", values: []string{"1#0000", ""}, want: []edit.Op{lines}},
		{name: "bad line range", edit: "lines", values: []string{"1-2", "x"}, err: `"1-2" is not FROM#HASH[-TO#HASH] as catnip show prints it`},
		{name: "text before the line number", edit: "lines", values: []string{"x1#0000", "x"}, err: `"x1#0000" is not FROM#HASH[-TO#HASH] as catnip show prints it`},
		{name: "hash too long", edit: "lines", values: []string{"1#00000", "x"}, err: `"1#00000" is not FROM#HASH[-TO#HASH] as catnip show prints it`},
		{name: "line zero", edit: "lines", values: []string{"0#0000", "x"}, err: `"0#0000": lines start at 1`},
		{name: "reversed range", edit: "lines", values: []string{"3#0000-1#0000", "x"}, err: `"3#0000-1#0000": the range ends before it starts`},
		{name: "line number too big", edit: "lines", values: []string{"99999999999999999999#0000", "x"}, err: `strconv.Atoi: parsing "99999999999999999999": value out of range`},
		{name: "lines without new", edit: "lines", values: []string{"1#0000"}, err: `want FROM#HASH[-TO#HASH] and NEW, got "1#0000"`},
		{name: "insert after", edit: "after", values: []string{"2#00aa", "a\nb"}, want: []edit.Op{{Insert: &edit.Anchor{Line: 2, Hash: "00aa", After: true}, New: "a\nb"}}},
		{name: "insert before", edit: "before", values: []string{"1#00bb", "--c"}, want: []edit.Op{{Insert: &edit.Anchor{Line: 1, Hash: "00bb"}, New: "--c"}}},
		{name: "insert without new", edit: "after", values: []string{"1#0000"}, err: `want LINE#HASH and NEW, got "1#0000"`},
		{name: "insert at a range", edit: "before", values: []string{"1#0000-2#0000", "x"}, err: `"1#0000-2#0000" is a range, want one LINE#HASH`},
		{name: "insert at a reversed range", edit: "after", values: []string{"2#0000-1#0000", "x"}, err: `"2#0000-1#0000" is a range, want one LINE#HASH`},
		{name: "insert at a bad address", edit: "after", values: []string{"1", "x"}, err: `"1" is not LINE#HASH as catnip show prints it`},
		{name: "insert at a negative line", edit: "after", values: []string{"-1#0000", "x"}, err: `"-1#0000" is not LINE#HASH as catnip show prints it`},
		{name: "insert at line zero", edit: "after", values: []string{"0#0000", "x"}, err: `"0#0000": lines start at 1`},
		{name: "empty insert", edit: "before", values: []string{"1#0000", ""}, err: `"1#0000" "": empty NEW inserts nothing`},
		{name: "unknown edit", edit: "middle", values: []string{"a", "b"}, err: `unknown edit "middle"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := edit.Add(tt.ops, tt.edit, tt.values)

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
