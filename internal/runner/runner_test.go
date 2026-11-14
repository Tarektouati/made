package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/parser"
)

func TestRunSuccess(t *testing.T) {
	if err := Run(context.Background(), parser.Task{Name: "ok", Script: "exit 0"}, "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	err := Run(context.Background(), parser.Task{Name: "fail", Script: "exit 3"}, "", nil)
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
	err := Run(context.Background(), parser.Task{Name: "x", Lang: "cobol", Script: "x"}, "", nil)
	if err == nil {
		t.Fatal("Run: want error for unsupported language, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "unsupported language")
	}
}

func TestRunNormalizesLanguage(t *testing.T) {
	// Mixed case and surrounding space still resolve to bash.
	if err := Run(context.Background(), parser.Task{Name: "ok", Lang: " BASH ", Script: "exit 0"}, "", nil); err != nil {
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
			err := Run(context.Background(), parser.Task{Name: "x", Lang: tc.lang, Script: "false\nexit 0\n"}, "", nil)
			wantExitCode(t, err, 1)
		})
	}
}

func TestRunShellPipefail(t *testing.T) {
	for _, lang := range []string{"bash", "zsh"} {
		t.Run(lang, func(t *testing.T) {
			requireCmd(t, lang)
			err := Run(context.Background(), parser.Task{Name: "x", Lang: lang, Script: "false | true\n"}, "", nil)
			wantExitCode(t, err, 1)
		})
	}
}

func TestRunShNoPipefail(t *testing.T) {
	// sh gets errexit only: pipefail isn't portable across sh implementations (e.g.
	// older dash), so a pipeline's status stays the last command's.
	requireCmd(t, "sh")
	if err := Run(context.Background(), parser.Task{Name: "x", Lang: "sh", Script: "false | true\n"}, "", nil); err != nil {
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
			if err := Run(context.Background(), parser.Task{Name: "x", Lang: "bash", Script: script}, "", nil); err != nil {
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
	if err := Run(context.Background(), parser.Task{Name: "ok", Script: "test -f marker"}, dir, nil); err != nil {
		t.Fatalf("Run in %s: %v", dir, err)
	}
}

func TestRunEmptyDirInheritsCwd(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir)
	t.Chdir(dir)
	if err := Run(context.Background(), parser.Task{Name: "ok", Script: "test -f marker"}, "", nil); err != nil {
		t.Fatalf("Run with empty dir: %v", err)
	}
}

func TestRunMissingInterpreter(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tests := []struct {
		name, lang string
		want       []string
	}{
		{"python", "python", []string{`"python3" was not found on PATH`, `task "py"`, `language "python"`}},
		{"plain fence", "", []string{`"bash" was not found on PATH`, `task "py"`, `"bash" (default for a plain fence)`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Run(context.Background(), parser.Task{Name: "py", Lang: tt.lang, Script: "x"}, "", nil)
			if !errors.Is(err, exec.ErrNotFound) {
				t.Fatalf("Run: error = %v, want it to wrap exec.ErrNotFound", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), w)
				}
			}
		})
	}
}

func TestRunPassesArgs(t *testing.T) {
	// Each script exits non-zero unless it received exactly ["-x", "b c"], which covers
	// dash-prefixed arguments (not taken as interpreter options) and embedded spaces.
	args := []string{"-x", "b c"}
	shellCheck := `[ "$#" = 2 ] && [ "$1" = -x ] && [ "$2" = "b c" ]`
	for _, tc := range []struct{ lang, bin, script string }{
		{"", "bash", shellCheck},
		{"bash", "bash", shellCheck},
		{"sh", "sh", shellCheck},
		{"zsh", "zsh", shellCheck},
		{"fish", "fish", `test (count $argv) = 2; and test $argv[1] = -x; and test $argv[2] = "b c"`},
		{"python", "python3", `import sys; sys.exit(sys.argv[1:] != ["-x", "b c"])`},
		{"node", "node", `process.exit(JSON.stringify(process.argv.slice(1)) === '["-x","b c"]' ? 0 : 1)`},
		{"ruby", "ruby", `exit(ARGV == ["-x", "b c"])`},
		{"perl", "perl", `exit(!(@ARGV == 2 && $ARGV[0] eq "-x" && $ARGV[1] eq "b c"))`},
	} {
		t.Run("lang="+tc.lang, func(t *testing.T) {
			requireCmd(t, tc.bin)
			if err := Run(context.Background(), parser.Task{Name: "x", Lang: tc.lang, Script: tc.script}, "", args); err != nil {
				t.Fatalf("Run: %v, want args %q passed through", err, args)
			}
		})
	}
}

func TestRunShellDollarZero(t *testing.T) {
	// $0 is "made" whether or not the task gets arguments.
	for _, shell := range []string{"bash", "sh", "zsh"} {
		for _, args := range [][]string{nil, {"a"}} {
			t.Run(fmt.Sprintf("%s/args=%d", shell, len(args)), func(t *testing.T) {
				requireCmd(t, shell)
				if err := Run(context.Background(), parser.Task{Name: "x", Lang: shell, Script: `[ "$0" = made ]`}, "", args); err != nil {
					t.Fatalf("Run: %v, want $0 = made", err)
				}
			})
		}
	}
}

func TestRunNoArgsSeparatorNotPassed(t *testing.T) {
	// The "--" separator is always appended; with no task arguments it must not show
	// up as an argument itself.
	for _, tc := range []struct{ lang, bin, script string }{
		{"fish", "fish", `test (count $argv) = 0`},
		{"node", "node", `process.exit(process.argv.length === 1 ? 0 : 1)`},
		{"ruby", "ruby", `exit(ARGV.empty?)`},
		{"perl", "perl", `exit(@ARGV != 0)`},
	} {
		t.Run("lang="+tc.lang, func(t *testing.T) {
			requireCmd(t, tc.bin)
			if err := Run(context.Background(), parser.Task{Name: "x", Lang: tc.lang, Script: tc.script}, "", nil); err != nil {
				t.Fatalf("Run: %v, want no arguments", err)
			}
		})
	}
}
