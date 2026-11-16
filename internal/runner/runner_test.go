package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/parser"
)

func TestRunSuccess(t *testing.T) {
	if err := Run(context.Background(), nil, parser.Task{Name: "ok", Script: "exit 0"}, "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	err := Run(context.Background(), nil, parser.Task{Name: "fail", Script: "exit 3"}, "", nil)
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
	err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: "cobol", Script: "x"}, "", nil)
	if err == nil {
		t.Fatal("Run: want error for unsupported language, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "unsupported language")
	}
}

func TestRunNormalizesLanguage(t *testing.T) {
	// Mixed case and surrounding space still resolve to bash.
	if err := Run(context.Background(), nil, parser.Task{Name: "ok", Lang: " BASH ", Script: "exit 0"}, "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// sigCause is a cancellation cause that reports a signal through a Signal method.
type sigCause struct{ sig os.Signal }

func (c sigCause) Error() string     { return c.sig.String() }
func (c sigCause) Signal() os.Signal { return c.sig }

func TestCancelSignal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error // nil: plain cancel
		want  os.Signal
	}{
		{"plain cancel", nil, nil},
		{"signal cause", sigCause{os.Interrupt}, os.Interrupt},
		{"wrapped signal cause", fmt.Errorf("stopping: %w", sigCause{os.Kill}), os.Kill},
		{"cause without a signal", errors.New("deadline"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(tc.cause)
			if got := cancelSignal(ctx); got != tc.want {
				t.Errorf("cancelSignal = %v, want %v", got, tc.want)
			}
		})
	}
	if got := cancelSignal(context.Background()); got != nil {
		t.Errorf("cancelSignal(uncancelled) = %v, want nil", got)
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
			err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: tc.lang, Script: "false\nexit 0\n"}, "", nil)
			wantExitCode(t, err, 1)
		})
	}
}

func TestRunShellPipefail(t *testing.T) {
	for _, lang := range []string{"bash", "zsh"} {
		t.Run(lang, func(t *testing.T) {
			requireCmd(t, lang)
			err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: lang, Script: "false | true\n"}, "", nil)
			wantExitCode(t, err, 1)
		})
	}
}

func TestRunShNoPipefail(t *testing.T) {
	// sh gets errexit only: pipefail isn't portable across sh implementations (e.g.
	// older dash), so a pipeline's status stays the last command's.
	requireCmd(t, "sh")
	if err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: "sh", Script: "false | true\n"}, "", nil); err != nil {
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
			if err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: "bash", Script: script}, "", nil); err != nil {
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
	if err := Run(context.Background(), nil, parser.Task{Name: "ok", Script: "test -f marker"}, dir, nil); err != nil {
		t.Fatalf("Run in %s: %v", dir, err)
	}
}

func TestRunEmptyDirInheritsCwd(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir)
	t.Chdir(dir)
	if err := Run(context.Background(), nil, parser.Task{Name: "ok", Script: "test -f marker"}, "", nil); err != nil {
		t.Fatalf("Run with empty dir: %v", err)
	}
}

// fakeBin is a file written into a test PATH directory.
type fakeBin struct {
	name string
	mode os.FileMode
	body string
}

// fakePath sets PATH to a fresh directory holding bins and returns the directory.
func fakePath(t *testing.T, bins ...fakeBin) string {
	t.Helper()
	dir := t.TempDir()
	for _, b := range bins {
		p := filepath.Join(dir, b.name)
		if err := os.WriteFile(p, []byte(b.body), b.mode); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		if err := os.Chmod(p, b.mode); err != nil { // WriteFile's mode is subject to umask
			t.Fatalf("chmod %s: %v", p, err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestRunInterpreterStartErrors(t *testing.T) {
	tests := []struct {
		name     string
		lang     string
		bins     []fakeBin
		unixOnly bool
		wantIs   error
		wantCode int
		want     []string
		notWant  []string
	}{
		{
			name: "python not found", lang: "python",
			wantIs: exec.ErrNotFound, wantCode: 127,
			want:    []string{`neither "python3" nor "python" was found on PATH`, `task "py"`, `language "python"`},
			notWant: []string{"executable file not found"},
		},
		{
			name: "python3 fence does not fall back", lang: "python3",
			bins:     []fakeBin{{"python", 0o755, "#!/bin/sh\nexit 0\n"}},
			unixOnly: true,
			wantIs:   exec.ErrNotFound, wantCode: 127,
			want:    []string{`"python3" was not found on PATH`, `language "python3"`},
			notWant: []string{"executable file not found", "neither"},
		},
		{
			name:   "plain fence not found",
			wantIs: exec.ErrNotFound, wantCode: 127,
			want:    []string{`"bash" was not found on PATH`, `task "py"`, `"bash" (default for a plain fence)`},
			notWant: []string{"executable file not found"},
		},
		{
			name:     "not executable",
			bins:     []fakeBin{{"bash", 0o644, "#!/bin/sh\nexit 0\n"}},
			unixOnly: true,
			wantIs:   fs.ErrPermission, wantCode: 126,
			want:    []string{`"bash" at `, "is not executable", `task "py"`},
			notWant: []string{"was not found"},
		},
		{
			name:     "broken shebang",
			bins:     []fakeBin{{"bash", 0o755, "#!/nonexistent/interp\n"}},
			unixOnly: true,
			wantIs:   fs.ErrNotExist, wantCode: 126,
			want:    []string{`cannot run "bash" at `, `task "py"`, "#! line"},
			notWant: []string{"fork/exec"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unixOnly && runtime.GOOS == "windows" {
				t.Skip("needs Unix permission bits and shebangs")
			}
			dir := fakePath(t, tt.bins...)
			err := Run(context.Background(), nil, parser.Task{Name: "py", Lang: tt.lang, Script: "x"}, "", nil)
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("Run: error = %v, want it to wrap %v", err, tt.wantIs)
			}
			var ie *InterpreterError
			if !errors.As(err, &ie) {
				t.Fatalf("Run: error = %v (%T), want *InterpreterError", err, err)
			}
			if got := ie.ExitCode(); got != tt.wantCode {
				t.Errorf("ExitCode = %d, want %d", got, tt.wantCode)
			}
			msg := err.Error()
			if len(tt.bins) > 0 && tt.wantIs != exec.ErrNotFound && !strings.Contains(msg, filepath.Join(dir, tt.bins[0].name)) {
				t.Errorf("error = %q, want it to name the path in %s", msg, dir)
			}
			for _, w := range tt.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error = %q, want it to contain %q", msg, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(msg, w) {
					t.Errorf("error = %q, want it not to contain %q", msg, w)
				}
			}
		})
	}
}

func TestInterpreterErrorMessage(t *testing.T) {
	// A start errno that matches fs.ErrPermission (EACCES, e.g. a noexec mount) is not
	// the "not executable" case, which only a PATH scan finding the file reports.
	tests := []struct {
		name    string
		err     *InterpreterError
		want    string
		notWant string
	}{
		{"not executable", &InterpreterError{Cmd: "bash", Path: "/x/bash", Err: fs.ErrPermission, NotExecutable: true}, `"bash" at /x/bash is not executable`, "cannot run"},
		{"start EACCES", &InterpreterError{Cmd: "bash", Path: "/x/bash", Err: syscall.EACCES}, `cannot run "bash" at /x/bash`, "is not executable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.want)
			}
			if strings.Contains(msg, tt.notWant) {
				t.Errorf("error = %q, want it not to contain %q", msg, tt.notWant)
			}
			if got := tt.err.ExitCode(); got != 126 {
				t.Errorf("ExitCode = %d, want 126", got)
			}
		})
	}
}

func TestRunPythonFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake interpreters are shell scripts")
	}
	// Each fake interpreter exits with its own code, so the error tells which one ran.
	tests := []struct {
		name     string
		bins     []fakeBin
		wantCode int // 0: Run returns nil
	}{
		{"only python", []fakeBin{{"python", 0o755, "#!/bin/sh\nexit 0\n"}}, 0},
		{"python3 preferred", []fakeBin{{"python3", 0o755, "#!/bin/sh\nexit 0\n"}, {"python", 0o755, "#!/bin/sh\nexit 7\n"}}, 0},
		{"python used when python3 missing", []fakeBin{{"python", 0o755, "#!/bin/sh\nexit 7\n"}}, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakePath(t, tt.bins...)
			err := Run(context.Background(), nil, parser.Task{Name: "py", Lang: "python", Script: "x"}, "", nil)
			if tt.wantCode == 0 {
				if err != nil {
					t.Fatalf("Run: %v, want nil", err)
				}
				return
			}
			wantExitCode(t, err, tt.wantCode)
		})
	}
}

func TestRunScriptSize(t *testing.T) {
	// 4 MiB exceeds the argument limit on Linux (per argument), macOS (total), and
	// Windows; half of argLimit leaves room for the rest of the command line.
	requireCmd(t, "bash")
	tests := []struct {
		name, script string
		wantErr      bool
	}{
		{"one 4 MiB line", strings.Repeat("x", 4<<20), true},
		{"many lines", strings.Repeat("echo x >/dev/null\n", 250_000), true},
		{"under limit", strings.Repeat(":\n", argLimit/4), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Run(context.Background(), nil, parser.Task{Name: "big", Script: tt.script}, "", nil)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Run: %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Run: want error for oversized script, got nil")
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Fatalf("Run: error = %v, want a start error, not an exit status", err)
			}
			if !argListTooLong(err) {
				t.Errorf("Run: error = %v, want it to wrap the OS argument-size error", err)
			}
			for _, w := range []string{"too large", `task "big"`, strconv.Itoa(len(tt.script)) + " bytes"} {
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
			if err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: tc.lang, Script: tc.script}, "", args); err != nil {
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
				if err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: shell, Script: `[ "$0" = made ]`}, "", args); err != nil {
					t.Fatalf("Run: %v, want $0 = made", err)
				}
			})
		}
	}
}

func TestInlineScript(t *testing.T) {
	tests := []struct{ lang, script, want string }{
		{"bash", "-x", " -x"},
		{"bash", "+x", " +x"},
		{"node", "-1", " -1"},
		{"python", "-1", "-1"},
		{"python3", "+1", "+1"},
		{"bash", "echo -x", "echo -x"},
		{"bash", " -x", " -x"},
		{"bash", "", ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%q", tt.lang, tt.script), func(t *testing.T) {
			if got := inlineScript(interpreters[tt.lang], tt.script); got != tt.want {
				t.Errorf("inlineScript(%q) = %q, want %q", tt.script, got, tt.want)
			}
		})
	}
}

func TestRunScriptLeadingDash(t *testing.T) {
	// Each script exits 42 only if it ran as a script rather than being parsed as an
	// interpreter option.
	shellScript := "-x 2>/dev/null || exit 42"
	for _, tc := range []struct {
		name, lang, bin, script string
		want                    int
	}{
		{"plain", "", "bash", shellScript, 42},
		{"bash", "bash", "bash", shellScript, 42},
		{"sh", "sh", "sh", shellScript, 42},
		{"zsh", "zsh", "zsh", shellScript, 42},
		{"bash plus", "bash", "bash", "+x 2>/dev/null || exit 42", 42},
		{"sh plus", "sh", "sh", "+x 2>/dev/null || exit 42", 42},
		{"zsh plus", "zsh", "zsh", "+x 2>/dev/null || exit 42", 42},
		{"node plus", "node", "node", "+process.exit(42)", 42},
		{"fish", "fish", "fish", "-x 2>/dev/null; or exit 42", 42},
		{"node", "node", "node", "-process.exit(42)", 42},
		{"ruby", "ruby", "ruby", "-1; exit 42", 42},
		{"perl", "perl", "perl", "-1; exit 42", 42},
		{"python", "python", "python3", "-1\nimport sys; sys.exit(42)", 42},
		// The padding must not shift line numbers. (bash 3.2 always reports $LINENO as 1
		// under -c, so zsh and node check this.)
		{"zsh line numbers", "zsh", "zsh", "-x 2>/dev/null || true\nexit $LINENO", 2},
		{"node line numbers", "node", "node", "-1\nprocess.exit(40 + +new Error().stack.match(/\\[eval\\]:(\\d+)/)[1])", 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireCmd(t, tc.bin)
			err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: tc.lang, Script: tc.script}, "", nil)
			wantExitCode(t, err, tc.want)
		})
	}
}

func TestRunRejectsNUL(t *testing.T) {
	tests := []struct {
		name, lang, script, wantLine string
	}{
		{"line 1", "bash", "echo a\x00b\n", "(line 1 of the script)"},
		{"line 3", "bash", "echo a\necho b\necho \x00\n", "(line 3 of the script)"},
		{"python", "python", "print('a')\n\x00\n", "(line 2 of the script)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Run(context.Background(), nil, parser.Task{Name: "nul", Lang: tt.lang, Script: tt.script}, "", nil)
			if err == nil {
				t.Fatal("Run: want error for NUL byte, got nil")
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Fatalf("Run: error = %v, want a refusal before anything runs", err)
			}
			for _, w := range []string{`task "nul"`, "contains a NUL byte", tt.wantLine} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), w)
				}
			}
		})
	}
}

func TestRunForceSkipsGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows kills the interpreter on the first cancel; there is no grace period")
	}
	requireCmd(t, "bash")
	tests := []struct {
		name       string
		force      bool
		minElapsed time.Duration
		maxElapsed time.Duration
	}{
		{name: "force skips grace", force: true, maxElapsed: 2 * time.Second},
		{name: "no force waits grace", minElapsed: killGrace, maxElapsed: killGrace + 5*time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			// The script ignores SIGTERM, so only SIGKILL ends it.
			task := parser.Task{Name: "stubborn", Script: "trap '' TERM\ntouch marker\nsleep 5\n"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var force chan struct{}
			if tt.force {
				force = make(chan struct{})
			}
			errc := make(chan error, 1)
			go func() { errc <- Run(ctx, force, task, dir, nil) }()

			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "marker")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("task never wrote its marker")
				}
				time.Sleep(10 * time.Millisecond)
			}

			begin := time.Now()
			cancel()
			if tt.force {
				time.Sleep(100 * time.Millisecond)
				close(force)
			}
			select {
			case <-errc:
			case <-time.After(tt.maxElapsed):
				t.Fatalf("Run did not return within %v of cancel", tt.maxElapsed)
			}
			if elapsed := time.Since(begin); elapsed < tt.minElapsed {
				t.Errorf("Run returned after %v, want at least %v", elapsed, tt.minElapsed)
			}
		})
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
			if err := Run(context.Background(), nil, parser.Task{Name: "x", Lang: tc.lang, Script: tc.script}, "", nil); err != nil {
				t.Fatalf("Run: %v, want no arguments", err)
			}
		})
	}
}
