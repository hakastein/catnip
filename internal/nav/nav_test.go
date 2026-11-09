package nav_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/config"
	"github.com/hakastein/catnip/internal/edit"
	"github.com/hakastein/catnip/internal/nav"
	"github.com/hakastein/catnip/internal/view"
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
	"cart/store.go": `package cart

// Store keeps carts.
type Store interface {
	Save(c *Cart) error
}

type mem struct{}

func (mem) Save(c *Cart) error { return nil }

type disk struct{}

func (disk) Save(c *Cart) error { return nil }

func keep(s Store, c *Cart) error {
	c.Add("x"); c.Add("y"); c.Add("z"); c.Add("w")
	return s.Save(c)
}
`,
	"go.work":       "go 1.26\n\nuse (\n\t.\n\t./pay\n\t../ext\n)\n",
	"../ext/go.mod": "module example.com/ext\n\ngo 1.26\n",
	"../ext/ext.go": "package ext\n\nfunc Hello() {}\n\nfunc Again() { Hello() }\n",
	"pay/go.mod":    "module example.com/pay\n\ngo 1.26\n",
	"pay/pay.go": `package pay

import "example.com/shop/cart"

// Pay charges for c.
func Pay(c *cart.Cart) {}

func init() {}

func init() {}

var a, b int
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
	// an interface method with an implementation and a use in a file of one platform, with a BOM and CRLF
	"cart/clock.go":         clockGo,
	"cart/clock_windows.go": clockWindows,
	// a module with a vendor directory beside the root
	"../vend/go.mod":                        "module example.com/vend\n\ngo 1.26\n\nrequire example.com/dep v1.0.0\n",
	"../vend/vendor/modules.txt":            "# example.com/dep v1.0.0\n## explicit; go 1.26\nexample.com/dep\n",
	"../vend/vendor/example.com/dep/dep.go": "package dep\n\n// Helper helps.\nfunc Helper() int { return 1 }\n",
	"../vend/main.go":                       "package main\n\nimport \"example.com/dep\"\n\nfunc main() { dep.Helper() }\n",
	// a module that a replace takes from a directory beside it
	"../rep/go.mod":  "module example.com/rep\n\ngo 1.26\n\nrequire example.com/far v1.0.0\n\nreplace example.com/far => ../far\n",
	"../rep/main.go": "package main\n\nimport \"example.com/far\"\n\nfunc main() { far.Away() }\n",
	"../far/go.mod":  "module example.com/far\n\ngo 1.26\n",
	"../far/far.go":  "package far\n\nfunc Away() {}\n",
}

// newShop writes shop into a fresh directory, makes it the current one and gives the rest of the test a fresh user
// cache and config, so the tests never touch the user's.
func newShop(t *testing.T) string {
	t.Helper()
	cache := t.TempDir()
	for _, name := range []string{"HOME", "XDG_CACHE_HOME", "LOCALAPPDATA", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(name, cache)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir()) // the go command refuses a Windows short name such as RUNNER~1
	require.NoError(t, err)
	for name, text := range shop {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
	}
	t.Chdir(dir)
	return dir
}

// native is the expected stderr s with <dir> and <up> made the directory of the project and the one that holds it;
// the paths under them, written with slashes, take the separator of the OS, as the provider prints them.
func native(s, dir string) string {
	return placeholder.ReplaceAllStringFunc(s, func(p string) string {
		base, rest, _ := strings.Cut(p, ">")
		if base == "<up" {
			return filepath.Dir(dir) + filepath.FromSlash(rest)
		}
		return dir + filepath.FromSlash(rest)
	})
}

var placeholder = regexp.MustCompile(`<(dir|up)>[^\s:,]*`)

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
	dir := newShop(t)
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
		{name: "vendor directory of another root", args: []string{"../vend/main.go:5", "dep.Helper"}, out: "../vend/main.go:5 dep.Helper\n" +
			at("../vend/vendor/example.com/dep/dep.go", 3, "// Helper helps.") +
			at("../vend/vendor/example.com/dep/dep.go", 4, "func Helper() int { return 1 }"), answered: true},
		{name: "replaced module of another root", args: []string{"../rep/main.go:5", "far.Away"}, out: "../rep/main.go:5 far.Away\n" +
			at(filepath.Join(filepath.Dir(dir), "far", "far.go"), 3, "func Away() {}"), answered: true},
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
			errs: "notes.txt:1 Add: no navigation in \".txt\" files: catnip knows Go (.go)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := nav.Parse("def", tt.args)
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

func TestShow(t *testing.T) {
	tests := []struct {
		name    string
		target  view.Target
		outline bool
		want    []view.Target
		err     string
	}{
		{name: "type and method", target: view.Target{Path: "cart/cart.go", Symbol: "Cart.Add"}, want: []view.Target{{Path: "cart/cart.go", From: 10, To: 14}}},
		{name: "bare name", target: view.Target{Path: "cart/cart.go", Symbol: "Add"}, want: []view.Target{{Path: "cart/cart.go", From: 10, To: 14}}},
		{name: "several declarations", target: view.Target{Path: "pay/pay.go", Symbol: "init"},
			want: []view.Target{{Path: "pay/pay.go", From: 8, To: 8}, {Path: "pay/pay.go", From: 10, To: 10}}},
		{name: "outline; names of one line once", target: view.Target{Path: "pay/pay.go"}, outline: true, want: []view.Target{
			{Path: "pay/pay.go", From: 6, To: 6}, {Path: "pay/pay.go", From: 8, To: 8}, {Path: "pay/pay.go", From: 10, To: 10}, {Path: "pay/pay.go", From: 12, To: 12}}},
		{name: "no symbol", target: view.Target{Path: "cart/cart.go", Symbol: "Remove"},
			err: `no symbol "Remove" in cart/cart.go; catnip show cart/cart.go --outline lists its declarations`},
		{name: "not Go", target: view.Target{Path: "notes.txt"}, outline: true, err: `no navigation in ".txt" files: catnip knows Go (.go)`},
	}
	newShop(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd, err := os.Getwd()
			require.NoError(t, err)
			n := nav.New(cwd)

			got, err := n.Show(tt.target, tt.outline)

			require.NoError(t, n.Close())
			assert.Equal(t, tt.err, errText(err))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDefStandardLibrary(t *testing.T) {
	newShop(t)
	qs, err := nav.Parse("def", []string{"cart/cart.go:13", "strings.TrimSpace"})
	require.NoError(t, err)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	n := nav.New(cwd)
	var out, errs bytes.Buffer

	answered := n.Def(qs, false, &out, &errs)

	require.NoError(t, n.Close())
	assert.True(t, answered, errs.String())
	assert.Regexp(t, `^cart/cart.go:13 strings.TrimSpace\n(/|[A-Z]:\\).+[/\\]src[/\\]strings[/\\]strings\.go:\d+#[0-9a-f]{4}: // TrimSpace `, out.String())
}

var (
	saveUses = at("cart/store.go", 18, "\treturn s.Save(c)")
	saveImpl = at("cart/store.go", 10, "func (mem) Save(c *Cart) error { return nil }")
	diskImpl = at("cart/store.go", 14, "func (disk) Save(c *Cart) error { return nil }")
	addUse7  = at("main.go", 7, "\tc.Add(\"a\")")
	addUse8  = at("main.go", 8, "\tc.Add(\"b\"); c.Add(\"c\")")                                    // two places, one line
	addUse17 = at("cart/store.go", 17, "\tc.Add(\"x\"); c.Add(\"y\"); c.Add(\"z\"); c.Add(\"w\")") // more places than main.go, fewer lines
)

func TestRef(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		limit    int
		out      string
		errs     string
		answered bool
	}{
		{name: "uses by file, most lines first", args: []string{"main.go:7", "c.Add"}, limit: 60,
			out: "main.go:7 c.Add\n" + addUse7 + addUse8 + addUse17, answered: true},
		{name: "limit gives a line to each file first", args: []string{"main.go:7", "c.Add"}, limit: 2,
			out: "main.go:7 c.Add\n" + addUse7 + addUse17 + "1 more lines in 1 files\n", answered: true},
		{name: "limit below the files", args: []string{"main.go:7", "c.Add"}, limit: 1,
			out: "main.go:7 c.Add\n" + addUse7 + "2 more lines in 2 files\n", answered: true},
		{name: "implementations", args: []string{"cart/store.go:5", "Save"}, limit: 60,
			out: "cart/store.go:5 Save\n" + saveUses + "implementations:\n" + saveImpl + diskImpl, answered: true},
		{name: "limit gives a line to each group first", args: []string{"Store.Save"}, limit: 2,
			out: "Store.Save\n" + saveUses + "implementations:\n" + saveImpl + "1 more lines in 1 files\n", answered: true},
		{name: "a name of several declarations; the other questions answered", args: []string{"Save", "Store.Save"}, limit: 60,
			out: "Save\n\nStore.Save\n" + saveUses + "implementations:\n" + saveImpl + diskImpl,
			errs: "Save: 3 declarations; ask about one of them by its address:\n" + at("cart/store.go", 5, "\tSave(c *Cart) error") +
				saveImpl + diskImpl},
		{name: "no uses", args: []string{"Total"}, limit: 60, out: "Total\n", errs: "Total: no uses found\n"},
		{name: "a module of go.work outside its directory is of the project", args: []string{"Hello"}, limit: 60,
			out: "Hello\n" + at("../ext/ext.go", 5, "func Again() { Hello() }"), answered: true},
	}
	newShop(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := nav.Parse("ref", tt.args)
			require.NoError(t, err)
			cwd, err := os.Getwd()
			require.NoError(t, err)
			n := nav.New(cwd)
			var out, errs bytes.Buffer

			answered := n.Ref(qs, tt.limit, &out, &errs)

			require.NoError(t, n.Close())
			assert.Equal(t, tt.out, out.String())
			assert.Equal(t, tt.errs, errs.String())
			assert.Equal(t, tt.answered, answered)
		})
	}
}

var (
	clockGo      = "package cart\n\n// Clock tells the time.\ntype Clock interface {\n\tNow() int\n}\n\ntype wall struct{}\n\nfunc (wall) Now() int { return 1 }\n\nvar _ Clock = wall{}\n"
	clockWindows = "\ufeffpackage cart\r\n\r\nfunc tick(c Clock) int { return c.Now() }\r\n"
	clockShop    = map[string]string{"cart/clock.go": clockGo, "cart/clock_windows.go": clockWindows}
	clockDiff    = filepath.FromSlash("cart/clock.go:5\n-\t\tNow() int\n+\t\tHour() int\ncart/clock.go:10\n") +
		"-\tfunc (wall) Now() int { return 1 }\n+\tfunc (wall) Hour() int { return 1 }\n"
	windowsDiff = filepath.FromSlash("cart/clock_windows.go:3\n") +
		"-\tfunc tick(c Clock) int { return c.Now() }\n+\tfunc tick(c Clock) int { return c.Hour() }\n"
)

func TestRename(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		newName string
		config  string
		out     string
		errs    string // <dir> stands for the directory of the project, <up> for the one that holds it
		code    int
		files   map[string]string // the content of files after
	}{
		{name: "an interface method, its implementation and a use in a file of one platform; each file checked",
			args: []string{"cart/clock.go:5", "Now"}, newName: "Hour", config: `{"*.go": {"check": {"has Hour": "grep -q Hour {file}"}}}`,
			out:  clockDiff + "has Hour: passed\n" + windowsDiff + "has Hour: passed\n",
			code: 0, files: map[string]string{"cart/clock.go": strings.ReplaceAll(clockGo, "Now", "Hour"),
				"cart/clock_windows.go": "\ufeffpackage cart\r\n\r\nfunc tick(c Clock) int { return c.Hour() }\r\n"}},
		{name: "a check failed in one file: written, the files listed",
			args: []string{"cart/clock.go:5", "Now"}, newName: "Hour", config: `{"*.go": {"check": {"has tick": "grep -q tick {file}"}}}`,
			out:  clockDiff + windowsDiff + "has tick: passed\n",
			errs: "has tick: failed\nexit status 1\nwritten: " + filepath.FromSlash("cart/clock.go, cart/clock_windows.go") + "\n",
			code: 3, files: map[string]string{"cart/clock.go": strings.ReplaceAll(clockGo, "Now", "Hour")}},
		{name: "a broken config: nothing written", args: []string{"cart/clock.go:5", "Now"}, newName: "Hour", config: `{`,
			errs: "<dir>/.catnip.json: unexpected EOF\nnothing written\n", code: 2, files: clockShop},
		{name: "the new name conflicts", args: []string{"cart/clock_windows.go:3", "tick"}, newName: "keep", config: `{}`,
			errs: "cart/clock_windows.go:3 tick: go: rename at <dir>/cart/clock_windows.go:3:6: <dir>/cart/clock_windows.go:3:6: " +
				"renaming this func \"tick\" to \"keep\"\n<dir>/cart/store.go:16:6:\tconflicts with func in same block\nnothing written\n",
			code: 1, files: clockShop},
		{name: "declared outside the project", args: []string{"../rep/main.go:5", "far.Away"}, newName: "Far", config: `{}`,
			errs: "../rep/main.go:5 far.Away: go: rename at <up>/rep/main.go:5:19: Away is declared outside the root (module) at " +
				"<up>/far/far.go:3:6\nnothing written\n", code: 1, files: map[string]string{"../far/far.go": shop["../far/far.go"]}},
		{name: "a package", args: []string{"cart/clock.go:1", "cart"}, newName: "shop", config: `{}`,
			errs: "cart/clock.go:1 cart: go: rename at <dir>/cart/clock.go:1:9: package cart: not supported\nnothing written\n",
			code: 1, files: clockShop},
		{name: "no symbol", args: []string{"cart/clock.go:3", "time"}, newName: "hour", config: `{}`,
			errs: "cart/clock.go:3 time: no symbol to rename\nnothing written\n", code: 1, files: clockShop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newShop(t)
			require.NoError(t, os.WriteFile(config.ProjectFile, []byte(tt.config), 0o644))
			qs, err := nav.Parse("rename", tt.args)
			require.NoError(t, err)
			n := nav.New(dir)
			var out, errs bytes.Buffer

			code := n.Rename(qs[0], tt.newName, false, &out, &errs)

			require.NoError(t, n.Close())
			assert.Equal(t, tt.code, code)
			assert.Equal(t, tt.out, out.String())
			assert.Equal(t, native(tt.errs, dir), errs.String())
			for path, want := range tt.files {
				got, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, want, string(got), path)
			}
		})
	}
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
		{args: []string{}, err: "ref needs a position and an expression, or a name"},
		{args: []string{"a.go:3"}, err: "a.go:3: want an expression from the line after the position"},
		{args: []string{"a.go:3", "()"}, err: `a.go:3 "()": the expression has no name`},
		{args: []string{"a.go:3#zz", "Write"}, err: `a.go:3#zz: "3#zz" is not LINE#HASH as catnip show prints it`},
		{args: []string{"a.go:3#0b1c-4#0b1c", "Write"}, err: `a.go:3#0b1c-4#0b1c: "3#0b1c-4#0b1c" is a range, want one LINE#HASH`},
		{args: []string{"a.go:3:0", "Write"}, err: "a.go:3:0: columns start at 1"},
		{args: []string{""}, err: `"": the name is empty`},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.args), func(t *testing.T) {
			got, err := nav.Parse("ref", tt.args)
			assert.Equal(t, tt.err, errText(err))
			assert.Equal(t, tt.want, got)
		})
	}
}
