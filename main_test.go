package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runTasksMD defines tasks for the run tests. The check tasks exit non-zero unless they
// received exactly the expected positional parameters, so a test can verify argument
// passing from run's exit code alone (the task's own output goes to the real stdout).
const runTasksMD = "# Tasks\n\n" +
	"### hello\nSay hello.\n```bash\necho hello\n```\n\n" +
	"### check\n```bash\n[ \"$#\" -eq 3 ] && [ \"$1\" = -x ] && [ \"$2\" = -- ] && [ \"$3\" = y ]\n```\n\n" +
	"### checkdash\n```bash\n[ \"$#\" -eq 1 ] && [ \"$1\" = -x ]\n```\n"

func writeRunTasks(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(runTasksMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runCase is one call to run and what it should produce. The want/not fields are
// substrings of the captured output.
type runCase struct {
	name       string
	args       []string
	wantCode   int
	wantStdout []string
	wantStderr []string
	notStdout  []string
	notStderr  []string
}

func checkRunCases(t *testing.T, cases []runCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d\nstdout: %s\nstderr: %s", tt.args, code, tt.wantCode, &stdout, &stderr)
			}
			checkContains(t, "stdout", stdout.String(), tt.wantStdout, true)
			checkContains(t, "stderr", stderr.String(), tt.wantStderr, true)
			checkContains(t, "stdout", stdout.String(), tt.notStdout, false)
			checkContains(t, "stderr", stderr.String(), tt.notStderr, false)
		})
	}
}

// checkContains reports each substring of subs whose presence in got differs from want.
func checkContains(t *testing.T, label, got string, subs []string, want bool) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(got, s) != want {
			if want {
				t.Errorf("%s = %q, want it to contain %q", label, got, s)
			} else {
				t.Errorf("%s = %q, want it not to contain %q", label, got, s)
			}
		}
	}
}

// requireCmd skips the test when name is not on PATH.
func requireCmd(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found on PATH; skipping", name)
	}
}

// TestRun covers the paths that never start a task process.
func TestRun(t *testing.T) {
	f := writeRunTasks(t)
	missing := filepath.Join(t.TempDir(), "nope.md")

	checkRunCases(t, []runCase{
		{name: "version", args: []string{"-V"}, wantStdout: []string{"made "}},
		{name: "list when no task", args: []string{"-f", f}, wantStdout: []string{"Tasks in " + f, "hello", "Say hello."}},
		{name: "list flag wins over task", args: []string{"-f", f, "-l", "check"}, wantStdout: []string{"Tasks in", "checkdash"}},
		{name: "unknown task", args: []string{"-f", f, "nope"}, wantCode: 1, wantStdout: []string{"Tasks in"}, wantStderr: []string{`unknown task "nope"`}},
		{name: "bad flag", args: []string{"-bogus"}, wantCode: 2, wantStderr: []string{"flag provided but not defined: -bogus", "Usage: made", "-no-chdir"}},
		{name: "help", args: []string{"-h"}, wantStderr: []string{"Usage: made", "-no-chdir", "-file"}},
		{name: "missing file", args: []string{"-f", missing}, wantCode: 1, wantStderr: []string{"cannot read " + missing}},
	})
}

// TestRunTaskArgs runs real bash tasks to check what reaches the script. notStderr
// rules out a runner error (for example a missing interpreter), which also exits 1.
func TestRunTaskArgs(t *testing.T) {
	requireCmd(t, "bash")
	f := writeRunTasks(t)
	noRunnerErr := []string{"made: "}

	checkRunCases(t, []runCase{
		{name: "args after task are not parsed by made", args: []string{"-f", f, "check", "-x", "--", "y"}, notStdout: []string{"Tasks in"}, notStderr: noRunnerErr},
		{name: "leading -- is dropped", args: []string{"-f", f, "checkdash", "--", "-x"}, notStdout: []string{"Tasks in"}, notStderr: noRunnerErr},
		{name: "wrong args fail the task", args: []string{"-f", f, "checkdash", "-y"}, wantCode: 1, notStderr: noRunnerErr},
	})
}

// TestRunIsReentrant guards against a return to package-global flag state, which
// panics with "flag redefined" on the second call.
func TestRunIsReentrant(t *testing.T) {
	f := writeRunTasks(t)
	for i := range 2 {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-f", f}, &stdout, &stderr); code != 0 {
			t.Fatalf("call %d: run = %d, want 0 (stderr: %s)", i+1, code, &stderr)
		}
		if !strings.Contains(stdout.String(), "hello") {
			t.Errorf("call %d: stdout = %q, want task list", i+1, &stdout)
		}
	}
}

func TestResolveVersionUsesLdflagsVar(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })

	version = "v9.9.9"
	if got := resolveVersion(); got != "v9.9.9" {
		t.Errorf("resolveVersion() = %q, want %q", got, "v9.9.9")
	}

	// With no injected version, the resolver still returns a non-empty fallback.
	version = ""
	if got := resolveVersion(); got == "" {
		t.Error("resolveVersion() = empty, want a non-empty fallback")
	}
}

func TestFindUp(t *testing.T) {
	root := filepath.FromSlash("/r")
	at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	readme := func(dir string) string { return filepath.Join(dir, defaultFile) }

	tests := []struct {
		name   string
		files  []string // paths for which isFile reports true
		start  string
		want   string
		wantOK bool
	}{
		{"in start", []string{readme(at("a"))}, at("a"), at("a"), true},
		{"in ancestor", []string{readme(root)}, at("a", "b"), root, true},
		{"nearest wins", []string{readme(root), readme(at("a"))}, at("a", "b"), at("a"), true},
		{"not found", nil, at("a", "b"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := map[string]bool{}
			for _, f := range tt.files {
				set[f] = true
			}
			got, ok := findUp(tt.start, func(p string) bool { return set[p] })
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("findUp(%q) = (%q, %v), want (%q, %v)", tt.start, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestIsRegularFileSkipsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, defaultFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if isRegularFile(filepath.Join(dir, defaultFile)) {
		t.Error("isRegularFile(directory) = true, want false")
	}
}

func TestResolveFileExplicitIsUnchanged(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := resolveFile("some/tasks.md")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got != "some/tasks.md" {
		t.Errorf("resolveFile = %q, want %q", got, "some/tasks.md")
	}
}

func TestResolveFileInCwdKeepsShortName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, defaultFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := resolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got != defaultFile {
		t.Errorf("resolveFile = %q, want %q", got, defaultFile)
	}
}
