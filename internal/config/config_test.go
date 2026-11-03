package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/config"
)

const global = `{"*.php": {"check": {"php lint": "php -l {file}"}}, "*.go": {"fix": {"gofmt": "gofmt -w {file}"}, "check": {"go vet": "go vet ."}}}`

// load writes the global config and a project config into a fresh repository and loads them from its src
// directory; the working directory stays at the repository root.
func load(t *testing.T, project string) (*config.Config, error) {
	home, repo := t.TempDir(), t.TempDir()
	isolateUserConfig(t, home)
	dir, err := os.UserConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "catnip"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "catnip", "config.json"), []byte(global), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, config.ProjectFile), []byte(project), 0o644))
	t.Chdir(filepath.Join(repo, "src"))
	c, err := config.Load(".")
	t.Chdir(repo)
	return c, err
}

// isolateUserConfig points the user config directory of every platform into dir.
func isolateUserConfig(t *testing.T, dir string) {
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS: $HOME/Library/Application Support
	t.Setenv("AppData", dir)         // Windows
}

func TestFor(t *testing.T) {
	tests := []struct {
		name    string
		project string
		file    string
		fix     []config.Command
		check   []config.Command
	}{
		{name: "global rule by base name", project: "{}", file: "src/a.php", check: []config.Command{{Name: "php lint", Cmd: "php -l 'src/a.php'"}}},
		{name: "no rule", project: "{}", file: "a.txt"},
		{
			name:    "project rule replaces the global one for its glob",
			project: `{"*.go": {"check": {"lint": "make lint", "build": "make build"}}}`,
			file:    "a.go",
			check:   []config.Command{{Name: "build", Cmd: "make build"}, {Name: "lint", Cmd: "make lint"}},
		},
		{name: "empty project rule disables the global one", project: `{"*.php": {}}`, file: "a.php"},
		{
			name:    "glob with a slash matches the path from the root, rules in glob order",
			project: `{"src/*.php": {"fix": {"fixer": "fix {file}"}}, "*.php": {"check": {"php lint": "php -l {file}"}}}`,
			file:    "src/it's.php",
			fix:     []config.Command{{Name: "fixer", Cmd: `fix 'src/it'\''s.php'`}},
			check:   []config.Command{{Name: "php lint", Cmd: `php -l 'src/it'\''s.php'`}},
		},
		{
			name:    "glob with a slash does not match deeper",
			project: `{"src/*.php": {"fix": {"fixer": "fix {file}"}}}`,
			file:    "src/x/a.php",
			check:   []config.Command{{Name: "php lint", Cmd: "php -l 'src/x/a.php'"}},
		},
		{
			name:    "commands of several globs come in glob order",
			project: `{"b*": {"fix": {"a": "second"}}, "*.txt": {"fix": {"z": "first", "y": "first, by name"}}}`,
			file:    "b.txt",
			fix:     []config.Command{{Name: "y", Cmd: "first, by name"}, {Name: "z", Cmd: "first"}, {Name: "a", Cmd: "second"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := load(t, tt.project)
			require.NoError(t, err)

			fix, check := c.For(tt.file)

			assert.Equal(t, tt.fix, fix)
			assert.Equal(t, tt.check, check)
		})
	}
}

func TestLoadRejectsBrokenConfig(t *testing.T) {
	tests := []struct {
		name    string
		project string
		err     string
	}{
		{name: "broken json", project: `{"*.go": `, err: "unexpected EOF"},
		{name: "unknown field", project: `{"*.go": {"checks": []}}`, err: `json: unknown field "checks"`},
		{name: "bad glob", project: `{"[": {}}`, err: `glob "[": syntax error in pattern`},
		{name: "text after the object", project: `{} {}`, err: "text after the config object"},
		{name: "null command", project: `{"*.txt": {"check": {"x": null}}}`, err: `glob "*.txt": check: "x": empty or null command`},
		{name: "null fixer", project: `{"*.txt": {"fix": {"x": null}}}`, err: `glob "*.txt": fix: "x": empty or null command`},
		{name: "fixers as a list", project: `{"*.txt": {"fix": ["true"]}}`, err: "cannot unmarshal array"},
		{name: "empty check name", project: `{"*.txt": {"check": {"": "true"}}}`, err: `glob "*.txt": check: empty name`},
		{name: "checks as a list", project: `{"*.txt": {"check": ["true"]}}`, err: "cannot unmarshal array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(t, tt.project)

			assert.ErrorContains(t, err, tt.err)
		})
	}
}
