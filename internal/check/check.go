// Package check runs the configured fixers and checks on a written file.
package check

import (
	"os"
	"os/exec"
	"strings"

	"github.com/hakastein/catnip/internal/config"
	"github.com/hakastein/catnip/internal/edit"
)

// Outcome of one fixer or check, under its configured name.
type Outcome struct {
	Name string
	// Failed holds the combined output and exit status of a command that exited non-zero.
	Failed string
	// Diff is what a fixer changed in the file.
	Diff string
}

// Run runs the fixers and then the checks on file through sh in dir (the current directory if dir is "") and
// returns their outcomes in run order.
func Run(dir, file string, fix, check []config.Command) ([]Outcome, error) {
	var out []Outcome
	for _, c := range fix {
		before, err := os.ReadFile(file)
		if err != nil {
			return out, err
		}
		o := run(dir, c)
		after, err := os.ReadFile(file)
		if err != nil {
			return out, err
		}
		o.Diff = edit.Diff(file, before, after)
		out = append(out, o)
	}
	for _, c := range check {
		out = append(out, run(dir, c))
	}
	return out, nil
}

func run(dir string, c config.Command) Outcome {
	cmd := exec.Command("sh", "-c", c.Cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return Outcome{Name: c.Name}
	}
	msg := strings.TrimRight(string(out), "\n")
	if msg != "" {
		msg += "\n"
	}
	return Outcome{Name: c.Name, Failed: msg + err.Error()}
}
