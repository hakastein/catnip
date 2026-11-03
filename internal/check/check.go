// Package check runs the configured fixers and checks on a written file.
package check

import (
	"os"
	"os/exec"
	"strings"

	"github.com/hakastein/catnip/internal/edit"
)

// Failure is a command that exited non-zero, with its combined output.
type Failure struct {
	Cmd    string
	Output string
}

// Result of the commands on one file.
type Result struct {
	// Diff is what the fixers changed.
	Diff string
	// Failed holds the fixers and checks that failed; Passed counts the checks that passed.
	Failed []Failure
	Passed int
}

// Run runs the fixers and then the checks on file through sh, in the current directory.
func Run(file string, fix, check []string) (Result, error) {
	var r Result
	if len(fix) > 0 {
		before, err := os.ReadFile(file)
		if err != nil {
			return r, err
		}
		for _, cmd := range fix {
			r.run(cmd)
		}
		after, err := os.ReadFile(file)
		if err != nil {
			return r, err
		}
		r.Diff = edit.Diff(file, before, after)
	}
	for _, cmd := range check {
		if r.run(cmd) {
			r.Passed++
		}
	}
	return r, nil
}

func (r *Result) run(cmd string) bool {
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		msg := strings.TrimRight(string(out), "\n")
		if msg != "" {
			msg += "\n"
		}
		r.Failed = append(r.Failed, Failure{cmd, msg + err.Error()})
		return false
	}
	return true
}
