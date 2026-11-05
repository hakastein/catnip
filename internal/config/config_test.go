package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/catnip/internal/config"
)

const global = `{"*.php": {"check": {"php lint": "php -l {file}"}}, "*.go": {"fix": {"gofmt": "gofmt -w {file}"}, "check": {"go vet": "go vet ."}}}`

// load writes the global config and a project config into a fresh project and loads them from its src
// directory; the working directory stays at the project root.
func load(t *testing.T, project string) (*config.Config, error) {
	home, repo := t.TempDir(), t.TempDir()
	isolateUserConfig(t, home)
	dir, err := os.UserConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "catnip"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "catnip", "config.json"), []byte(global), 0o644))
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

func TestForFindsTheNearestProject(t *testing.T) {
	tests := []struct {
		name     string
		projects map[string]string // directory → project config
		check    []config.Command
	}{
		{
			name:     "project above the file, path from its root",
			projects: map[string]string{"a": `{"*.txt": {"check": {"outer": "lint {file}"}}}`},
			check:    []config.Command{{Name: "outer", Cmd: "lint 'b/x.txt'"}},
		},
		{
			name: "nested project replaces the outer one",
			projects: map[string]string{
				"a":   `{"*.txt": {"check": {"outer": "lint {file}"}}}`,
				"a/b": `{"*.txt": {"check": {"inner": "lint {file}"}}}`,
			},
			check: []config.Command{{Name: "inner", Cmd: "lint 'x.txt'"}},
		},
		{name: "no project", projects: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateUserConfig(t, t.TempDir())
			t.Chdir(t.TempDir())
			require.NoError(t, os.MkdirAll(filepath.Join("a", "b"), 0o755))
			for dir, project := range tt.projects {
				require.NoError(t, os.WriteFile(filepath.Join(dir, config.ProjectFile), []byte(project), 0o644))
			}
			file := filepath.Join("a", "b", "x.txt")
			c, err := config.Load(filepath.Dir(file))
			require.NoError(t, err)

			_, check := c.For(file)

			assert.Equal(t, tt.check, check)
		})
	}
}

// tree writes files, path to content, into a fresh directory and returns it; "~" in a content stands for the directory.
func tree(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(content, "~", dir)), 0o644))
	}
	return dir
}

// git files of a main working tree "main" on branch main with a linked worktree "wt" on branch feature/x.
var worktree = map[string]string{
	"main/.git/HEAD":                   "ref: refs/heads/main\n",
	"main/.git/worktrees/wt/HEAD":      "ref: refs/heads/feature/x\n",
	"main/.git/worktrees/wt/commondir": "../..\n",
	"wt/.git":                          "gitdir: ~/main/.git/worktrees/wt\n",
}

func with(files map[string]string, more ...string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}
	for i := 0; i < len(more); i += 2 {
		out[more[i]] = more[i+1]
	}
	return out
}

func TestFindRepo(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		dir   string
		want  config.Repo // Root and Main relative to the tree
	}{
		{name: "main working tree", files: worktree, dir: "main/src", want: config.Repo{Root: "main", Main: "main", Branch: "main"}},
		{name: "linked worktree", files: worktree, dir: "wt/src", want: config.Repo{Root: "wt", Main: "main", Branch: "feature/x"}},
		{
			name:  "relative gitdir, detached HEAD",
			files: with(worktree, "wt/.git", "gitdir: ../main/.git/worktrees/wt", "main/.git/worktrees/wt/HEAD", "1f0e2d3c\n"),
			dir:   "wt",
			want:  config.Repo{Root: "wt", Main: "main"},
		},
		{
			name: "worktree of a bare repository",
			files: map[string]string{
				"r.git/HEAD":                   "ref: refs/heads/main\n",
				"r.git/worktrees/wt/HEAD":      "ref: refs/heads/main\n",
				"r.git/worktrees/wt/commondir": "../..\n",
				"wt/.git":                      "gitdir: ~/r.git/worktrees/wt\n",
			},
			dir:  "wt",
			want: config.Repo{Root: "wt", Main: "r.git", Branch: "main"},
		},
		{
			name: "submodule is a repository of its own",
			files: map[string]string{
				"super/.git/HEAD":             "ref: refs/heads/main\n",
				"super/.git/modules/sub/HEAD": "ref: refs/heads/dev\n",
				"super/sub/.git":              "gitdir: ../.git/modules/sub\n",
			},
			dir:  "super/sub/src",
			want: config.Repo{Root: "super/sub", Main: "super/sub", Branch: "dev"},
		},
		{name: "outside git", files: map[string]string{"a/x": ""}, dir: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tree(t, tt.files)

			got := config.FindRepo(filepath.Join(dir, filepath.FromSlash(tt.dir)))

			assert.Equal(t, tt.want, config.Repo{Root: rel(t, dir, got.Root), Main: rel(t, dir, got.Main), Branch: got.Branch})
		})
	}
}

func TestProject(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		dir        string
		root, file string // relative to the tree
	}{
		{
			name:  "main working tree: nearest config",
			files: with(worktree, "main/.catnip.json", "{}", "main/sub/.catnip.json", "{}"),
			dir:   "main/sub/x",
			root:  "main/sub", file: "main/sub/.catnip.json",
		},
		{
			name:  "linked worktree with a config of its own",
			files: with(worktree, "main/.catnip.json", "{}", "wt/.catnip.json", "{}"),
			dir:   "wt/sub",
			root:  "wt", file: "wt/.catnip.json",
		},
		{
			name:  "linked worktree without one: main tree config, rooted in the worktree",
			files: with(worktree, "main/sub/.catnip.json", "{}", "wt/sub/x/a", ""),
			dir:   "wt/sub/x",
			root:  "wt/sub", file: "main/sub/.catnip.json",
		},
		{
			name: "worktree inside the main tree does not take the config above it as its own",
			files: map[string]string{
				"main/.git/HEAD":                   "ref: refs/heads/main\n",
				"main/.git/worktrees/wt/HEAD":      "ref: refs/heads/x\n",
				"main/.git/worktrees/wt/commondir": "../..\n",
				"main/.catnip.json":                "{}",
				"main/.wt/x/.git":                  "gitdir: ~/main/.git/worktrees/wt\n",
				"main/.wt/x/src/a":                 "",
			},
			dir:  "main/.wt/x/src",
			root: "main/.wt/x", file: "main/.catnip.json",
		},
		{
			name:  "config above the main tree",
			files: with(worktree, ".catnip.json", "{}"),
			dir:   "wt",
			root:  ".", file: ".catnip.json",
		},
		{name: "linked worktree, no config", files: worktree, dir: "wt"},
		{
			name: "submodule takes the config above it",
			files: map[string]string{
				"super/.git/HEAD":             "ref: refs/heads/main\n",
				"super/.git/modules/sub/HEAD": "ref: refs/heads/main\n",
				"super/.catnip.json":          "{}",
				"super/sub/.git":              "gitdir: ../.git/modules/sub\n",
			},
			dir:  "super/sub",
			root: "super", file: "super/.catnip.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tree(t, tt.files)

			root, file := config.Project(filepath.Join(dir, filepath.FromSlash(tt.dir)))

			assert.Equal(t, []string{tt.root, tt.file}, []string{rel(t, dir, root), rel(t, dir, file)})
		})
	}
}

func TestLoadInLinkedWorktree(t *testing.T) {
	isolateUserConfig(t, t.TempDir())
	dir := tree(t, with(worktree, "main/.catnip.json", `{"sub/*.txt": {"check": {"lint": "lint {file}"}}}`, "wt/sub/a.txt", ""))

	c, err := config.Load(filepath.Join(dir, "wt", "sub"))
	require.NoError(t, err)
	_, check := c.For(filepath.Join(dir, "wt", "sub", "a.txt"))

	assert.Equal(t, filepath.Join(dir, "wt"), c.Root)
	assert.Equal(t, []config.Command{{Name: "lint", Cmd: "lint 'sub/a.txt'"}}, check)
}

// rel is path relative to dir with forward slashes, "" for "".
func rel(t *testing.T, dir, path string) string {
	if path == "" {
		return ""
	}
	r, err := filepath.Rel(dir, path)
	require.NoError(t, err)
	return filepath.ToSlash(r)
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
