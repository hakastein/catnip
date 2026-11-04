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
	// Root is the directory holding the project config, "" without one; globs with "/" match paths relative to it
	// and the commands run in it.
	Root  string
	rules map[string]Rule
}

// ProjectFile is the project config's name; its directory is the project root.
const ProjectFile = ".catnip.json"

// Load reads the global config (<user config dir>/catnip/config.json) and the nearest project config from dir up. A project rule replaces the global rule for the same glob. A missing file is no rules.
func Load(dir string) (*Config, error) {
	c := &Config{Root: root(dir), rules: map[string]Rule{}}
	d, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("user config directory: %w", err)
	}
	paths := []string{filepath.Join(d, "catnip", "config.json")}
	if c.Root != "" {
		paths = append(paths, filepath.Join(c.Root, ProjectFile))
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

// root is the nearest directory from dir up that holds the project config, or "".
func root(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ProjectFile)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
