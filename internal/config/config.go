// Package config maps file globs to the fixers and checks run after a file is written.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Rule is what runs on a file matching a glob: Fix commands may change the file, Check commands must not.
// "{file}" in a command is replaced by the file path.
type Rule struct {
	Fix   []string `json:"fix"`
	Check []string `json:"check"`
}

// Config is the merged project and global configuration.
type Config struct {
	// Root is the repository root holding the project config; globs with "/" match paths relative to it.
	Root  string
	rules map[string]Rule
}

// ProjectFile is the project config's name in the repository root.
const ProjectFile = ".catnip.json"

// Load reads the global config (<user config dir>/catnip/config.json) and the project config of the repository
// containing dir. A project rule replaces the global rule for the same glob. A missing file is no rules.
func Load(dir string) (*Config, error) {
	c := &Config{Root: root(dir), rules: map[string]Rule{}}
	paths := []string{}
	if d, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(d, "catnip", "config.json"))
	}
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
			c.rules[glob] = r
		}
	}
	return c, nil
}

// For lists the fixers and checks for file, in glob order, with "{file}" filled in.
func (c *Config) For(file string) (fix, check []string) {
	name := filepath.Base(file)
	rel := ""
	if abs, err := filepath.Abs(file); err == nil && c.Root != "" {
		if r, err := filepath.Rel(c.Root, abs); err == nil {
			if r = filepath.ToSlash(r); r != ".." && !strings.HasPrefix(r, "../") {
				rel = r
			}
		}
	}
	globs := make([]string, 0, len(c.rules))
	for g := range c.rules {
		globs = append(globs, g)
	}
	sort.Strings(globs)
	quoted := "'" + strings.ReplaceAll(file, "'", `'\''`) + "'"
	for _, g := range globs {
		subject := name
		if strings.Contains(g, "/") {
			subject = rel
		}
		if ok, _ := path.Match(g, subject); !ok {
			continue
		}
		for _, cmd := range c.rules[g].Fix {
			fix = append(fix, strings.ReplaceAll(cmd, "{file}", quoted))
		}
		for _, cmd := range c.rules[g].Check {
			check = append(check, strings.ReplaceAll(cmd, "{file}", quoted))
		}
	}
	return fix, check
}

// root is the nearest directory from dir up that holds .git, or "".
func root(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
