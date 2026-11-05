package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hakastein/catnip/internal/edit"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []edit.Op
		err  string
	}{
		{
			name: "replaces with and without count, lines; values kept as they are",
			args: []string{
				"--replace", "--old \"$x\" \\", "new\nline", "--count", "2",
				"--replace", "a", "",
				"--lines", "3#00ff-5#abcd", "x",
				"--lines", "7#1234", "",
				"--after", "2#00aa", "a\nb",
				"--before", "1#00bb", "--c",
			},
			want: []edit.Op{
				{Old: "--old \"$x\" \\", New: "new\nline", Count: 2},
				{Old: "a", Count: 1},
				{Lines: &edit.LineRange{From: 3, To: 5, FromHash: "00ff", ToHash: "abcd"}, New: "x"},
				{Lines: &edit.LineRange{From: 7, To: 7, FromHash: "1234", ToHash: "1234"}},
				{Insert: &edit.Anchor{Line: 2, Hash: "00aa", After: true}, New: "a\nb"},
				{Insert: &edit.Anchor{Line: 1, Hash: "00bb"}, New: "--c"},
			},
		},
		{name: "no edits", args: nil, err: "no edits: want --replace OLD NEW, --lines FROM#HASH[-TO#HASH] NEW, --after LINE#HASH NEW or --before LINE#HASH NEW"},
		{name: "replace without new", args: []string{"--replace", "a"}, err: "--replace needs OLD and NEW"},
		{name: "empty text to find", args: []string{"--replace", "", "b"}, err: "--replace: empty text to find"},
		{name: "count without number", args: []string{"--replace", "a", "b", "--count"}, err: "--count needs N"},
		{name: "bad count", args: []string{"--replace", "a", "b", "--count", "0"}, err: `--count: bad number "0"`},
		{name: "count not after replace", args: []string{"--lines", "1#0000", "x", "--count", "2"}, err: "--count goes right after a --replace"},
		{name: "bad line range", args: []string{"--lines", "1-2", "x"}, err: `--lines: "1-2" is not FROM#HASH[-TO#HASH] as catnip show prints it`},
		{name: "text before the line number", args: []string{"--lines", "x1#0000", "x"}, err: `--lines: "x1#0000" is not FROM#HASH[-TO#HASH] as catnip show prints it`},
		{name: "hash too long", args: []string{"--lines", "1#00000", "x"}, err: `--lines: "1#00000" is not FROM#HASH[-TO#HASH] as catnip show prints it`},
		{name: "line zero", args: []string{"--lines", "0#0000", "x"}, err: `--lines: "0#0000": lines start at 1`},
		{name: "reversed range", args: []string{"--lines", "3#0000-1#0000", "x"}, err: `--lines: "3#0000-1#0000": the range ends before it starts`},
		{name: "line number too big", args: []string{"--lines", "99999999999999999999#0000", "x"}, err: `--lines: strconv.Atoi: parsing "99999999999999999999": value out of range`},
		{name: "lines without new", args: []string{"--lines", "1#0000"}, err: "--lines needs FROM#HASH[-TO#HASH] and NEW"},
		{name: "insert without new", args: []string{"--after", "1#0000"}, err: "--after needs LINE#HASH and NEW"},
		{name: "insert at a range", args: []string{"--before", "1#0000-2#0000", "x"}, err: `--before: "1#0000-2#0000" is a range, want one LINE#HASH`},
		{name: "insert at a reversed range", args: []string{"--after", "2#0000-1#0000", "x"}, err: `--after: "2#0000-1#0000" is a range, want one LINE#HASH`},
		{name: "insert at a bad address", args: []string{"--after", "1", "x"}, err: `--after: "1" is not LINE#HASH as catnip show prints it`},
		{name: "insert at a negative line", args: []string{"--after", "-1#0000", "x"}, err: `--after: "-1#0000" is not LINE#HASH as catnip show prints it`},
		{name: "insert at line zero", args: []string{"--after", "0#0000", "x"}, err: `--after: "0#0000": lines start at 1`},
		{name: "empty insert", args: []string{"--before", "1#0000", ""}, err: "--before: empty NEW inserts nothing"},
		{name: "unknown argument", args: []string{"--middle", "a", "b"}, err: `unexpected "--middle": want --replace, --lines, --after or --before`},
		{name: "help in place of an edit", args: []string{"--replace", "--help", "b", "--help"}, err: "flag: help requested"},
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
