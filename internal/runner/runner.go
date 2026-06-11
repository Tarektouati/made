// Package runner executes a parsed task's script.
package runner

import (
	"context"
	"os"
	"os/exec"

	"github.com/Tarektouati/made/internal/parser"
)

// Run executes the task's script with `bash -c`, inheriting the current process's
// stdin/stdout/stderr, environment, and working directory. A failing script returns
// an *exec.ExitError whose ExitCode the caller can propagate.
func Run(t parser.Task) error {
	cmd := exec.CommandContext(context.Background(), "bash", "-c", t.Script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
