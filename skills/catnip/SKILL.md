---
name: catnip
description: "Use for every file read or edit, by file tools or shell: cat, sed, head/tail, heredoc, python; and to find where a Go symbol is declared."
---

# catnip

`catnip` reads and edits files in one bash command and chains with `&&`. Every file read and every file edit goes
through it: in place of the agent's built-in tools for reading and editing files, `cat`, `sed -n`, `head`/`tail`,
`sed -i`, heredoc into a file and python scripts with `str.replace`. Help for a command: `catnip <command> --help`.

## View

```bash
catnip show src/a.php                     # the whole file; the line address 57#a3f0 is printed without flags
catnip show src/a.php:40-80               # a range
catnip show src/a.php:57                  # a position and 5 lines around it; -C N changes the context
catnip show src/a.php:57:12               # a position with a column, as LSP-based tools print it
catnip show --gutter line src/a.php:40-80 # line numbers only; --gutter none prints only the text
catnip show --blame src/a.php:57          # commit, author and date of every line
```

A line address `57#a3f0` is the line number and a hash of its content; edits address lines by it. Lines and columns
start at 1. A range past the end of the file is an error that names the real line count.

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

## Find a declaration (Go)

Instead of grepping for `func Name`, ask where a symbol is declared: by a position and an expression from its line,
or by a name. The answer is resolved by types.

```bash
catnip def internal/cart/cart.go:57 s.store.Write   # the symbol at the last word of the expression
catnip def internal/cart/cart.go:57#a3f0 Write      # a line address from show; rejected if the line changed
catnip def internal/cart/cart.go:57:31 Write        # the column picks one of several equal expressions
catnip def cart.Cart.Add Store                      # names: Func, Type.Method, pkg.Func; a bare name matches any
catnip def --body Store.Write                       # the whole declaration
```

Each question gets a block: the question, then the doc comment and signature of each declaration, every line as
`path:N#hash: text`, ready for `show` and `edit`. A declaration of the standard library or a dependency has an
absolute path and is marked `(built-in)` or `(vendor)`. Questions mix in one call; one without an answer keeps its
line, the reason and the current line go to stderr, the others are answered, and the exit code is 1.
## Exit codes

| Code | Meaning |
|---|---|
| 0 | written, checks passed |
| 1 | nothing written; `def`: a question has no answer |
| 2 | bad arguments or config |
| 3 | written, but a check failed or the file was written in part |
