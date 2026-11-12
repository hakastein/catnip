---
name: catnip
description: "Use for every file read or edit and code symbol lookup."
---

# catnip

`catnip` reads and edits files in one bash command and chains with `&&`. Every file read and every file edit goes
through it: in place of the agent's built-in tools for reading and editing files, `cat`, `sed -n`, `head`/`tail`,
`sed -i`, heredoc into a file and python scripts with `str.replace`; and in place of grep wherever the question is
about a symbol of the code: where it is declared, who uses it, what implements it. Help for a command:
`catnip <command> --help`.

## View

```bash
catnip show src/a.php                     # the whole file; the line address 57#a3f0 is printed without flags
catnip show src/a.php:40-80               # a range
catnip show src/a.php:57                  # a position and 5 lines around it; -C N changes the context
catnip show src/a.php:57:12               # a position with a column, as LSP-based tools print it
catnip show --gutter line src/a.php:40-80 # line numbers only; --gutter none prints only the text
catnip show --blame src/a.php:57          # commit, author and date of every line
catnip show src/a.php:40-80 src/b.php     # several targets, a block each after its target line
catnip show --rev main src/a.php:40-80    # the file as it was in a git revision
catnip show cart/cart.go::Cart.Add        # a Go declaration by name, from its doc comment to its end
catnip show cart/cart.go --outline        # the line of each declaration of a Go file
```

A line address `57#a3f0` is the line number and a hash of its content; edits address lines by it. Lines and columns
start at 1. A range past the end of the file prints up to its last line and warns on stderr. In a Go file, read a
function by its name with `::` rather than by guessing its lines, and look at `--outline` before reading the whole file.

## Navigate code (Go)

`def` and `ref` answer by types, not by text, and take a symbol by its name alone: no path, several names in one
call, one block per name.

| Instead of | Ask |
|---|---|
| `grep -n 'func Name' -A30`, reading a file to find a function | `catnip def --body Name` (`Type.Method`, `pkg.Func`) |
| `grep -rn 'Name'` to find who calls or uses it | `catnip ref Name` |
| `grep -rn 'A\|B\|C'` over several names | `catnip ref A B C` |
| grep for the types that implement an interface | `catnip ref Iface.Method`: uses, then `implementations:` |

grep stays for text that is not a symbol (strings, SQL, config keys, comments), for code that is not Go and for code
at another git revision.

```bash
catnip def --body Store.Write Cart.Add              # the whole declarations; without --body the doc and signature
catnip def internal/cart/cart.go:57 s.store.Write   # the symbol at the last word of an expression on line 57
catnip def internal/cart/cart.go:57#a3f0 Write      # a line address from show; rejected if the line changed
catnip ref Store.Write Cart.Add                     # uses of each, files with most lines first
catnip ref --limit 200 Store.Write                  # more than the default 60 lines
```

A bare name matches the last segment of qualified names: `def` prints every declaration it matches, `ref` refuses a
name of several declarations and lists their addresses to ask by. Answer lines are
`path:N#hash: text`: pass `path:N#hash` to `show` as is, `edit` takes the path and `N#hash` apart. When `ref` ends with
`<n> more lines in <m> files`, raise `--limit`.

## Edit

One file, any number of edits, all or nothing:

```bash
catnip edit src/a.php \
  --replace '$total = $price * $qty;' '$total = $price * $qty * (1 - $discount);' \
  --replace 'oldName(' 'newName(' --count 2 \
  --lines 57#a3f0-59#0b1c '        return null;' \
  --after 12#9c41 'use App\Discount;' \
  && make test
```

- `--replace OLD NEW` replaces exact text: part of a line or several lines. The text must be found exactly once, or,
  with `--count N` right after, exactly N times (all are replaced).
- `--lines FROM#HASH[-TO#HASH] NEW` replaces whole lines by the addresses from `show`. If an edge line of the range
  has changed, the edit is rejected; catnip does not check the middle of the range. When it matters that the whole
  block is unchanged, use `--replace` with its full text.
- `--after LINE#HASH NEW`, `--before LINE#HASH NEW` insert whole lines after or before the line; if it has changed,
  the edit is rejected.
- Every edit addresses the original content of the file: line numbers from `show` hold for the whole command;
  overlapping edits are an error.
- If any edit does not match, the file is not written, and the error names the reason: the matching lines, the
  first line where multi-line text diverged from the file, or similar lines.
- Text is plain shell arguments: inside single quotes `$`, `\` and newlines pass as is, and `'` itself is written
  as `'\''`.

After writing, catnip prints the changed lines (`path:line`, `-`/`+`) and runs the fixers and checks from the
config, each under its name: `<name>: passed`; `<name>: fixed` if a fixer changed the file (`--show-fix-diff`
before the path shows what it changed); `<name>: failed` and the command's output. A change of BOM or line endings
is reported on a line of its own.

## New file

```bash
catnip write src/new.go <<'EOF'
package x
EOF
```

Rewriting an existing file keeps its BOM, line endings and whether it ends with a newline.

## Rename (Go)

`rename` takes the symbol as `def` does, by a position.

```bash
catnip rename internal/cart/cart.go:57 s.store.Write Put && go test ./...
```

Files of every platform (`*_windows.go`) are renamed too, and with an interface method the methods of the types the
code converts to the interface. A refusal (the name conflicts, the symbol is not of the project) writes nothing;
the changed lines and the fixers and checks print per file as after `edit`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | written, checks passed |
| 1 | nothing written, `rename` refused; `def`, `ref`: a question has no answer; `show`: a target not printed, the others print |
| 2 | bad arguments or config |
| 3 | written, but a check failed or the file was written in part; `rename`: stderr ends with the files written |
