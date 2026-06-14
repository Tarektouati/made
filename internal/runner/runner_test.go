package runner

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/parser"
)

func TestRunSuccess(t *testing.T) {
	if err := Run(context.Background(), parser.Task{Name: "ok", Script: "exit 0"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	err := Run(context.Background(), parser.Task{Name: "fail", Script: "exit 3"})
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

func TestRunUnsupportedLanguage(t *testing.T) {
	err := Run(context.Background(), parser.Task{Name: "x", Lang: "cobol", Script: "x"})
	if err == nil {
		t.Fatal("Run: want error for unsupported language, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "unsupported language")
	}
}

func TestRunNormalizesLanguage(t *testing.T) {
	// Mixed case and surrounding space still resolve to bash.
	if err := Run(context.Background(), parser.Task{Name: "ok", Lang: " BASH ", Script: "exit 0"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
