// Package config maps file globs to the fixers and checks run after a file is written.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Rule is what runs on a file matching a glob, each command under the name catnip reports it by: Fix commands may
// change the file, Check commands must not. "{file}" in a command is replaced by the file path.
type Rule struct {
	Fix   map[string]string `json:"fix"`
	Check map[string]string `json:"check"`
}

// Command is a named fixer or check.
type Command struct {
	Name, Cmd string
}

// Config is the merged project and global configuration.
type Config struct {
	// Root is the project root as Project finds it, "" without a project config; globs with "/" match paths
	// relative to it and the commands run in it.
	Root  string
	rules map[string]Rule
}

// ProjectFile is the project config's name.
const ProjectFile = ".catnip.json"

// Load reads the global config (<user config dir>/catnip/config.json) and the project config of dir as Project finds
// it. A project rule replaces the global rule for the same glob. A missing file is no rules.
func Load(dir string) (*Config, error) {
	root, project := Project(dir)
	c := &Config{Root: root, rules: map[string]Rule{}}
	d, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("user config directory: %w", err)
	}
	paths := []string{filepath.Join(d, "catnip", "config.json")}
	if project != "" {
		paths = append(paths, project)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var rules map[string]Rule
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rules); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if dec.More() {
			return nil, fmt.Errorf("%s: text after the config object", p)
		}
		for glob, r := range rules {
			if _, err := path.Match(glob, ""); err != nil {
				return nil, fmt.Errorf("%s: glob %q: %w", p, glob, err)
			}
			for name, cmd := range r.Fix {
				if err := named(name, cmd); err != nil {
					return nil, fmt.Errorf("%s: glob %q: fix: %w", p, glob, err)
				}
			}
			for name, cmd := range r.Check {
				if err := named(name, cmd); err != nil {
					return nil, fmt.Errorf("%s: glob %q: check: %w", p, glob, err)
				}
			}
			c.rules[glob] = r
		}
	}
	return c, nil
}

func named(name, cmd string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("empty name")
	}
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("%q: empty or null command", name)
	}
	return nil
}

// For lists the fixers and checks for file, in glob order and by name within a glob, with "{file}" filled in:
// the path from Root, or file as given without a project config.
func (c *Config) For(file string) (fix, check []Command) {
	name := filepath.Base(file)
	arg, rel := file, ""
	if abs, err := filepath.Abs(file); err == nil && c.Root != "" {
		arg = abs
		if r, err := filepath.Rel(c.Root, abs); err == nil {
			if r = filepath.ToSlash(r); r != ".." && !strings.HasPrefix(r, "../") {
				arg, rel = r, r
			}
		}
	}
	globs := make([]string, 0, len(c.rules))
	for g := range c.rules {
		globs = append(globs, g)
	}
	sort.Strings(globs)
	quoted := "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	for _, g := range globs {
		subject := name
		if strings.Contains(g, "/") {
			subject = rel
		}
		if ok, _ := path.Match(g, subject); !ok {
			continue
		}
		fix = append(fix, commands(c.rules[g].Fix, quoted)...)
		check = append(check, commands(c.rules[g].Check, quoted)...)
	}
	return fix, check
}

func commands(named map[string]string, file string) []Command {
	var out []Command
	for _, name := range slices.Sorted(maps.Keys(named)) {
		out = append(out, Command{name, strings.ReplaceAll(named[name], "{file}", file)})
	}
	return out
}

// Project is the project config for files in dir: root is the directory its globs with "/", "{file}" and commands
// work from, file is the config read; both "" without one. The config is the nearest .catnip.json from dir up. In a
// linked git worktree with none up to its top, it is the main working tree's config for the same relative directory,
// and root is that directory in the worktree.
func Project(dir string) (root, file string) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", ""
	}
	root = Find(dir, ProjectFile)
	if r := FindRepo(dir); r.Root != r.Main && !within(r.Root, root) {
		rel, _ := filepath.Rel(r.Root, dir) // dir is inside r.Root
		found := Find(filepath.Join(r.Main, rel), ProjectFile)
		if found == "" {
			return "", ""
		}
		file, root = filepath.Join(found, ProjectFile), found
		if in, err := filepath.Rel(r.Main, found); err == nil && filepath.IsLocal(in) {
			root = filepath.Join(r.Root, in)
		}
		return root, file
	}
	if root == "" {
		return "", ""
	}
	return root, filepath.Join(root, ProjectFile)
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return path != "" && err == nil && filepath.IsLocal(rel)
}

// Repo is the git working tree a directory belongs to, found by reading .git without running git.
type Repo struct {
	// Root is the top directory of the working tree, the one holding .git; "" outside git.
	Root string
	// Main is the top directory of the main working tree: Root, unless Root is a linked worktree. For a worktree of a
	// bare repository it is the repository itself.
	Main string
	// Branch is the checked-out branch, "" when HEAD is detached.
	Branch string
}

// FindRepo is the repository of dir.
func FindRepo(dir string) Repo {
	root := Find(dir, ".git")
	if root == "" {
		return Repo{}
	}
	r := Repo{Root: root, Main: root}
	gitdir := filepath.Join(root, ".git")
	if info, err := os.Stat(gitdir); err == nil && !info.IsDir() { // a linked worktree or a submodule
		raw, err := os.ReadFile(gitdir)
		p, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
		if err != nil || !ok {
			return r
		}
		gitdir = resolve(root, p)
		if raw, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil { // only a linked worktree has one
			common := resolve(gitdir, strings.TrimSpace(string(raw)))
			r.Main = common
			if filepath.Base(common) == ".git" {
				r.Main = filepath.Dir(common)
			}
		}
	}
	if head, err := os.ReadFile(filepath.Join(gitdir, "HEAD")); err == nil {
		if b, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/"); ok {
			r.Branch = b
		}
	}
	return r
}

// resolve is the path p that git wrote, relative to base unless absolute.
func resolve(base, p string) string {
	if p = filepath.FromSlash(p); filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// Find is the nearest directory from dir up that holds name, or "".
func Find(dir, name string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
