// Package check runs the configured fixers and checks on a written file.
package check

import (
	"os"
	"os/exec"
	"strings"

	"github.com/hakastein/catnip/internal/config"
	"github.com/hakastein/catnip/internal/edit"
)

// Failure is a fixer or check that exited non-zero, with its combined output. Name is the check's name or the
// fixer's command.
type Failure struct {
	Name   string
	Output string
}

// Result of the commands on one file.
type Result struct {
	// Diff is what the fixers changed.
	Diff   string
	Failed []Failure
	// Passed names the checks that passed, in run order.
	Passed []string
}

// Run runs the fixers and then the checks on file through sh, in the current directory.
func Run(file string, fix []string, checks []config.Check) (Result, error) {
	var r Result
	if len(fix) > 0 {
		before, err := os.ReadFile(file)
		if err != nil {
			return r, err
		}
		for _, cmd := range fix {
			r.run(cmd, cmd)
		}
		after, err := os.ReadFile(file)
		if err != nil {
			return r, err
		}
		r.Diff = edit.Diff(file, before, after)
	}
	for _, c := range checks {
		if r.run(c.Name, c.Cmd) {
			r.Passed = append(r.Passed, c.Name)
		}
	}
	return r, nil
}

func (r *Result) run(name, cmd string) bool {
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		msg := strings.TrimRight(string(out), "\n")
		if msg != "" {
			msg += "\n"
		}
		r.Failed = append(r.Failed, Failure{name, msg + err.Error()})
		return false
	}
	return true
}
