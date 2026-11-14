package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/parser"
)

func TestRunSuccess(t *testing.T) {
	if err := Run(context.Background(), parser.Task{Name: "ok", Script: "exit 0"}, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	err := Run(context.Background(), parser.Task{Name: "fail", Script: "exit 3"}, "")
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
	err := Run(context.Background(), parser.Task{Name: "x", Lang: "cobol", Script: "x"}, "")
	if err == nil {
		t.Fatal("Run: want error for unsupported language, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "unsupported language")
	}
}

func TestRunNormalizesLanguage(t *testing.T) {
	// Mixed case and surrounding space still resolve to bash.
	if err := Run(context.Background(), parser.Task{Name: "ok", Lang: " BASH ", Script: "exit 0"}, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// requireCmd skips the test when name is not on PATH.
func requireCmd(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found on PATH; skipping", name)
	}
}

// wantExitCode asserts err is an *exec.ExitError with the given code.
func wantExitCode(t *testing.T, err error, code int) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run: error = %v (%T), want *exec.ExitError", err, err)
	}
	if exitErr.ExitCode() != code {
		t.Errorf("ExitCode = %d, want %d", exitErr.ExitCode(), code)
	}
}

func TestRunShellStopsOnFailure(t *testing.T) {
	// A failing intermediate command must not be masked by a later successful one.
	for _, tc := range []struct{ lang, shell string }{
		{"", "bash"}, {"bash", "bash"}, {"shell", "bash"}, {"sh", "sh"}, {"zsh", "zsh"},
	} {
		t.Run("lang="+tc.lang, func(t *testing.T) {
			requireCmd(t, tc.shell)
			err := Run(context.Background(), parser.Task{Name: "x", Lang: tc.lang, Script: "false\nexit 0\n"}, "")
			wantExitCode(t, err, 1)
		})
	}
}

func TestRunShellPipefail(t *testing.T) {
	for _, lang := range []string{"bash", "zsh"} {
		t.Run(lang, func(t *testing.T) {
			requireCmd(t, lang)
			err := Run(context.Background(), parser.Task{Name: "x", Lang: lang, Script: "false | true\n"}, "")
			wantExitCode(t, err, 1)
		})
	}
}

func TestRunShNoPipefail(t *testing.T) {
	// sh gets errexit only: pipefail isn't portable across sh implementations (e.g.
	// older dash), so a pipeline's status stays the last command's.
	requireCmd(t, "sh")
	if err := Run(context.Background(), parser.Task{Name: "x", Lang: "sh", Script: "false | true\n"}, ""); err != nil {
		t.Fatalf("Run: %v, want nil (sh must not enable pipefail)", err)
	}
}

func TestRunShellOptOut(t *testing.T) {
	requireCmd(t, "bash")
	for name, script := range map[string]string{
		"errexit":  "set +e\nfalse\nexit 0\n",
		"pipefail": "set +o pipefail\nfalse | true\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := Run(context.Background(), parser.Task{Name: "x", Lang: "bash", Script: script}, ""); err != nil {
				t.Fatalf("Run: %v, want nil after opting out", err)
			}
		})
	}
}

// writeMarker creates an empty file named "marker" in dir.
func writeMarker(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "marker"), nil, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

func TestRunUsesDir(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir)
	t.Chdir(t.TempDir()) // the marker must not be reachable from the cwd
	if err := Run(context.Background(), parser.Task{Name: "ok", Script: "test -f marker"}, dir); err != nil {
		t.Fatalf("Run in %s: %v", dir, err)
	}
}

func TestRunEmptyDirInheritsCwd(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir)
	t.Chdir(dir)
	if err := Run(context.Background(), parser.Task{Name: "ok", Script: "test -f marker"}, ""); err != nil {
		t.Fatalf("Run with empty dir: %v", err)
	}
}
