package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/lint"
	"github.com/Tarektouati/made/internal/parser"
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
		{name: "list flag ignores unknown task", args: []string{"-f", f, "-l", "nope"}, wantStdout: []string{"Tasks in"}, notStderr: []string{"unknown task"}},
		{name: "list flag does not run task", args: []string{"-f", f, "-l", "hello"}, wantStdout: []string{"Tasks in", "Say hello."}},
		{name: "unknown task", args: []string{"-f", f, "nope"}, wantCode: 1, wantStdout: []string{"Tasks in"}, wantStderr: []string{`unknown task "nope"`}},
		{name: "bad flag", args: []string{"-bogus"}, wantCode: 2, wantStderr: []string{"flag provided but not defined: -bogus", "Usage: made", "--no-chdir"}, notStdout: []string{"Usage"}},
		{name: "help", args: []string{"-h"}, wantStdout: []string{"Usage: made", "--no-chdir", "--file"}, notStderr: []string{"Usage"}},
		{name: "missing file", args: []string{"-f", missing}, wantCode: 1, wantStderr: []string{"cannot read " + missing}},
		// The package dir has a Madefile.md, so falling back to discovery would list it.
		{name: "empty -f", args: []string{"-f", ""}, wantCode: 2, wantStderr: []string{"-f requires a file path"}, notStdout: []string{"Tasks in"}},
		{name: "empty --file", args: []string{"--file", ""}, wantCode: 2, wantStderr: []string{"-f requires a file path"}, notStdout: []string{"Tasks in"}},
		{name: "empty -f=", args: []string{"-f="}, wantCode: 2, wantStderr: []string{"-f requires a file path"}, notStdout: []string{"Tasks in"}},
		{name: "empty -f with task", args: []string{"-f", "", "hello"}, wantCode: 2, wantStderr: []string{"-f requires a file path"}, notStdout: []string{"Tasks in"}},
		{name: "empty -f with -l", args: []string{"-f", "", "-l"}, wantCode: 2, wantStderr: []string{"-f requires a file path"}, notStdout: []string{"Tasks in"}},
	})
}

// TestUsage checks the help text: on stdout for -h, in the README's synopsis, with
// paired short and long names and no hidden or single-dash long flags.
func TestUsage(t *testing.T) {
	f := writeRunTasks(t)
	wantHelp := []string{
		"Usage: made [-f file] [--no-chdir] [-l] [-V] [task [--] [args...]]",
		"made [-f file] --check [--strict]",
		"made init [--force]",
		"made completion <bash|zsh|fish>",
		"-f, --file <file>",
		"    --no-chdir",
		"    --check",
		"    --strict",
		"-l, --list",
		"-V, --version",
		"-h, --help",
	}
	notHelp := []string{"list-names", "\n  -file", "\n  -list", "\n  -version", "\n  -no-chdir"}

	var cases []runCase
	for _, arg := range []string{"-h", "--help", "-help"} {
		cases = append(cases, runCase{name: arg, args: []string{arg}, wantStdout: wantHelp, notStdout: notHelp, notStderr: []string{"Usage"}})
	}
	cases = append(cases,
		runCase{name: "single-dash -file", args: []string{"-file", f}, wantStdout: []string{"Tasks in " + f}},
		runCase{name: "single-dash -version", args: []string{"-version"}, wantStdout: []string{"made "}},
		runCase{name: "init --help", args: []string{"init", "--help"}, wantStdout: []string{"Usage: made init [--force]", "    --force", "-h, --help"}, notStdout: []string{"\n  -force"}, notStderr: []string{"Usage"}},
		runCase{name: "init bad flag", args: []string{"init", "--bogus"}, wantCode: 2, wantStderr: []string{"Usage: made init [--force]"}, notStdout: []string{"Usage"}},
	)
	checkRunCases(t, cases)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("run(--help) = %d, stderr %q; want 0 and empty", code, &stderr)
	}
	_, opts, ok := strings.Cut(stdout.String(), "Options:")
	if !ok {
		t.Fatalf("usage has no Options: section\n%s", &stdout)
	}
	prev := -1
	for _, name := range []string{"--file", "--no-chdir", "--check", "--strict", "--list", "--version", "--help"} {
		i := strings.Index(opts, name)
		if i <= prev {
			t.Errorf("%s at %d in the options list, want it after the previous flag (at %d)\n%s", name, i, prev, opts)
		}
		prev = i
	}
}

func writeDeepNestMadefile(t *testing.T, marker string, depth int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deep.md")
	if err := os.WriteFile(path, []byte(strings.Repeat(marker, depth)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunRejectsDeepNesting(t *testing.T) {
	f := writeDeepNestMadefile(t, "> ", 257)
	checkRunCases(t, []runCase{
		{
			name:       "list",
			args:       []string{"-f", f, "-l"},
			wantCode:   1,
			wantStderr: []string{"cannot parse", "nesting too deep"},
		},
		{
			name:       "list-names",
			args:       []string{"-f", f, "--list-names"},
			wantCode:   1,
			wantStderr: []string{"cannot parse", "nesting too deep"},
		},
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

// TestRunInterpreterExitCodes checks that an interpreter start failure exits with the
// shell's codes: 127 when it is not on PATH, 126 when it is there but not executable.
func TestRunInterpreterExitCodes(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tasks.md")
	content := "### py\n```python\nprint('hi')\n```\n\n### plain\n```\necho hi\n```\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("not found", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		checkRunCases(t, []runCase{
			{name: "python", args: []string{"-f", f, "py"}, wantCode: 127, wantStderr: []string{`made: neither "python3" nor "python" was found on PATH`}, notStderr: []string{"executable file not found"}},
			{name: "plain fence", args: []string{"-f", f, "plain"}, wantCode: 127, wantStderr: []string{`made: "bash" was not found on PATH`}, notStderr: []string{"executable file not found"}},
		})
	})

	t.Run("not executable", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("needs Unix permission bits")
		}
		dir := t.TempDir()
		bash := filepath.Join(dir, "bash")
		if err := os.WriteFile(bash, []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		checkRunCases(t, []runCase{
			{name: "plain fence", args: []string{"-f", f, "plain"}, wantCode: 126, wantStderr: []string{`"bash" at ` + bash + " is not executable"}},
		})
	})
}

// TestRunDashTaskName checks that a task whose name starts with "-" runs only after "--".
func TestRunDashTaskName(t *testing.T) {
	requireCmd(t, "bash")
	f := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(f, []byte("### -x\n```bash\nexit 0\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkRunCases(t, []runCase{
		{name: "after --", args: []string{"-f", f, "--", "-x"}, notStdout: []string{"Tasks in"}, notStderr: []string{"made: "}},
		{name: "taken as a flag", args: []string{"-f", f, "-x"}, wantCode: 2, wantStderr: []string{"flag provided but not defined: -x"}},
	})
}

func TestWatchSignals(t *testing.T) {
	tests := []struct {
		signals    int
		wantCancel bool
		wantForce  bool
	}{
		{signals: 0},
		{signals: 1, wantCancel: true},
		{signals: 2, wantCancel: true, wantForce: true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("signals=%d", tt.signals), func(t *testing.T) {
			sigs := make(chan os.Signal, 2)
			ctx, force, stop, received := watchSignals(sigs)
			defer stop()
			for range tt.signals {
				sigs <- os.Interrupt
			}
			closed := func(c <-chan struct{}) bool {
				select {
				case <-c:
					return true
				case <-time.After(200 * time.Millisecond):
					return false
				}
			}
			if got := closed(ctx.Done()); got != tt.wantCancel {
				t.Errorf("ctx cancelled = %v, want %v", got, tt.wantCancel)
			}
			if got := closed(force); got != tt.wantForce {
				t.Errorf("force closed = %v, want %v", got, tt.wantForce)
			}
			if got, want := received() != nil, tt.wantCancel; got != want {
				t.Errorf("received() = %v, want a signal: %v", received(), want)
			}
		})
	}
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

	tests := []struct {
		name    string
		ldflags string
		want    string
	}{
		{name: "tagged", ldflags: "v9.9.9", want: "v9.9.9"},
		{name: "goreleaser strips v", ldflags: "0.4.0", want: "v0.4.0"},
		{name: "goreleaser snapshot", ldflags: "0.4.0-SNAPSHOT-fd1991f", want: "v0.4.0-SNAPSHOT-fd1991f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version = tt.ldflags
			if got := resolveVersion(); got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}

	// With no injected version, the resolver still returns a non-empty fallback.
	version = ""
	if got := resolveVersion(); got == "" {
		t.Error("resolveVersion() = empty, want a non-empty fallback")
	}
}

func TestNormalizeVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "release without v", in: "0.4.0", want: "v0.4.0"},
		{name: "snapshot without v", in: "0.4.0-SNAPSHOT-fd1991f", want: "v0.4.0-SNAPSHOT-fd1991f"},
		{name: "release with v", in: "v0.4.0", want: "v0.4.0"},
		{name: "pseudo-version", in: "v0.0.0-20261116000000-abcdef123456", want: "v0.0.0-20261116000000-abcdef123456"},
		{name: "dev", in: "dev", want: "dev"},
		{name: "empty", in: "", want: ""},
		{name: "custom", in: "custom-build", want: "custom-build"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeVersion(tt.in); got != tt.want {
				t.Errorf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// mkTree creates root/<path> for each path: a trailing "/" makes a directory,
// anything else an empty file (parent directories are created as needed).
func mkTree(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// requireCaseSensitiveFS skips the test when dir's filesystem folds case, where
// Madefile.md and madefile.md cannot coexist.
func requireCaseSensitiveFS(t *testing.T, dir string) {
	t.Helper()
	mkTree(t, dir, "case-probe")
	if _, err := os.Stat(filepath.Join(dir, "CASE-PROBE")); err == nil {
		t.Skip("case-insensitive filesystem; skipping")
	}
}

func TestFindMadefile(t *testing.T) {
	// Every tree has .git at its root so the walk never leaves the temp dir.
	tests := []struct {
		name        string
		paths       []string
		start       string // relative to the temp root
		wantDir     string // relative to the temp root
		wantName    string
		wantOK      bool
		wantSkipped []string // relative to the temp root
	}{
		{"in start", []string{".git/", "Madefile.md"}, ".", ".", "Madefile.md", true, nil},
		{"in ancestor", []string{".git/", "Madefile.md", "a/b/"}, "a/b", ".", "Madefile.md", true, nil},
		{"nearest wins", []string{".git/", "Madefile.md", "a/Madefile.md", "a/b/"}, "a/b", "a", "Madefile.md", true, nil},
		{"lowercase name", []string{".git/", "a/madefile.md"}, "a", "a", "madefile.md", true, nil},
		{"uppercase name", []string{".git/", "a/MADEFILE.md"}, "a", "a", "MADEFILE.md", true, nil},
		{"other case is not a match", []string{".git/", "MadeFile.md"}, ".", "", "", false, nil},
		{"README is ignored", []string{".git/", "README.md"}, ".", "", "", false, nil},
		{"directory named Madefile.md is skipped", []string{".git/", "Madefile.md", "a/Madefile.md/"}, "a", ".", "Madefile.md", true, []string{"a/Madefile.md"}},
		{"stops at .git directory", []string{"Madefile.md", "repo/.git/", "repo/sub/"}, "repo/sub", "", "", false, nil},
		{"stops at .git file", []string{"Madefile.md", "repo/.git", "repo/sub/"}, "repo/sub", "", "", false, nil},
		{"boundary dir is checked", []string{"Madefile.md", "repo/.git/", "repo/Madefile.md", "repo/sub/"}, "repo/sub", "repo", "Madefile.md", true, nil},
		{"not found", []string{".git/", "a/b/"}, "a/b", "", "", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			mkTree(t, root, tt.paths...)
			dir, name, others, skipped, ok := findMadefile(filepath.Join(root, filepath.FromSlash(tt.start)))
			wantDir := ""
			if tt.wantOK {
				wantDir = filepath.Join(root, filepath.FromSlash(tt.wantDir))
			}
			if dir != wantDir || name != tt.wantName || ok != tt.wantOK || len(others) != 0 {
				t.Errorf("findMadefile = (%q, %q, %q, %v), want (%q, %q, [], %v)",
					dir, name, others, ok, wantDir, tt.wantName, tt.wantOK)
			}
			var gotSkipped, wantSkipped []string
			for _, s := range skipped {
				gotSkipped = append(gotSkipped, s.path)
			}
			for _, p := range tt.wantSkipped {
				wantSkipped = append(wantSkipped, filepath.Join(root, filepath.FromSlash(p)))
			}
			if !slices.Equal(gotSkipped, wantSkipped) {
				t.Errorf("findMadefile skipped = %q, want %q", gotSkipped, wantSkipped)
			}
		})
	}
}

func TestFindMadefileSkipsUnusable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks and FIFOs need Unix")
	}
	tests := []struct {
		name       string
		rootFile   bool                         // root holds a valid Madefile.md
		bad        func(t *testing.T, a string) // makes a/Madefile.md unusable
		wantPath   string                       // the skipped path, relative to a
		wantReason string
	}{
		{"symlink loop", true, func(t *testing.T, a string) {
			mkSymlink(t, "Madefile.md", filepath.Join(a, "Madefile.md"))
		}, "Madefile.md", "too many levels"},
		{"broken symlink", true, func(t *testing.T, a string) {
			mkSymlink(t, "missing.md", filepath.Join(a, "Madefile.md"))
		}, "Madefile.md", "broken symlink to missing.md"},
		{"unsearchable directory", true, func(t *testing.T, a string) {
			mkTree(t, a, "Madefile.md")
			chmodDir(t, a, 0o644)
		}, "Madefile.md", "permission denied"},
		{"unlistable directory", true, func(t *testing.T, a string) {
			mkTree(t, a, "Madefile.md")
			chmodDir(t, a, 0o311)
		}, ".", "permission denied"},
		{"fifo", true, func(t *testing.T, a string) {
			mkFIFO(t, filepath.Join(a, "Madefile.md"))
		}, "Madefile.md", "not a regular file"},
		{"no fallback", false, func(t *testing.T, a string) {
			mkSymlink(t, "missing.md", filepath.Join(a, "Madefile.md"))
		}, "Madefile.md", "broken symlink to missing.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			mkTree(t, root, ".git/", "a/")
			if tt.rootFile {
				mkTree(t, root, "Madefile.md")
			}
			a := filepath.Join(root, "a")
			tt.bad(t, a)
			dir, name, _, skipped, ok := findMadefile(a)
			if tt.rootFile && (!ok || dir != root || name != "Madefile.md") {
				t.Errorf("findMadefile = (%q, %q, %v), want (%q, %q, true)", dir, name, ok, root, "Madefile.md")
			}
			if !tt.rootFile && ok {
				t.Errorf("findMadefile = (%q, %q, true), want not found", dir, name)
			}
			wantPath := filepath.Join(a, tt.wantPath)
			if len(skipped) != 1 || skipped[0].path != wantPath || !strings.Contains(skipped[0].err.Error(), tt.wantReason) {
				t.Errorf("findMadefile skipped = %+v, want one entry for %q containing %q", skipped, wantPath, tt.wantReason)
			}
		})
	}
}

// chmodDir sets dir's mode for the test and restores 0755 afterwards so t.TempDir
// can be removed. It skips the test as root, which ignores directory permissions.
func chmodDir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// mkSymlink creates a symlink at link pointing to target.
func mkSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestFindMadefileWalksToRoot checks that without a .git the walk goes past the
// start's parents. It cannot assert "not found" without controlling "/".
func TestFindMadefileWalksToRoot(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, "Madefile.md", "a/b/c/")
	dir, name, _, _, ok := findMadefile(filepath.Join(root, "a", "b", "c"))
	if !ok || dir != root || name != "Madefile.md" {
		t.Errorf("findMadefile = (%q, %q, %v), want (%q, %q, true)", dir, name, ok, root, "Madefile.md")
	}
}

func TestFindMadefilePriority(t *testing.T) {
	tests := []struct {
		name       string
		paths      []string
		wantName   string
		wantOthers []string
	}{
		{"all three", []string{"Madefile.md", "madefile.md", "MADEFILE.md"}, "Madefile.md", []string{"madefile.md", "MADEFILE.md"}},
		{"lower beats upper", []string{"madefile.md", "MADEFILE.md"}, "madefile.md", []string{"MADEFILE.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			requireCaseSensitiveFS(t, root)
			mkTree(t, root, append([]string{".git/"}, tt.paths...)...)
			_, name, others, _, ok := findMadefile(root)
			if !ok || name != tt.wantName || strings.Join(others, ",") != strings.Join(tt.wantOthers, ",") {
				t.Errorf("findMadefile = (%q, %q, %v), want (%q, %q, true)", name, others, ok, tt.wantName, tt.wantOthers)
			}
		})
	}
}

func TestIsRegularFile(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular.md")
	if err := os.WriteFile(regular, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isRegularFile(regular) {
		t.Error("isRegularFile(regular file) = false, want true")
	}

	dirPath := filepath.Join(dir, madefileNames[0])
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if isRegularFile(dirPath) {
		t.Error("isRegularFile(directory) = true, want false")
	}

	if runtime.GOOS != "windows" {
		fifo := filepath.Join(dir, "pipe.md")
		mkFIFO(t, fifo)
		if isRegularFile(fifo) {
			t.Error("isRegularFile(FIFO) = true, want false")
		}
	}
}

func TestRunSkipsFIFOForDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are not supported on Windows")
	}
	t.Run("fifo only", func(t *testing.T) {
		dir := t.TempDir()
		mkTree(t, dir, ".git/")
		mkFIFO(t, filepath.Join(dir, "Madefile.md"))
		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		code := run([]string{"-l"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("run(-l) = %d, want 1", code)
		}
		checkContains(t, "stderr", stderr.String(), []string{"made: skipping ", "no Madefile.md found"}, true)
	})
	t.Run("fifo in subdir parent wins", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Tasks\n\n### hello\n```bash\necho hi\n```\n"
		if err := os.WriteFile(filepath.Join(dir, "Madefile.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		mkTree(t, dir, ".git/", "sub/nested/")
		mkFIFO(t, filepath.Join(dir, "sub", "Madefile.md"))
		t.Chdir(filepath.Join(dir, "sub", "nested"))
		var stdout, stderr bytes.Buffer
		code := run([]string{"-l"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run(-l) = %d, want 0 (stderr: %s)", code, stderr.String())
		}
		checkContains(t, "stdout", stdout.String(), []string{"hello"}, true)
		checkContains(t, "stderr", stderr.String(), []string{"made: skipping "}, true)
	})
}

func TestRunWarnsOnUnusableMadefile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need Unix")
	}
	tests := []struct {
		name     string
		parent   bool // a valid Madefile.md one level up
		wantCode int
		wantErr  []string
	}{
		{"only candidate", false, 1, []string{"made: skipping ", "no Madefile.md found"}},
		{"parent used", true, 0, []string{"made: skipping ", "made: using "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mkTree(t, dir, ".git/", "sub/")
			if tt.parent {
				content := "# Tasks\n\n### hello\n```bash\necho hi\n```\n"
				if err := os.WriteFile(filepath.Join(dir, "Madefile.md"), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			mkSymlink(t, "missing.md", filepath.Join(dir, "sub", "Madefile.md"))
			t.Chdir(filepath.Join(dir, "sub"))
			var stdout, stderr bytes.Buffer
			code := run([]string{"hello"}, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run(hello) = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			checkContains(t, "stderr", stderr.String(), tt.wantErr, true)
		})
	}
}

func TestRunRejectsNonRegularExplicitFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are not supported on Windows")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "tasks.md")
	mkFIFO(t, fifo)
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-f", fifo, "-l"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("run(-f fifo -l) = %d, want 1", code)
	}
	checkContains(t, "stderr", stderr.String(), []string{"not a regular file"}, true)
}

func TestReadMadefileRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.md")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxMadefileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = readMadefile(path)
	if err == nil {
		t.Fatal("readMadefile(oversize) = nil, want error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("readMadefile error = %q, want mentions maximum size", err)
	}
}

// writeTemp writes content to name in a new temporary directory and returns its path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheck(t *testing.T) {
	clean := writeRunTasks(t)
	bad := writeTemp(t, "bad.md", "### a\n```yaml\nk\n```\n")
	warn := writeTemp(t, "warn.md", "### a\n```bash\nx\n```\n```bash\ny\n```\n")
	docs := writeTemp(t, "docs.md", "### a\n```bash\nx\n```\n## Options\n### `--verbose`\nPrints more.\n")
	checkRunCases(t, []runCase{
		{name: "clean", args: []string{"-f", clean, "--check"}, notStdout: []string{clean}, notStderr: []string{"made: "}},
		{name: "clean strict", args: []string{"--check", "--strict", "-f", clean}},
		{name: "no-chdir allowed", args: []string{"--no-chdir", "--check", "-f", clean}},
		{name: "errors", args: []string{"--check", "-f", bad}, wantCode: 1, wantStdout: []string{bad + ":2: error: unsupported-language: ", "1 problem (1 error, 0 warnings)"}},
		{name: "warnings only", args: []string{"--check", "-f", warn}, wantStdout: []string{warn + ":5: warning: extra-code-block: "}},
		{name: "fence-less docs heading", args: []string{"--check", "-f", docs}, wantStdout: []string{docs + ":6: warning: no-code-block: ", "(0 errors, 1 warning)"}, notStdout: []string{"empty-name"}},
		{name: "warnings strict", args: []string{"--check", "--strict", "-f", warn}, wantCode: 1, wantStdout: []string{"1 problem (0 errors, 1 warning)"}},
		{name: "missing file", args: []string{"--check", "-f", filepath.Join(t.TempDir(), "none.md")}, wantCode: 1, wantStderr: []string{"made: cannot read "}},
		{name: "strict alone", args: []string{"--strict", "-f", clean}, wantCode: 2, wantStderr: []string{"made: --strict requires --check"}},
		{name: "task name", args: []string{"--check", "-f", clean, "hello"}, wantCode: 2, wantStderr: []string{"made: --check takes no task name"}},
		{name: "init", args: []string{"--check", "init"}, wantCode: 2, wantStderr: []string{"made: --check takes no task name"}},
		{name: "completion", args: []string{"--check", "completion", "bash"}, wantCode: 2, wantStderr: []string{"made: --check takes no task name"}},
		{name: "list", args: []string{"--check", "-l", "-f", clean}, wantCode: 2, wantStderr: []string{"made: --check does not take -l"}},
		{name: "version", args: []string{"-V", "--check"}, wantCode: 2, wantStderr: []string{"made: --check does not take -V"}},
		{name: "list-names", args: []string{"--check", "--list-names", "-f", clean}, wantCode: 2, wantStderr: []string{"made: --check does not take --list-names"}},
	})
}

func TestCheckExactOutput(t *testing.T) {
	f := writeTemp(t, "tasks.md", "### a\n```bash\nx\n```\n### a\n```bash\n```\n### b\nprose\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--check", "-f", f}, &stdout, &stderr)
	want := f + `:5: error: duplicate-task: "a" already defined at line 1` + "\n" +
		f + `:6: error: empty-script: task "a" has an empty code block` + "\n" +
		f + `:8: warning: no-code-block: heading "b" has no code block, so it is not a task` + "\n" +
		"3 problems (2 errors, 1 warning)\n"
	if code != 1 || stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("run = %d\nstdout:\n%s\nwant:\n%s\nstderr: %q", code, &stdout, want, &stderr)
	}
}

func TestCheckFileTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.md")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxMadefileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	checkRunCases(t, []runCase{{
		name: "too large", args: []string{"--check", "-f", path}, wantCode: 1,
		wantStdout: []string{path + ": error: file-too-large: ", "1 problem (1 error, 0 warnings)"},
	}})
}

func TestResolveFileExplicitIsUnchanged(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := resolveFile("some/tasks.md")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got.path != "some/tasks.md" || got.fromParent || len(got.others) != 0 {
		t.Errorf("resolveFile = %+v, want path %q only", got, "some/tasks.md")
	}
}

func TestResolveFileInCwdKeepsShortName(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, ".git/", "Madefile.md")
	t.Chdir(dir)
	got, err := resolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got.path != "Madefile.md" || got.fromParent {
		t.Errorf("resolveFile = %+v, want path %q, fromParent false", got, "Madefile.md")
	}
}

func TestResolveFileFromParent(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, ".git/", "Madefile.md", "a/")
	t.Chdir(filepath.Join(dir, "a"))
	got, err := resolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	// t.TempDir may sit behind a symlink (/var -> /private/var on macOS) that
	// resolveFile resolves.
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(physical, "Madefile.md")
	if got.path != want || !got.fromParent {
		t.Errorf("resolveFile = %+v, want path %q, fromParent true", got, want)
	}
}

func TestResolveFileThroughSymlinkedCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need Unix")
	}
	tests := []struct {
		name           string
		target         string // of sd/link, relative to sd
		wantFromParent bool
	}{
		{"link below the Madefile", "real/deep", true},
		{"link to the Madefile's directory", "real", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			mkTree(t, root, ".git/", "sd/real/Madefile.md", "sd/real/deep/")
			link := filepath.Join(root, "sd", "link")
			mkSymlink(t, filepath.FromSlash(tt.target), link)
			t.Chdir(link) // sets $PWD to the logical path, like a shell's cd
			got, err := resolveFile("")
			if err != nil {
				t.Fatalf("resolveFile: %v", err)
			}
			want := "Madefile.md"
			if tt.wantFromParent {
				physical, err := filepath.EvalSymlinks(filepath.Join(root, "sd", "real"))
				if err != nil {
					t.Fatal(err)
				}
				want = filepath.Join(physical, "Madefile.md")
			}
			if got.path != want || got.fromParent != tt.wantFromParent {
				t.Errorf("resolveFile = %+v, want path %q, fromParent %v", got, want, tt.wantFromParent)
			}
		})
	}
}

func TestResolveFileDuplicateOthers(t *testing.T) {
	dir := t.TempDir()
	requireCaseSensitiveFS(t, dir)
	mkTree(t, dir, ".git/", "Madefile.md", "madefile.md")
	t.Chdir(dir)
	got, err := resolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got.path != "Madefile.md" || strings.Join(got.others, ",") != "madefile.md" {
		t.Errorf("resolveFile = %+v, want path %q, others [madefile.md]", got, "Madefile.md")
	}
}

func TestResolveFileNotFound(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, ".git/", "README.md")
	t.Chdir(dir)
	_, err := resolveFile("")
	if err == nil {
		t.Fatal("resolveFile: want error, got nil")
	}
	physical, err2 := filepath.EvalSymlinks(dir)
	if err2 != nil {
		t.Fatal(err2)
	}
	for _, want := range []string{"no Madefile.md found in " + physical + " or its parents", "use -f", "made init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
}

// TestResolveFileNotFoundThroughSymlinkedCwd checks the error names the directory the
// search started from, not the symlink the shell cd'd through: the Madefile next to
// the link must not be found, and the message must not point there.
func TestResolveFileNotFoundThroughSymlinkedCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need Unix")
	}
	root := t.TempDir()
	mkTree(t, root, "sd/Madefile.md", "other/.git/", "other/real/deep/")
	link := filepath.Join(root, "sd", "link")
	mkSymlink(t, filepath.Join("..", "other", "real", "deep"), link)
	t.Chdir(link)
	_, err := resolveFile("")
	if err == nil {
		t.Fatal("resolveFile: want error, got nil")
	}
	physical, err2 := filepath.EvalSymlinks(filepath.Join(root, "other", "real", "deep"))
	if err2 != nil {
		t.Fatal(err2)
	}
	want := "no Madefile.md found in " + physical + " or its parents"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err, want)
	}
}

func TestInitCreatesMadefile(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "1.2.3" // as goreleaser injects it: no leading "v"
	dir := t.TempDir()
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got := stdout.String(); got != "made: created Madefile.md\n" {
		t.Errorf("stdout = %q, want %q", got, "made: created Madefile.md\n")
	}
	got, err := os.ReadFile(filepath.Join(dir, "Madefile.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "<!-- made v1.2.3 -->\n\n" + madefileTemplate; string(got) != want {
		t.Errorf("Madefile.md = %q, want %q", got, want)
	}
}

// TestInitTemplateIsValid checks the init template, with and without the version
// note, and that both parse to exactly the example task.
func TestInitTemplateIsValid(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{version: "dev", want: madefileTemplate},
		{version: "v1.2.3", want: "<!-- made v1.2.3 -->\n\n" + madefileTemplate},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := initTemplate(tt.version)
			if got != tt.want {
				t.Fatalf("initTemplate(%q) = %q, want %q", tt.version, got, tt.want)
			}
			tasks, err := parser.Parse([]byte(got))
			if err != nil {
				t.Fatalf("parser.Parse(template): %v", err)
			}
			if len(tasks) != 1 || tasks[0].Name != "hello" ||
				tasks[0].Description != "Prints a friendly greeting." || tasks[0].Script == "" {
				t.Errorf("template tasks = %+v, want one task %q with its description and a script", tasks, "hello")
			}
			if diags := lint.Check([]byte(got)); diags != nil {
				t.Errorf("lint.Check(template) = %+v, want no findings", diags)
			}
		})
	}
}

func TestInitRefusesExisting(t *testing.T) {
	for _, name := range madefileNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if name != madefileNames[0] {
				// Probe a sibling dir: the probe file would break the entry count below.
				requireCaseSensitiveFS(t, t.TempDir())
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"init"}, &stdout, &stderr); code != 1 {
				t.Errorf("run(init) = %d, want 1", code)
			}
			checkContains(t, "stderr", stderr.String(), []string{name + " already exists", "made init --force"}, true)
			if got, _ := os.ReadFile(path); string(got) != "keep me" {
				t.Errorf("%s = %q, want it untouched", name, got)
			}
			if name != madefileNames[0] {
				if _, err := os.Stat(filepath.Join(dir, madefileNames[0])); err == nil {
					t.Errorf("%s was created next to %s", madefileNames[0], name)
				}
			}
		})
	}
}

// TestInitIgnoresParentMadefile checks init creates a nested Madefile rather than
// refusing because of one higher up.
func TestInitIgnoresParentMadefile(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, ".git/", "Madefile.md", "sub/")
	t.Chdir(filepath.Join(dir, "sub"))
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "Madefile.md")); err != nil {
		t.Errorf("sub/Madefile.md not created: %v", err)
	}
}

func TestInitUsageErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	checkRunCases(t, []runCase{
		{name: "with -f", args: []string{"-f", "tasks.md", "init"}, wantCode: 2, wantStderr: []string{"init does not take -f"}},
		{name: "with empty -f", args: []string{"-f", "", "init"}, wantCode: 2, wantStderr: []string{"-f requires a file path"}},
		{name: "extra args", args: []string{"init", "extra"}, wantCode: 2, wantStderr: []string{"init takes no arguments"}},
		{name: "force with extra args", args: []string{"init", "--force", "extra"}, wantCode: 2, wantStderr: []string{"init takes no arguments"}},
		{name: "unknown init flag", args: []string{"init", "--bogus"}, wantCode: 2, wantStderr: []string{"flag provided but not defined: -bogus", "Usage: made init [--force]"}},
		{name: "-f after init", args: []string{"init", "-f", "x"}, wantCode: 2, wantStderr: []string{"flag provided but not defined: -f"}},
		{name: "init help", args: []string{"init", "-h"}, wantCode: 0, wantStdout: []string{"Usage: made init [--force]", "--force"}},
		{name: "-l before init", args: []string{"-l", "init"}, wantCode: 2, wantStderr: []string{"init does not take -l"}},
		{name: "--list before init", args: []string{"--list", "init"}, wantCode: 2, wantStderr: []string{"init does not take --list"}},
		{name: "--no-chdir before init", args: []string{"--no-chdir", "init"}, wantCode: 2, wantStderr: []string{"init does not take --no-chdir"}},
		{name: "--list-names before init", args: []string{"--list-names", "init"}, wantCode: 2, wantStderr: []string{"init does not take --list-names"}},
		{name: "-l after init", args: []string{"init", "-l"}, wantCode: 2, wantStderr: []string{"flag provided but not defined: -l"}},
	})
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Errorf("cwd has %d entries after usage errors, want none", len(entries))
	}
}

// TestInitIsReserved checks a user task named init never runs: made init scaffolds
// (or refuses) instead.
func TestInitIsReserved(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	content := "### init\n```bash\ntouch " + marker + "\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "Madefile.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code != 1 {
		t.Errorf("run(init) = %d, want 1", code)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the user's init task ran")
	}
}

func TestInitForceOverwrites(t *testing.T) {
	for _, name := range madefileNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if name != madefileNames[0] {
				// Probe a sibling dir: the probe file would break the entry count below.
				requireCaseSensitiveFS(t, t.TempDir())
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
			}
			if want := "made: overwrote " + name + "\n"; stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}
			if got, _ := os.ReadFile(path); string(got) != initTemplate(resolveVersion()) {
				t.Errorf("%s = %q, want the template", name, got)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("dir has %d entries, want only %s", len(entries), name)
			}
		})
	}
}

func TestInitForceWithoutExisting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got := stdout.String(); got != "made: created Madefile.md\n" {
		t.Errorf("stdout = %q, want %q", got, "made: created Madefile.md\n")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "Madefile.md")); string(got) != initTemplate(resolveVersion()) {
		t.Errorf("Madefile.md = %q, want the template", got)
	}
}

// TestInitForceMultiple checks only the highest-priority Madefile is overwritten and
// the others are named and left alone.
func TestInitForceMultiple(t *testing.T) {
	dir := t.TempDir()
	requireCaseSensitiveFS(t, t.TempDir())
	for _, n := range []string{"madefile.md", "Madefile.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("old "+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got := stdout.String(); got != "made: overwrote Madefile.md\n" {
		t.Errorf("stdout = %q, want %q", got, "made: overwrote Madefile.md\n")
	}
	checkContains(t, "stderr", stderr.String(), []string{"also found madefile.md; left unchanged"}, true)
	if got, _ := os.ReadFile("Madefile.md"); string(got) != initTemplate(resolveVersion()) {
		t.Errorf("Madefile.md = %q, want the template", got)
	}
	if got, _ := os.ReadFile("madefile.md"); string(got) != "old madefile.md" {
		t.Errorf("madefile.md = %q, want it untouched", got)
	}
}

// TestInitForceSkipsDirectory checks that a directory with a Madefile name is not a
// Madefile, like findMadefile: --force overwrites the regular file made reads.
func TestInitForceSkipsDirectory(t *testing.T) {
	dir := t.TempDir()
	requireCaseSensitiveFS(t, t.TempDir())
	mkTree(t, dir, "Madefile.md/")
	if err := os.WriteFile(filepath.Join(dir, "madefile.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got := stdout.String(); got != "made: overwrote madefile.md\n" {
		t.Errorf("stdout = %q, want %q", got, "made: overwrote madefile.md\n")
	}
	if got, _ := os.ReadFile("madefile.md"); string(got) != initTemplate(resolveVersion()) {
		t.Errorf("madefile.md = %q, want the template", got)
	}
	if info, err := os.Stat("Madefile.md"); err != nil || !info.IsDir() {
		t.Errorf("Madefile.md is no longer a directory (err: %v)", err)
	}
}

// initSymlinkMadefileTree mirrors issue #67: Madefile.md symlinks to a file outside repo/.
func initSymlinkMadefileTree(t *testing.T) (repo, target string) {
	t.Helper()
	base := t.TempDir()
	outDir := filepath.Join(base, "outside")
	repo = filepath.Join(base, "repo")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(outDir, "target.txt")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(repo, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(repo, "Madefile.md")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	return repo, target
}

// TestWriteTemplateNoFollowRefusesSymlink checks force open flags do not follow a symlink.
func TestWriteTemplateNoFollowRefusesSymlink(t *testing.T) {
	if initForceNoFollow == 0 {
		t.Skip("O_NOFOLLOW not used on this platform")
	}
	repo, target := initSymlinkMadefileTree(t)
	t.Chdir(repo)
	if err := writeTemplate("Madefile.md", os.O_TRUNC|initForceNoFollow); err == nil {
		t.Fatal("writeTemplate on symlink with O_NOFOLLOW = nil, want error")
	}
	if got, _ := os.ReadFile(target); string(got) != "ORIGINAL" {
		t.Errorf("symlink target = %q, want %q", got, "ORIGINAL")
	}
}

// TestInitForceRefusesSymlink checks made init --force does not follow a symlinked Madefile.
func TestInitForceRefusesSymlink(t *testing.T) {
	repo, target := initSymlinkMadefileTree(t)
	t.Chdir(repo)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--force"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run(init --force) = %d, want 1 (stdout: %q, stderr: %q)", code, &stdout, &stderr)
	}
	checkContains(t, "stderr", stderr.String(), []string{"Madefile.md", "symlink", "refusing"}, true)
	if got, _ := os.ReadFile(target); string(got) != "ORIGINAL" {
		t.Errorf("symlink target = %q, want %q", got, "ORIGINAL")
	}
	if info, err := os.Lstat("Madefile.md"); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Madefile.md is no longer a symlink (err: %v)", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

// TestInitRefusesSymlinkWithoutFollowing checks plain made init never writes through a symlink.
func TestInitRefusesSymlinkWithoutFollowing(t *testing.T) {
	repo, target := initSymlinkMadefileTree(t)
	t.Chdir(repo)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code != 1 {
		t.Errorf("run(init) = %d, want 1", code)
	}
	checkContains(t, "stderr", stderr.String(), []string{"Madefile.md already exists", "made init --force"}, true)
	if got, _ := os.ReadFile(target); string(got) != "ORIGINAL" {
		t.Errorf("symlink target = %q, want %q", got, "ORIGINAL")
	}
	if info, err := os.Lstat("Madefile.md"); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Madefile.md is no longer a symlink (err: %v)", err)
	}
}

// TestInitDirectoryOnly checks that a directory named Madefile.md is neither
// overwritten nor reported as an existing Madefile.
func TestInitDirectoryOnly(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, "Madefile.md/")
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--force"}, &stdout, &stderr); code != 1 {
		t.Errorf("run(init --force) = %d, want 1", code)
	}
	checkContains(t, "stderr", stderr.String(), []string{"cannot create Madefile.md"}, true)
	if info, err := os.Stat("Madefile.md"); err != nil || !info.IsDir() {
		t.Errorf("Madefile.md is no longer a directory (err: %v)", err)
	}
}

// TestInitForceFIFO checks that init --force refuses a FIFO named Madefile.md
// instead of blocking on opening it for writing.
func TestInitForceFIFO(t *testing.T) {
	dir := t.TempDir()
	mkFIFO(t, filepath.Join(dir, "Madefile.md"))
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"init", "--force"}, &stdout, &stderr) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("run(init --force) = %d, want 1", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("made init --force hung on a FIFO Madefile.md")
	}
	checkContains(t, "stderr", stderr.String(), []string{"cannot create Madefile.md"}, true)
}

func TestCompletion(t *testing.T) {
	checkRunCases(t, []runCase{
		{name: "bash", args: []string{"completion", "bash"}, wantStdout: []string{"complete -o default -F _made made"}},
		{name: "zsh", args: []string{"completion", "zsh"}, wantStdout: []string{"#compdef made"}},
		{name: "fish", args: []string{"completion", "fish"}, wantStdout: []string{"complete -c made"}},
		{name: "no shell", args: []string{"completion"}, wantCode: 2, wantStderr: []string{"completion takes one of: bash, zsh, fish"}},
		{name: "unknown shell", args: []string{"completion", "pwsh"}, wantCode: 2, wantStderr: []string{"completion takes one of"}},
		{name: "extra args", args: []string{"completion", "zsh", "extra"}, wantCode: 2, wantStderr: []string{"completion takes one of"}},
		{name: "with -f", args: []string{"-f", "tasks.md", "completion", "zsh"}, wantCode: 2, wantStderr: []string{"completion does not take -f"}},
		{name: "with empty -f", args: []string{"-f", "", "completion", "zsh"}, wantCode: 2, wantStderr: []string{"-f requires a file path"}},
		{name: "-l before completion", args: []string{"-l", "completion", "bash"}, wantCode: 2, wantStderr: []string{"completion does not take -l"}, notStdout: []string{"complete -o default"}},
		{name: "--no-chdir before completion", args: []string{"--no-chdir", "completion", "zsh"}, wantCode: 2, wantStderr: []string{"completion does not take --no-chdir"}, notStdout: []string{"#compdef"}},
		{name: "--list-names before completion", args: []string{"--list-names", "completion", "fish"}, wantCode: 2, wantStderr: []string{"completion does not take --list-names"}, notStdout: []string{"complete -c made"}},
		{name: "help shows usage", args: []string{"-h"}, wantStdout: []string{"made completion <bash|zsh|fish>"}, notStdout: []string{"list-names"}},
	})
}

// TestCompletionIsReserved checks a user task named completion never runs, and that
// made completion needs no task file.
func TestCompletionIsReserved(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	content := "### completion\n```bash\ntouch " + marker + "\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "Madefile.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"completion", "bash"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(completion bash) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the user's completion task ran")
	}
}

func TestListNames(t *testing.T) {
	f := writeRunTasks(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-f", f, "--list-names"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--list-names) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	want := "hello\tSay hello.\ncheck\t\ncheckdash\t\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", &stderr)
	}
}

// TestListNamesSkipsDuplicateNote keeps stdout and stderr clean for completion
// scripts when several Madefile names exist.
func TestListNamesSkipsDuplicateNote(t *testing.T) {
	requireCaseSensitiveFS(t, t.TempDir())
	dir := t.TempDir()
	for _, name := range []string{"Madefile.md", "madefile.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(runTasksMD), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--list-names"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Errorf("run(--list-names) = %d, stderr %q; want 0 and empty", code, &stderr)
	}
}

func TestSanitizeDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "hello", want: "hello"},
		{name: "csi clear", in: "a\x1b[2Jb", want: "a[2Jb"},
		{name: "osc title", in: "\x1b]0;evil\x07title", want: "]0;eviltitle"},
		{name: "tab", in: "a\tb", want: "ab"},
		{name: "newline", in: "a\nb", want: "ab"},
		{name: "del", in: "a\x7fb", want: "ab"},
		{name: "c1 csi", in: "a\u009b2Jb", want: "a2Jb"},
		{name: "unicode", in: "保留", want: "保留"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeDisplay(tt.in); got != tt.want {
				t.Errorf("sanitizeDisplay(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// controlCharsTasksMD has escape sequences in the task heading and description.
const controlCharsTasksMD = "### evil\x1b[2J\n\x1b]0;pwned\x07note\n```bash\ntrue\n```\n"

func TestPrintTasksStripsControlChars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(controlCharsTasksMD), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
	}{
		{name: "default list", args: []string{"-f", path}},
		{name: "list flag", args: []string{"-f", path, "-l"}},
		{name: "list-names", args: []string{"-f", path, "--list-names"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != 0 {
				t.Fatalf("run(%v) = %d, want 0 (stderr: %s)", tt.args, code, &stderr)
			}
			out := stdout.String()
			if strings.Contains(out, "\x1b") {
				t.Errorf("stdout contains ESC: %q", out)
			}
			if strings.Contains(out, "\x07") {
				t.Errorf("stdout contains BEL: %q", out)
			}
			if !strings.Contains(out, "evil") {
				t.Errorf("stdout = %q, want visible task name", out)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", &stderr)
			}
		})
	}
}

func TestDisplayWidth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "empty", in: "", want: 0},
		{name: "ascii", in: "build", want: 5},
		{name: "cjk", in: "日本語", want: 6},
		{name: "emoji", in: "emoji🚀", want: 7},
		{name: "fullwidth", in: "ＡＢ", want: 4},
		{name: "halfwidth katakana", in: "ｱ", want: 1},
		{name: "combining accent", in: "e\u0301", want: 1},
		{name: "precomposed accent", in: "é", want: 1},
		{name: "emoji selector widens narrow symbol", in: "❤\ufe0f", want: 2},
		{name: "emoji selector on wide rune", in: "🚀\ufe0f", want: 2},
		{name: "text selector", in: "❤\ufe0e", want: 1},
		{name: "lone emoji selector", in: "\ufe0f", want: 0},
		{name: "zwj", in: "\u200d", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := displayWidth(tt.in); got != tt.want {
				t.Errorf("displayWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestPrintTasksAlignment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		md    string
		descs []string
	}{
		{
			name:  "cjk and emoji",
			md:    "### 日本語\nJapanese.\n```bash\ntrue\n```\n### emoji🚀\nRocket.\n```bash\ntrue\n```\n### build\nBuild it.\n```bash\ntrue\n```\n",
			descs: []string{"Japanese.", "Rocket.", "Build it."},
		},
		{
			name:  "ascii",
			md:    "### a\nShort.\n```bash\ntrue\n```\n### long-name\nLong.\n```bash\ntrue\n```\n",
			descs: []string{"Short.", "Long."},
		},
		{
			name:  "wide name without description",
			md:    "### 日本語\n```bash\ntrue\n```\n### b\nB.\n```bash\ntrue\n```\n",
			descs: []string{"", "B."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tasks, err := parser.Parse([]byte(tt.md))
			if err != nil {
				t.Fatalf("parser.Parse: %v", err)
			}
			var stdout, stderr bytes.Buffer
			printTasks(&stdout, &stderr, "tasks.md", tasks)
			rows := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")[1:]
			if len(rows) != len(tt.descs) {
				t.Fatalf("got %d rows, want %d: %q", len(rows), len(tt.descs), &stdout)
			}
			want := -1
			for i, row := range rows {
				prefix, ok := strings.CutSuffix(row, tt.descs[i])
				if !ok {
					t.Fatalf("row %q does not end with %q", row, tt.descs[i])
				}
				col := displayWidth(prefix)
				if want == -1 {
					want = col
				} else if col != want {
					t.Errorf("row %q: description at column %d, want %d", row, col, want)
				}
			}
		})
	}
}

func TestPrintTasksExactOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		tasks []parser.Task
		want  string
	}{
		{
			name:  "ascii matches tabwriter layout",
			tasks: []parser.Task{{Name: "a", Description: "Short."}, {Name: "long-name"}, {Name: "mid", Description: "Two\nlines."}},
			want: "  a          Short.\n" +
				"  long-name  \n" +
				"  mid        Two lines.\n",
		},
		{
			name:  "cjk and emoji",
			tasks: []parser.Task{{Name: "日本語", Description: "Japanese."}, {Name: "emoji🚀", Description: "Rocket."}, {Name: "build", Description: "Build it."}},
			want: "  日本語   Japanese.\n" +
				"  emoji🚀  Rocket.\n" +
				"  build    Build it.\n",
		},
		{
			name:  "emoji selector",
			tasks: []parser.Task{{Name: "⚙\ufe0f setup", Description: "Gear."}, {Name: "build", Description: "Build it."}},
			want: "  ⚙\ufe0f setup  Gear.\n" +
				"  build     Build it.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			printTasks(&stdout, &stderr, "tasks.md", tt.tasks)
			want := "Tasks in tasks.md:\n" + tt.want
			if got := stdout.String(); got != want {
				t.Errorf("printTasks output = %q, want %q", got, want)
			}
		})
	}
}
