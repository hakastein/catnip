package edit

import (
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Diff shows the changed lines between two raw contents of path, without context. Each hunk opens with
// "path:<line>", the line in the new content where it lands. A changed BOM or line terminator style is named
// apart, since it does not show in the lines.
func Diff(path string, before, after []byte) string {
	a, ea := Decode(before)
	b, eb := Decode(after)
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
	switch {
	case ea.BOM && !eb.BOM:
		fmt.Fprintf(&out, "%s: BOM removed\n", path)
	case !ea.BOM && eb.BOM:
		fmt.Fprintf(&out, "%s: BOM added\n", path)
	}
	switch {
	case ea.CRLF && !eb.CRLF:
		fmt.Fprintf(&out, "%s: line terminators CRLF → LF\n", path)
	case !ea.CRLF && eb.CRLF:
		fmt.Fprintf(&out, "%s: line terminators LF → CRLF\n", path)
	}
	if out.Len() == 0 && string(before) != string(after) {
		fmt.Fprintf(&out, "%s: only line terminators changed\n", path)
	}
	return out.String()
}
