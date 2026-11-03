package edit

import (
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Diff shows the changed lines between two raw contents of path, without context. Each hunk opens with
// "path:<line>", the line in the new content where it lands.
func Diff(path string, before, after []byte) string {
	a, _ := Decode(before)
	b, _ := Decode(after)
	al, bl := Lines(a), Lines(b)
	var out strings.Builder
	for _, group := range difflib.NewMatcher(al, bl).GetGroupedOpCodes(0) {
		fmt.Fprintf(&out, "%s:%d\n", path, group[0].J1+1)
		for _, c := range group {
			if c.Tag == 'r' || c.Tag == 'd' {
				for _, l := range al[c.I1:c.I2] {
					out.WriteString("-\t" + l + "\n")
				}
			}
			if c.Tag == 'r' || c.Tag == 'i' {
				for _, l := range bl[c.J1:c.J2] {
					out.WriteString("+\t" + l + "\n")
				}
			}
		}
	}
	if out.Len() == 0 && string(before) != string(after) {
		fmt.Fprintf(&out, "%s: only line terminators or BOM changed\n", path)
	}
	return out.String()
}
