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
			},
			want: []edit.Op{
				{Old: "--old \"$x\" \\", New: "new\nline", Count: 2},
				{Old: "a", Count: 1},
				{Lines: &edit.LineRange{From: 3, To: 5, FromHash: "00ff", ToHash: "abcd"}, New: "x"},
				{Lines: &edit.LineRange{From: 7, To: 7, FromHash: "1234", ToHash: "1234"}},
			},
		},
		{name: "no edits", args: nil, err: "no edits: want --replace OLD NEW or --lines FROM#HASH[-TO#HASH] NEW"},
		{name: "replace without new", args: []string{"--replace", "a"}, err: "--replace needs OLD and NEW"},
		{name: "empty text to find", args: []string{"--replace", "", "b"}, err: "--replace: empty text to find"},
		{name: "count without number", args: []string{"--replace", "a", "b", "--count"}, err: "--count needs N"},
		{name: "bad count", args: []string{"--replace", "a", "b", "--count", "0"}, err: `--count: bad number "0"`},
		{name: "count not after replace", args: []string{"--lines", "1#0000", "x", "--count", "2"}, err: "--count goes right after a --replace"},
		{name: "bad line range", args: []string{"--lines", "1-2", "x"}, err: `--lines: "1-2" is not FROM#HASH[-TO#HASH] as catnip show --hash prints it`},
		{name: "lines without new", args: []string{"--lines", "1#0000"}, err: "--lines needs FROM#HASH[-TO#HASH] and NEW"},
		{name: "unknown argument", args: []string{"--before", "a", "b"}, err: `unexpected "--before": want --replace or --lines`},
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
