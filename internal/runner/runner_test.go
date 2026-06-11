package runner

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/Tarektouati/made/internal/parser"
)

func TestRunSuccess(t *testing.T) {
	if err := Run(parser.Task{Name: "ok", Script: "exit 0"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	err := Run(parser.Task{Name: "fail", Script: "exit 3"})
	if err == nil {
		t.Fatal("Run: want error, got nil")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run: error type = %T, want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("ExitCode = %d, want 3", exitErr.ExitCode())
	}
}
