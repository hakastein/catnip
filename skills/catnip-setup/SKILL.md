---
name: catnip-setup
description: "Use when setting up catnip for a project: its .catnip.json, fixers and checks."
---

# Setting up catnip in a project

The result is `.catnip.json` at the project root: for every language people edit by hand in the project, the fixers
and checks the project already uses, each command verified on a real file. The config format is the `Конфиг` section
of the [README](https://github.com/hakastein/catnip#конфиг).

## 1. Project tools

Languages come from `git ls-files`, excluding generated and vendored code. Tools come from what the project itself
runs: CI, `Makefile` and other task runners, `package.json` and `composer.json` scripts, `pyproject.toml`,
pre-commit, tool configs (`.golangci.yml`, `.eslintrc*`, `.prettierrc*`, `.php-cs-fixer*`, `phpstan.neon`,
`ruff.toml` and the like). Call each tool the way the project does: its binary (`vendor/bin`, `node_modules/.bin`),
its config and version.

A language the project has no tools for gets only the built-in syntax check that writes no files (`php -l`), if
there is one; anything else is a suggestion to the user in the report.

Read the global config `catnip/config.json` in the user config directory, if it exists: its rules run on project
files too.

Done when every language has a list of tools with their source (`path:line`) or the note "the project has none".

## 2. One command per file

catnip runs the commands after every file write: through `sh -c` at the project root, `{file}` is the quoted path
from the root. All fixers first, then all checks; within each, by glob, and within a glob, by name, alphabetically.

- A fixer changes the file (`gofmt -w {file}`, `prettier --write {file}`); a check does not. A tool that fixes and
  fails on what it cannot fix (`eslint --fix`, `ruff check --fix`) is a single fixer: its failure prints as
  `<name>: failed`. When fixer order matters, names set it within a glob, and the globs themselves across globs.
- A tool that works on a package rather than a file runs on the file's package: `cd "$(dirname {file})" && go vet .`.
- A command that needs more than 5 seconds per file (measure with `time`) stays out of the config: it would run after
  every edit. It goes into the report as a command to run by hand.
- The command name is the tool name (`gofmt`, `phpstan`): the agent sees it in the output
  `<name>: passed|fixed|failed`.
- A glob without `/` matches the file name at any depth (`*.go`); a glob with `/` matches the path from the root,
  and `*` does not cross `/`: `src/*.go` does not take `src/x/a.go`. There is no `{a,b}` and no `**`: one glob per
  extension. A glob cannot exclude a directory, since every matching glob applies; exclusions belong in the tool's
  own config, where CI sees them too.
- A subproject with its own tools (a directory with its own `package.json` or `composer.json`) gets its own
  `.catnip.json` at its root: catnip takes the config nearest to the file as a whole, `{file}` and the commands
  work from its directory.
- A project rule with the same glob as a global one replaces the global rule; with a different glob it runs
  alongside it. The rule `{}` disables the global rule with the same glob.

## 3. Write and verify

Save `git status --short` before you start. Write the config with `catnip write .catnip.json`; extend an existing
one with `catnip edit`, keeping its rules. Exit code 2 from the next catnip command means the config did not parse;
the error names the file and the spot. `catnip show` reads a broken project `.catnip.json`: carry its rules into the
fixed text, delete the file and write that text again. Report an error in any other file to the user.

Then, for every glob:

1. A project file under this glob with no local changes (`git status --porcelain -- <file>` is empty):
   `catnip write <file> < <file>` keeps the content and runs the rules. Expect `<name>: passed` for every command
   and exit code 0. Empty output means the glob did not take the file.
   - `fixed`: the fixer formats differently from the project. Find an invocation that leaves the file unchanged or
     drop the fixer; restore the file with `git checkout -- <file>`.
   - `failed` with `not found`: the tool is called differently from how the project installs it.
   - `failed` on a real violation in the file: take another file, and restore this one with
     `git checkout -- <file>` if a fixer changed it; project code is not edited for the sake of the config.
2. A probe file next to real ones, with a syntax error (a fixer silently fixes a style error); its path is a new
   file the glob takes: `catnip_probe.go` for `*.go`, `catnip_probe_test.go` for `*_test.go`:

   ```bash
   catnip write <path under the glob> <<'EOF'
   <code with a syntax error>
   EOF
   ```

   Expect `<name>: failed` for every check and exit code 3. Delete the probe file.

Done when every command printed `passed` on a real file, every check printed `failed` on the probe, and
`git status --short` differs from the saved one only by `.catnip.json`.

## 4. Report

To the user: a table "glob → name → command → source", the global config's rules, what was left out and why,
suggestions for languages without tools. Whether to commit `.catnip.json` is the user's call; an uncommitted config
in the main working tree also applies in its git worktrees.
