package nav_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/edit"
	"github.com/hakastein/catnip/internal/nav"
)

// shop is a Go module of the tests by file.
var shop = map[string]string{
	"go.mod": "module example.com/shop\n\ngo 1.26\n",
	"cart/cart.go": `package cart

import "strings"

// Cart is the items of one customer.
type Cart struct {
	Items []string
}

// Add puts an item into the cart
// and trims it.
func (c *Cart) Add(it string) {
	c.Items = append(c.Items, strings.TrimSpace(it))
}

// Total sums the prices.
func Total(
	prices ...int,
) int {
	return 0
}
`,
	"go.work":    "go 1.26\n\nuse (\n\t.\n\t./pay\n)\n",
	"pay/go.mod": "module example.com/pay\n\ngo 1.26\n",
	"pay/pay.go": `package pay

import "example.com/shop/cart"

// Pay charges for c.
func Pay(c *cart.Cart) {}
`,
	"main.go": `package main

import "example.com/shop/cart"

func main() {
	c := &cart.Cart{}
	c.Add("a")
	c.Add("b"); c.Add("c")
}
`,
	"notes.txt": "Add\n",
}

// newShop writes shop into a fresh directory, makes it the current one and gives the rest of the test a fresh user
// cache, so the tests never touch the user's cache.
func newShop(t *testing.T) string {
	t.Helper()
	cache := t.TempDir()
	for _, name := range []string{"HOME", "XDG_CACHE_HOME", "LOCALAPPDATA"} {
		t.Setenv(name, cache)
	}
	dir := t.TempDir()
	for name, text := range shop {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
	}
	t.Chdir(dir)
	return dir
}

// at is the line of an answer: the address of line n of path and its text.
func at(path string, n int, text string) string {
	return fmt.Sprintf("%s:%d#%s: %s\n", filepath.FromSlash(path), n, edit.Hash(text), text)
}

var (
	addDecl = at("cart/cart.go", 10, "// Add puts an item into the cart") +
		at("cart/cart.go", 11, "// and trims it.") +
		at("cart/cart.go", 12, "func (c *Cart) Add(it string) {")
	addBody = addDecl +
		at("cart/cart.go", 13, "\tc.Items = append(c.Items, strings.TrimSpace(it))") +
		at("cart/cart.go", 14, "}")
	cartDecl = at("cart/cart.go", 5, "// Cart is the items of one customer.") +
		at("cart/cart.go", 6, "type Cart struct {")
	line7 = "7#" + edit.Hash("\tc.Add(\"a\")") + "\t\tc.Add(\"a\")"
	line8 = "8#" + edit.Hash("\tc.Add(\"b\"); c.Add(\"c\")") + "\t\tc.Add(\"b\"); c.Add(\"c\")"
)

func TestDef(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		body     bool
		out      string
		errs     string
		answered bool
	}{
		{name: "position", args: []string{"main.go:7", "c.Add"}, out: "main.go:7 c.Add\n" + addDecl, answered: true},
		{name: "address", args: []string{"main.go:7#" + edit.Hash("\tc.Add(\"a\")"), "Add"},
			out: "main.go:7#" + edit.Hash("\tc.Add(\"a\")") + " Add\n" + addDecl, answered: true},
		{name: "column picks one of several", args: []string{"main.go:8:17", "Add"}, out: "main.go:8:17 Add\n" + addDecl, answered: true},
		{name: "column on the last byte", args: []string{"main.go:8:18", "Add"}, out: "main.go:8:18 Add\n" + addDecl, answered: true},
		{name: "column before the expression", args: []string{"main.go:8:15", "Add"}, out: "main.go:8:15 Add\n",
			errs: "main.go:8:15 Add: \"Add\" is not at line 8 column 15: " + line8 + "\n"},
		{name: "type", args: []string{"main.go:6", "&cart.Cart"}, out: "main.go:6 &cart.Cart\n" + cartDecl, answered: true},
		{name: "on the declaration", args: []string{"cart/cart.go:12", "Add"}, out: "cart/cart.go:12 Add\n" + addDecl, answered: true},
		{name: "qualified name", args: []string{"cart.Cart.Add"}, out: "cart.Cart.Add\n" + addDecl, answered: true},
		{name: "multi-line signature", args: []string{"Total"}, out: "Total\n" + at("cart/cart.go", 16, "// Total sums the prices.") +
			at("cart/cart.go", 17, "func Total(") + at("cart/cart.go", 18, "\tprices ...int,") + at("cart/cart.go", 19, ") int {"), answered: true},
		{name: "go.work above the nearest go.mod", args: []string{"pay/pay.go:6", "cart.Cart"}, out: "pay/pay.go:6 cart.Cart\n" + cartDecl, answered: true},
		{name: "body", args: []string{"Add"}, body: true, out: "Add\n" + addBody, answered: true},
		{name: "several questions", args: []string{"Cart", "Add"}, out: "Cart\n" + cartDecl + "\nAdd\n" + addDecl, answered: true},
		{name: "changed line", args: []string{"main.go:7#0000", "Add"}, out: "main.go:7#0000 Add\n",
			errs: "main.go:7#0000 Add: line 7 changed: " + line7 + "\n"},
		{name: "line past the end", args: []string{"main.go:20", "Add"}, out: "main.go:20 Add\n",
			errs: "main.go:20 Add: line 20 is past the end: the file has 9 lines\n"},
		{name: "expression not on the line", args: []string{"main.go:7", "Remove"}, out: "main.go:7 Remove\n",
			errs: "main.go:7 Remove: \"Remove\" is not on line 7: " + line7 + "\n"},
		{name: "part of a longer word", args: []string{"main.go:7", "Ad"}, out: "main.go:7 Ad\n",
			errs: "main.go:7 Ad: \"Ad\" is not on line 7: " + line7 + "\n"},
		{name: "column off the expression", args: []string{"main.go:8:2", "Add"}, out: "main.go:8:2 Add\n",
			errs: "main.go:8:2 Add: \"Add\" is not at line 8 column 2: " + line8 + "\n"},
		{name: "expression twice; the other questions answered", args: []string{"main.go:8", "Add", "main.go:7", "c.Add"},
			out:  "main.go:8 Add\n\nmain.go:7 c.Add\n" + addDecl,
			errs: "main.go:8 Add: \"Add\" stands on line 8 2 times, at columns 4, 16; give the column as main.go:8:<column>: " + line8 + "\n"},
		{name: "no declaration", args: []string{"Remove"}, out: "Remove\n", errs: "Remove: no declaration found\n"},
		{name: "not Go", args: []string{"notes.txt:1", "Add"}, out: "notes.txt:1 Add\n",
			errs: "notes.txt:1 Add: no navigation in \".txt\" files: def knows Go (.go)\n"},
	}
	newShop(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := nav.Parse(tt.args)
			require.NoError(t, err)
			cwd, err := os.Getwd()
			require.NoError(t, err)
			n := nav.New(cwd)
			var out, errs bytes.Buffer

			answered := n.Def(qs, tt.body, &out, &errs)

			require.NoError(t, n.Close())
			assert.Equal(t, tt.out, out.String())
			assert.Equal(t, tt.errs, errs.String())
			assert.Equal(t, tt.answered, answered)
		})
	}
}

func TestDefStandardLibrary(t *testing.T) {
	newShop(t)
	qs, err := nav.Parse([]string{"cart/cart.go:13", "strings.TrimSpace"})
	require.NoError(t, err)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	n := nav.New(cwd)
	var out, errs bytes.Buffer

	answered := n.Def(qs, false, &out, &errs)

	require.NoError(t, n.Close())
	assert.True(t, answered, errs.String())
	assert.Regexp(t, `^cart/cart.go:13 strings.TrimSpace\n.+[/\\]src[/\\]strings[/\\]strings\.go:\d+#[0-9a-f]{4}: // TrimSpace `, out.String())
}

// errText is the text of err, "" for none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestParse(t *testing.T) {
	tests := []struct {
		args []string
		want []nav.Question
		err  string
	}{
		{args: []string{"a.go:3", "s.store.Write"}, want: []nav.Question{{Text: "a.go:3 s.store.Write", Path: "a.go", Line: 3, Expr: "s.store.Write"}}},
		{args: []string{"a.go:3:9", "Write"}, want: []nav.Question{{Text: "a.go:3:9 Write", Path: "a.go", Line: 3, Column: 9, Expr: "Write"}}},
		{args: []string{"a.go:3#0b1c", "Write", "pkg.Func"}, want: []nav.Question{
			{Text: "a.go:3#0b1c Write", Path: "a.go", Line: 3, Hash: "0b1c", Expr: "Write"},
			{Text: "pkg.Func", Name: "pkg.Func"},
		}},
		{args: []string{"Cart::add"}, want: []nav.Question{{Text: "Cart::add", Name: "Cart::add"}}},
		{args: []string{}, err: "def needs a position and an expression, or a name"},
		{args: []string{"a.go:3"}, err: "a.go:3: want an expression from the line after the position"},
		{args: []string{"a.go:3", "()"}, err: `a.go:3 "()": the expression has no name`},
		{args: []string{"a.go:3#zz", "Write"}, err: `a.go:3#zz: "3#zz" is not LINE#HASH as catnip show prints it`},
		{args: []string{"a.go:3#0b1c-4#0b1c", "Write"}, err: `a.go:3#0b1c-4#0b1c: "3#0b1c-4#0b1c" is a range, want one LINE#HASH`},
		{args: []string{"a.go:3:0", "Write"}, err: "a.go:3:0: columns start at 1"},
		{args: []string{""}, err: `"": the name is empty`},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.args), func(t *testing.T) {
			got, err := nav.Parse(tt.args)
			assert.Equal(t, tt.err, errText(err))
			assert.Equal(t, tt.want, got)
		})
	}
}
