package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/discover"
	"github.com/Tarektouati/made/internal/display"
	"github.com/Tarektouati/made/internal/lint"
	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/task"
	"github.com/Tarektouati/made/internal/testenv"
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
	newTestApp().checkRunCases(t, cases)
}

func (a *app) checkRunCases(t *testing.T, cases []runCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := a.run(tt.args, &stdout, &stderr)
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

// requireCmd skips the test when name is not on PATH (see testenv.RequireCmd).
func requireCmd(t *testing.T, name string) {
	t.Helper()
	testenv.RequireCmd(t, name)
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
		{name: "bad flag", args: []string{"-bogus"}, wantCode: 2, wantStderr: []string{"made: unknown flag -bogus (see made --help)"}, notStdout: []string{"Usage"}, notStderr: []string{"Usage"}},
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

// TestReadErrorPath checks that a task file that cannot be read is named once, quoted
// when blank or padded with spaces so the path stays visible.
func TestReadErrorPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.md")
	for _, tc := range []struct{ path, shown string }{
		{" ", `" "`},
		{"a ", `"a "`},
		{" a", `" a"`},
		{missing, missing},
	} {
		_, statErr := os.Stat(tc.path)
		if statErr == nil {
			t.Fatalf("%q exists", tc.path)
		}
		want := "made: cannot read " + tc.shown + ": " + display.PathOnlyOnce(tc.path, statErr).Error() + "\n"
		for _, args := range [][]string{{"-f", tc.path}, {"--check", "-f", tc.path}} {
			var stdout, stderr bytes.Buffer
			if code := newTestApp().run(args, &stdout, &stderr); code != 1 {
				t.Errorf("run(%q) = %d, want 1", args, code)
			}
			if got := stderr.String(); got != want {
				t.Errorf("run(%q) stderr = %q, want %q", args, got, want)
			}
		}
	}
}

// TestFlagErrors checks that a bad flag gets a single line on stderr naming the flag as
// it was typed, exit code 2, and no usage text.
func TestFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--nope"}, "made: unknown flag --nope (see made --help)\n"},
		{[]string{"-nope"}, "made: unknown flag -nope (see made --help)\n"},
		{[]string{"-lV"}, "made: unknown flag -lV (see made --help)\n"},
		{[]string{"--nope=3"}, "made: unknown flag --nope (see made --help)\n"},
		{[]string{"---x"}, "made: unknown flag ---x (see made --help)\n"},
		{[]string{"-l", "--nope", "task"}, "made: unknown flag --nope (see made --help)\n"},
		{[]string{"-f"}, "made: flag -f needs an argument (see made --help)\n"},
		{[]string{"-l", "--file"}, "made: flag --file needs an argument (see made --help)\n"},
		{[]string{"--list=maybe"}, "made: invalid value for flag --list (see made --help)\n"},
		{[]string{"--list=maybe", "---x"}, "made: invalid value for flag --list (see made --help)\n"},
		{[]string{"init", "--bogus"}, "made: unknown flag --bogus (see made init --help)\n"},
		{[]string{"init", "--force=maybe"}, "made: invalid value for flag --force (see made init --help)\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := newTestApp().run(tc.args, &stdout, &stderr); code != 2 {
				t.Errorf("run(%q) = %d, want 2", tc.args, code)
			}
			if got := stderr.String(); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
			if stdout.Len() > 0 {
				t.Errorf("stdout = %q, want it empty", &stdout)
			}
		})
	}
}

// TestUsage checks the help text: on stdout for -h, in the README's synopsis, with
// paired short and long names and no hidden or single-dash long flags.
func TestUsage(t *testing.T) {
	f := writeRunTasks(t)
	wantHelp := []string{
		"Usage: made [-f file] [--no-chdir] [-l] [-V] [task [--] [args...]]",
		"made [-f file] --check [--strict] [--host]",
		"made init [--force]",
		"made completion <bash|zsh|fish>",
		"-f, --file <file>",
		"    --no-chdir",
		"    --check",
		"    --strict",
		"    --host",
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
		runCase{name: "init bad flag", args: []string{"init", "--bogus"}, wantCode: 2, wantStderr: []string{"made: unknown flag --bogus (see made init --help)"}, notStdout: []string{"Usage"}, notStderr: []string{"Usage"}},
	)
	checkRunCases(t, cases)

	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"--help"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("run(--help) = %d, stderr %q; want 0 and empty", code, &stderr)
	}
	_, opts, ok := strings.Cut(stdout.String(), "Options:")
	if !ok {
		t.Fatalf("usage has no Options: section\n%s", &stdout)
	}
	prev := -1
	for _, name := range []string{"--file", "--no-chdir", "--check", "--strict", "--host", "--list", "--version", "--help"} {
		i := strings.Index(opts, name)
		if i <= prev {
			t.Errorf("%s at %d in the options list, want it after the previous flag (at %d)\n%s", name, i, prev, opts)
		}
		prev = i
	}
}

func TestRunRejectsDeepNesting(t *testing.T) {
	f := filepath.Join(t.TempDir(), "deep.md")
	if err := os.WriteFile(f, []byte("### t\n\n"+strings.Repeat("> ", 257)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"made: cannot parse ", "deep.md: line 3: nesting too deep (max 256)\n", "\nmade: hint: "}
	checkRunCases(t, []runCase{
		{
			name:       "list",
			args:       []string{"-f", f, "-l"},
			wantCode:   1,
			wantStderr: want,
		},
		{
			name:       "list-names",
			args:       []string{"-f", f, "--list-names"},
			wantCode:   1,
			wantStderr: want,
		},
	})
}

// TestRunDeepNestingHintQuotesPath checks the hint's command can be pasted into a
// shell: a path with a space is single-quoted, a plain one is left as is.
func TestRunDeepNestingHintQuotesPath(t *testing.T) {
	t.Chdir(t.TempDir())
	src := []byte(strings.Repeat("> ", 257) + "\n")
	for _, tc := range []struct{ file, want string }{
		{"my tasks.md", "run made --check -f 'my tasks.md'\n"},
		{"deep.md", "run made --check -f deep.md\n"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if err := os.WriteFile(tc.file, src, 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if code := newTestApp().run([]string{"-f", tc.file, "-l"}, &stdout, &stderr); code != 1 {
				t.Errorf("run = %d, want 1", code)
			}
			if got := stderr.String(); !strings.HasSuffix(got, tc.want) {
				t.Errorf("stderr = %q, want it to end with %q", got, tc.want)
			}
		})
	}
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

// TestRunArgsTooLarge checks that a task the OS refuses to start because its arguments
// are too large exits with 126, as a shell does, and is not blamed on the script.
func TestRunArgsTooLarge(t *testing.T) {
	requireCmd(t, "bash")
	f := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(f, []byte("### s\n```bash\necho ok\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := newTestApp().run([]string{"-f", f, "s", strings.Repeat("a", 4<<20)}, &stdout, &stderr)
	if code != 126 {
		t.Errorf("run = %d, want 126\nstderr: %s", code, &stderr)
	}
	checkContains(t, "stderr", stderr.String(), []string{`made: task "s": `, "1 argument of "}, true)
	checkContains(t, "stderr", stderr.String(), []string{"script is", "aaaa"}, false)
	checkContains(t, "stdout", stdout.String(), []string{"ok"}, false)
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
		{name: "taken as a flag", args: []string{"-f", f, "-x"}, wantCode: 2, wantStderr: []string{"made: unknown flag -x (see made --help)"}},
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
	a := newTestApp()
	f := writeRunTasks(t)
	for i := range 2 {
		var stdout, stderr bytes.Buffer
		if code := a.run([]string{"-f", f}, &stdout, &stderr); code != 0 {
			t.Fatalf("call %d: run = %d, want 0 (stderr: %s)", i+1, code, &stderr)
		}
		if !strings.Contains(stdout.String(), "hello") {
			t.Errorf("call %d: stdout = %q, want task list", i+1, &stdout)
		}
	}
}

func TestResolveVersionUsesBuildVersion(t *testing.T) {
	t.Parallel()

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
			t.Parallel()
			if got := resolveVersion(tt.ldflags); got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}

	// With no injected version, the resolver still returns a non-empty fallback.
	if got := resolveVersion(""); got == "" {
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
			dir, name, others, skipped, ok := discover.FindMadefile(filepath.Join(root, filepath.FromSlash(tt.start)), "")
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
				gotSkipped = append(gotSkipped, s.Path)
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
			dir, name, _, skipped, ok := discover.FindMadefile(a, "")
			if tt.rootFile && (!ok || dir != root || name != "Madefile.md") {
				t.Errorf("findMadefile = (%q, %q, %v), want (%q, %q, true)", dir, name, ok, root, "Madefile.md")
			}
			if !tt.rootFile && ok {
				t.Errorf("findMadefile = (%q, %q, true), want not found", dir, name)
			}
			wantPath := filepath.Join(a, tt.wantPath)
			if len(skipped) != 1 || skipped[0].Path != wantPath || !strings.Contains(skipped[0].Err.Error(), tt.wantReason) {
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
	dir, name, _, _, ok := discover.FindMadefile(filepath.Join(root, "a", "b", "c"), "")
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
			_, name, others, _, ok := discover.FindMadefile(root, "")
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

	dirPath := filepath.Join(dir, discover.Names()[0])
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
		code := newTestApp().run([]string{"-l"}, &stdout, &stderr)
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
		code := newTestApp().run([]string{"-l"}, &stdout, &stderr)
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
			code := newTestApp().run([]string{"hello"}, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run(hello) = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			checkContains(t, "stderr", stderr.String(), tt.wantErr, true)
		})
	}
}

// TestRunFailsOnUnreadableMadefile checks that a Madefile found but not readable
// stops discovery with an error instead of falling back to a parent's Madefile.
func TestRunFailsOnUnreadableMadefile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mode 000 file")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode 000 file")
	}
	tests := []struct {
		name string
		bad  func(t *testing.T, sub string) // makes sub/Madefile.md unreadable
	}{
		{"mode 000 file", func(t *testing.T, sub string) {
			mkUnreadable(t, filepath.Join(sub, "Madefile.md"))
		}},
		{"symlink to mode 000 file", func(t *testing.T, sub string) {
			mkUnreadable(t, filepath.Join(sub, "real.md"))
			mkSymlink(t, "real.md", filepath.Join(sub, "Madefile.md"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mkTree(t, dir, ".git/", "sub/")
			content := "# Tasks\n\n### hello\n```bash\necho outer\n```\n"
			if err := os.WriteFile(filepath.Join(dir, "Madefile.md"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			sub := filepath.Join(dir, "sub")
			tt.bad(t, sub)
			t.Chdir(sub)
			var stdout, stderr bytes.Buffer
			code := newTestApp().run([]string{"hello"}, &stdout, &stderr)
			if code != 1 {
				t.Errorf("run(hello) = %d, want 1 (stderr: %s)", code, stderr.String())
			}
			checkContains(t, "stderr", stderr.String(), []string{"made: cannot read Madefile.md: permission denied"}, true)
			checkContains(t, "stderr", stderr.String(), []string{"made: skipping ", "made: using "}, false)
		})
	}
}

// mkUnreadable creates a Madefile at path with mode 000.
func mkUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("### t\n```bash\necho inner\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
}

func TestRunReadsExplicitFIFO(t *testing.T) {
	clean := "### hello\n```bash\necho hi\n```\n"
	tests := []struct {
		name    string
		check   bool
		content string
		runCase
	}{
		{name: "list", content: clean, runCase: runCase{wantStdout: []string{"hello"}}},
		{name: "check clean", check: true, content: clean, runCase: runCase{notStdout: []string{"problem"}}},
		{name: "check errors", check: true, content: "### a\n```yaml\nk\n```\n",
			runCase: runCase{wantCode: 1, wantStdout: []string{":2: error: unsupported-language: "}}},
		{name: "list too large", content: strings.Repeat("x", discover.MaxMadefileBytes+1),
			runCase: runCase{wantCode: 1, wantStderr: []string{"exceeds maximum size"}}},
		{name: "check too large", check: true, content: strings.Repeat("x", discover.MaxMadefileBytes+1),
			runCase: runCase{wantCode: 1, wantStdout: []string{": error: file-too-large: "}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fifo := filepath.Join(t.TempDir(), "tasks.md")
			mkFIFO(t, fifo)
			feedFIFO(t, fifo, []byte(tt.content))
			tt.runCase.name = tt.name
			tt.args = []string{"-f", fifo, "-l"}
			if tt.check {
				tt.args = []string{"--check", "-f", fifo}
			}
			checkRunCases(t, []runCase{tt.runCase})
		})
	}
}

// TestListNamesRefusesExplicitFIFO checks that --list-names, which tab completion runs
// with the typed -f value, fails at once on a FIFO with no writer instead of blocking.
func TestListNamesRefusesExplicitFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "tasks.md")
	mkFIFO(t, fifo)
	type result struct {
		code   int
		stderr string
	}
	done := make(chan result, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := newTestApp().run([]string{"-f", fifo, "--list-names"}, &stdout, &stderr)
		done <- result{code, stderr.String()}
	}()
	select {
	case r := <-done:
		if r.code != 1 || !strings.Contains(r.stderr, "not a regular file") {
			t.Errorf("run(-f fifo --list-names) = %d, stderr %q; want 1, not a regular file", r.code, r.stderr)
		}
	case <-time.After(2 * time.Second):
		feedFIFO(t, fifo, nil)
		t.Fatal("run(-f fifo --list-names) blocked on a FIFO with no writer")
	}
}

func TestRunExplicitDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"-f", dir, "-l"}, {"--check", "-f", dir}} {
		var stdout, stderr bytes.Buffer
		code := newTestApp().run(args, &stdout, &stderr)
		if want := "made: cannot read " + display.Path(dir) + ": is a directory\n"; code != 1 || stderr.String() != want {
			t.Errorf("run(%q) = %d, stderr %q; want 1, %q", args, code, stderr.String(), want)
		}
	}
}

func TestRunExplicitDevNull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/dev/null needs Unix")
	}
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", os.DevNull, "-l"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(-f /dev/null -l) = %d, want 0 (stderr: %s)", code, &stderr)
	}
}

func TestReadMadefileDiscoveryRejectsFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "Madefile.md")
	mkFIFO(t, fifo)
	done := make(chan error, 1)
	go func() {
		_, _, err := discover.ReadMadefile(fifo, false, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("readMadefile(fifo, false, nil) error = %v, want not a regular file", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readMadefile(fifo, false, nil) blocked on a FIFO")
	}
}

func TestReadMadefileRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.md")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(discover.MaxMadefileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = discover.ReadMadefile(path, false, nil)
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
	reserved := writeTemp(t, "reserved.md", "### init\n```bash\nx\n```\n### -x\n```bash\nx\n```\n")
	big := writeTemp(t, "big.md", "### big\n```bash\n"+strings.Repeat("x", 280000)+"\n```\n")
	selfUpdate := writeTemp(t, "self-update.md", "### update\n```bash\nx\n```\n### self-update\n```bash\nx\n```\n")
	checkRunCases(t, []runCase{
		{name: "reserved name", args: []string{"--check", "-f", reserved}, wantCode: 1, wantStdout: []string{reserved + ":1: error: unreachable-name: ", reserved + ":5: warning: unreachable-name: ", "(1 error, 1 warning)"}},
		{name: "reserved self-update", args: []string{"--check", "-f", selfUpdate}, wantCode: 1, wantStdout: []string{selfUpdate + `:5: error: unreachable-name: task "self-update" cannot be run: made self-update is reserved`, "1 problem (1 error, 0 warnings)"}},
		{name: "self-update", args: []string{"--check", "self-update"}, wantCode: 2, wantStderr: []string{"made: --check takes no task name"}},
		{name: "script too large", args: []string{"--check", "-f", big}, wantCode: 1, wantStdout: []string{big + ":2: error: script-too-large: ", "script is 280001 "}},
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
		{name: "host alone", args: []string{"--host", "-f", clean}, wantCode: 2, wantStderr: []string{"made: --host requires --check"}},
		{name: "host clean", args: []string{"--check", "--host", "--strict", "-f", clean}},
	})
}

func TestCheckHost(t *testing.T) {
	f := writeTemp(t, "rb.md", "### rb\n```ruby\nputs \"hi\"\n```\n")
	t.Setenv("PATH", t.TempDir())
	checkRunCases(t, []runCase{
		{name: "without host", args: []string{"--check", "--strict", "-f", f}},
		{name: "host", args: []string{"--check", "--host", "-f", f}, wantCode: 1, wantStdout: []string{f + `:2: error: interpreter-not-found: "ruby" was not found on PATH`, "1 problem (1 error, 0 warnings)"}},
	})
}

// TestRunEmptyScript checks that a task whose first code block is empty or blank fails
// without running anything, including a later block under the same heading.
func TestRunEmptyScript(t *testing.T) {
	f := writeTemp(t, "tasks.md", "### empty\n```bash\n```\n\n```bash\ntouch ran\n```\n\n### blank\n```bash\n\n```\n")
	checkRunCases(t, []runCase{
		{name: "list", args: []string{"-l", "-f", f}, wantStdout: []string{"empty", "blank"}},
		{name: "empty", args: []string{"-f", f, "empty"}, wantCode: 1, wantStderr: []string{`made: task "empty" has an empty code block`}},
		{name: "blank", args: []string{"-f", f, "blank"}, wantCode: 1, wantStderr: []string{`made: task "blank" has an empty code block`}},
	})
	if _, err := os.Stat(filepath.Join(filepath.Dir(f), "ran")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat ran = %v, want the second code block not to run", err)
	}
}

func TestCheckExactOutput(t *testing.T) {
	f := writeTemp(t, "tasks.md", "### a\n```bash\nx\n```\n### a\n```bash\n```\n### b\nprose\n")
	var stdout, stderr bytes.Buffer
	code := newTestApp().run([]string{"--check", "-f", f}, &stdout, &stderr)
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
	if err = f.Truncate(discover.MaxMadefileBytes + 1); err != nil {
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
	got, err := discover.ResolveFile("some/tasks.md")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got.Path != "some/tasks.md" || got.FromParent || len(got.Others) != 0 {
		t.Errorf("resolveFile = %+v, want path %q only", got, "some/tasks.md")
	}
}

func TestResolveFileInCwdKeepsShortName(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, ".git/", "Madefile.md")
	t.Chdir(dir)
	got, err := discover.ResolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got.Path != "Madefile.md" || got.FromParent {
		t.Errorf("resolveFile = %+v, want path %q, fromParent false", got, "Madefile.md")
	}
}

func TestResolveFileFromParent(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, ".git/", "Madefile.md", "a/")
	t.Chdir(filepath.Join(dir, "a"))
	got, err := discover.ResolveFile("")
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
	if got.Path != want || !got.FromParent {
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
			got, err := discover.ResolveFile("")
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
			if got.Path != want || got.FromParent != tt.wantFromParent {
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
	got, err := discover.ResolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got.Path != "Madefile.md" || strings.Join(got.Others, ",") != "madefile.md" {
		t.Errorf("resolveFile = %+v, want path %q, others [madefile.md]", got, "Madefile.md")
	}
}

func TestResolveFileNotFound(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, ".git/", "README.md")
	t.Chdir(dir)
	_, err := discover.ResolveFile("")
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
	_, err := discover.ResolveFile("")
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
	a := newTestApp()
	a.version = "1.2.3" // as goreleaser injects it: no leading "v"
	dir := t.TempDir()
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := a.run([]string{"init"}, &stdout, &stderr); code != 0 {
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
			if diags := lint.Check([]byte(got), lint.Options{}); diags != nil {
				t.Errorf("lint.Check(template) = %+v, want no findings", diags)
			}
		})
	}
}

func TestInitRefusesExisting(t *testing.T) {
	for _, name := range discover.Names() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if name != discover.Names()[0] {
				// Probe a sibling dir: the probe file would break the entry count below.
				requireCaseSensitiveFS(t, t.TempDir())
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			var stdout, stderr bytes.Buffer
			if code := newTestApp().run([]string{"init"}, &stdout, &stderr); code != 1 {
				t.Errorf("run(init) = %d, want 1", code)
			}
			checkContains(t, "stderr", stderr.String(), []string{name + " already exists", "made init --force"}, true)
			if got, _ := os.ReadFile(path); string(got) != "keep me" {
				t.Errorf("%s = %q, want it untouched", name, got)
			}
			if name != discover.Names()[0] {
				if _, err := os.Stat(filepath.Join(dir, discover.Names()[0])); err == nil {
					t.Errorf("%s was created next to %s", discover.Names()[0], name)
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
	if code := newTestApp().run([]string{"init"}, &stdout, &stderr); code != 0 {
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
		{name: "unknown init flag", args: []string{"init", "--bogus"}, wantCode: 2, wantStderr: []string{"made: unknown flag --bogus (see made init --help)"}, notStderr: []string{"Usage"}},
		{name: "-f after init", args: []string{"init", "-f", "x"}, wantCode: 2, wantStderr: []string{"made: unknown flag -f (see made init --help)"}},
		{name: "init help", args: []string{"init", "-h"}, wantCode: 0, wantStdout: []string{"Usage: made init [--force]", "--force"}},
		{name: "-l before init", args: []string{"-l", "init"}, wantCode: 2, wantStderr: []string{"init does not take -l"}},
		{name: "--list before init", args: []string{"--list", "init"}, wantCode: 2, wantStderr: []string{"init does not take --list"}},
		{name: "--no-chdir before init", args: []string{"--no-chdir", "init"}, wantCode: 2, wantStderr: []string{"init does not take --no-chdir"}},
		{name: "--list-names before init", args: []string{"--list-names", "init"}, wantCode: 2, wantStderr: []string{"init does not take --list-names"}},
		{name: "-l after init", args: []string{"init", "-l"}, wantCode: 2, wantStderr: []string{"made: unknown flag -l (see made init --help)"}},
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
	if code := newTestApp().run([]string{"init"}, &stdout, &stderr); code != 1 {
		t.Errorf("run(init) = %d, want 1", code)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the user's init task ran")
	}
}

func TestInitForceOverwrites(t *testing.T) {
	for _, name := range discover.Names() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if name != discover.Names()[0] {
				// Probe a sibling dir: the probe file would break the entry count below.
				requireCaseSensitiveFS(t, t.TempDir())
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			var stdout, stderr bytes.Buffer
			if code := newTestApp().run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
			}
			if want := "made: overwrote " + name + "\n"; stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}
			if got, _ := os.ReadFile(path); string(got) != initTemplate(resolveVersion("")) {
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
	if code := newTestApp().run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got := stdout.String(); got != "made: created Madefile.md\n" {
		t.Errorf("stdout = %q, want %q", got, "made: created Madefile.md\n")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "Madefile.md")); string(got) != initTemplate(resolveVersion("")) {
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
	if code := newTestApp().run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got := stdout.String(); got != "made: overwrote Madefile.md\n" {
		t.Errorf("stdout = %q, want %q", got, "made: overwrote Madefile.md\n")
	}
	checkContains(t, "stderr", stderr.String(), []string{"also found madefile.md; left unchanged"}, true)
	if got, _ := os.ReadFile("Madefile.md"); string(got) != initTemplate(resolveVersion("")) {
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
	if code := newTestApp().run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(init --force) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got := stdout.String(); got != "made: overwrote madefile.md\n" {
		t.Errorf("stdout = %q, want %q", got, "made: overwrote madefile.md\n")
	}
	if got, _ := os.ReadFile("madefile.md"); string(got) != initTemplate(resolveVersion("")) {
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
	if err := newTestApp().writeTemplate("Madefile.md", os.O_TRUNC|initForceNoFollow); err == nil {
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
	if code := newTestApp().run([]string{"init", "--force"}, &stdout, &stderr); code != 1 {
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
	if code := newTestApp().run([]string{"init"}, &stdout, &stderr); code != 1 {
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
	checkInitNonRegular(t, "made: cannot create Madefile.md: it exists and is not a regular file (a directory); remove it first\n")
	if info, err := os.Stat("Madefile.md"); err != nil || !info.IsDir() {
		t.Errorf("Madefile.md is no longer a directory (err: %v)", err)
	}
}

// checkInitNonRegular runs made init, plain and with --force, in the current directory
// whose Madefile.md is not a regular file, and checks both refuse with want on stderr.
func checkInitNonRegular(t *testing.T, want string) {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"init", "--force"}} {
		var stdout, stderr bytes.Buffer
		if code := newTestApp().run(args, &stdout, &stderr); code != 1 {
			t.Errorf("run(%q) = %d, want 1", args, code)
		}
		if got := stderr.String(); got != want {
			t.Errorf("run(%q) stderr = %q, want %q", args, got, want)
		}
		if stdout.Len() > 0 {
			t.Errorf("run(%q) stdout = %q, want it empty", args, &stdout)
		}
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
	go func() { done <- newTestApp().run([]string{"init", "--force"}, &stdout, &stderr) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("run(init --force) = %d, want 1", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("made init --force hung on a FIFO Madefile.md")
	}
	if got, want := stderr.String(), "made: cannot create Madefile.md: it exists and is not a regular file; remove it first\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
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
	if code := newTestApp().run([]string{"completion", "bash"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(completion bash) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the user's completion task ran")
	}
}

func TestListNames(t *testing.T) {
	f := writeRunTasks(t)
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", f, "--list-names"}, &stdout, &stderr); code != 0 {
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

// TestListNamesSkipsEmptyName leaves out tasks whose name is empty once sanitized: a
// line "\tdesc" would make bash's read take the description as the name.
func TestListNamesSkipsEmptyName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	md := "### \nempty\n```sh\necho\n```\n" +
		"### \u200b\ninvisible\n```sh\necho\n```\n" +
		"### hello\nSay hello.\n```sh\necho\n```\n"
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", path, "--list-names"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--list-names) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if got, want := stdout.String(), "hello\tSay hello.\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
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
	if code := newTestApp().run([]string{"--list-names"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Errorf("run(--list-names) = %d, stderr %q; want 0 and empty", code, &stderr)
	}
}

// dupTasksMD defines "dup" twice; the second definition exits 3, so a zero exit
// code shows the first one ran.
const dupTasksMD = "### dup\nThe first.\n```bash\nexit 0\n```\n\n" +
	"### dup\nThe second.\n```bash\nexit 3\n```\n\n" +
	"### other\n```bash\nexit 0\n```\n"

const dupWarning = `made: task "dup" is defined again at line 7; only the first (line 1) runs` + "\n"

func TestRunWarnsDuplicateTask(t *testing.T) {
	f := writeTemp(t, "Madefile.md", dupTasksMD)
	listing := "Tasks in " + f + ":\n  dup    The first.\n  other  \n"
	for _, tc := range []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // the task's own output goes to the real stdout
	}{
		{name: "run duplicated task", args: []string{"dup"}},
		{name: "run other task", args: []string{"other"}},
		{name: "list", args: []string{"-l"}, wantStdout: listing},
		{name: "no task", args: nil, wantStdout: listing},
		{name: "unknown task", args: []string{"nope"}, wantCode: 1, wantStdout: listing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := newTestApp().run(append([]string{"-f", f}, tc.args...), &stdout, &stderr)
			if code != tc.wantCode {
				t.Errorf("run = %d, want %d (stderr: %s)", code, tc.wantCode, &stderr)
			}
			if got := strings.Count(stderr.String(), dupWarning); got != 1 {
				t.Errorf("stderr = %q, want the warning %q once", &stderr, dupWarning)
			}
			if !strings.HasPrefix(stderr.String(), dupWarning) {
				t.Errorf("stderr = %q, want it to start with the warning", &stderr)
			}
			if stdout.String() != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", &stdout, tc.wantStdout)
			}
		})
	}
}

// TestRunUsingNoteBeforeDuplicateWarning pins the stderr order when the Madefile
// comes from a parent directory: the using note names the file whose lines the
// duplicate warning then cites.
func TestRunUsingNoteBeforeDuplicateWarning(t *testing.T) {
	root := filepath.Dir(writeTemp(t, "Madefile.md", dupTasksMD))
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"dup"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, &stderr)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "made: using ") || !strings.HasSuffix(got, "Madefile.md\n"+dupWarning) || strings.Count(got, "\n") != 2 {
		t.Errorf("stderr = %q, want one using line and then the duplicate warning", got)
	}
}

func TestRunWarnsEachRedefinition(t *testing.T) {
	f := writeTemp(t, "Madefile.md", "### a\n```bash\nexit 0\n```\n### a\n```bash\nexit 3\n```\n### a\n```bash\nexit 4\n```\n")
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", f, "a"}, &stdout, &stderr); code != 0 {
		t.Errorf("run = %d, want 0 (stderr: %s)", code, &stderr)
	}
	want := `made: task "a" is defined again at line 5; only the first (line 1) runs` + "\n" +
		`made: task "a" is defined again at line 9; only the first (line 1) runs` + "\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", &stderr, want)
	}
}

func TestListNamesQuietOnDuplicates(t *testing.T) {
	f := writeTemp(t, "Madefile.md", dupTasksMD)
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", f, "--list-names"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--list-names) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if stderr.Len() != 0 || strings.Contains(stdout.String(), "defined again") {
		t.Errorf("stdout = %q, stderr = %q; want no warning", &stdout, &stderr)
	}
}

func TestRunSkipsEmptyNames(t *testing.T) {
	f := writeTemp(t, "Madefile.md", "### ok\n```bash\nexit 0\n```\n\n###\n```bash\nexit 3\n```\n\n### ` `\n```bash\nexit 4\n```\n")
	const warnings = "made: task at line 6 has an empty name; it is skipped\n" +
		"made: task at line 11 has an empty name; it is skipped\n"

	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", f, "-l"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(-l) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if want := "Tasks in " + f + ":\n  ok  \n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", &stdout, want)
	}
	if stderr.String() != warnings {
		t.Errorf("stderr = %q, want %q", &stderr, warnings)
	}

	for _, name := range []string{"", " "} {
		stdout.Reset()
		stderr.Reset()
		if code := newTestApp().run([]string{"-f", f, name}, &stdout, &stderr); code != 1 {
			t.Errorf("run(%q) = %d, want 1", name, code)
		}
		if want := warnings + fmt.Sprintf("made: unknown task %q\n\n", name); stderr.String() != want {
			t.Errorf("run(%q) stderr = %q, want %q", name, &stderr, want)
		}
	}
}

// TestRunWarningsMatchCheck keeps the run-time warnings on the same lines as the
// duplicate-task and empty-name findings of made --check.
func TestRunWarningsMatchCheck(t *testing.T) {
	src := "### a\n```bash\nx\n```\n### a\nprose\n### a\n```bash\ny\n```\n" +
		"###\n```bash\nx\n```\n### ` `\n```bash\nx\n```\n### <br>\n```bash\nx\n```\n" +
		"## Section\n### b\n```bash\nx\n```\n### b\n```sh\nx\n```\n### a\n```bash\nz\n```\n"
	var want []int
	for _, d := range lint.Check([]byte(src), lint.Options{}) {
		if d.Rule == "duplicate-task" || d.Rule == "empty-name" {
			want = append(want, d.Line)
		}
	}
	f := writeTemp(t, "Madefile.md", src)
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", f, "-l"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(-l) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	var got []int
	for line := range strings.Lines(stderr.String()) {
		_, rest, _ := strings.Cut(line, "at line ")
		var n int
		if _, err := fmt.Sscanf(rest, "%d", &n); err != nil {
			t.Fatalf("cannot read the line number in %q: %v", line, err)
		}
		got = append(got, n)
	}
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("warned lines %v, --check lines %v (stderr: %s)", got, want, &stderr)
	}
}

// scotlandFlag is an emoji tag sequence: black flag, tags "gbsct", cancel tag.
const scotlandFlag = "🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f"

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
			if code := newTestApp().run(tt.args, &stdout, &stderr); code != 0 {
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

func TestPrintTasksStripsBidiChars(t *testing.T) {
	path := writeTemp(t, "tasks.md", "### bid\u202ei\nsp\u200boof\u2066x\n```bash\ntrue\n```\n")
	for _, args := range [][]string{{"-f", path, "-l"}, {"-f", path, "--list-names"}} {
		var stdout, stderr bytes.Buffer
		if code := newTestApp().run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%v) = %d, want 0 (stderr: %s)", args, code, &stderr)
		}
		out := stdout.String()
		if !strings.Contains(out, "bidi") || !strings.Contains(out, "spoofx") {
			t.Errorf("run(%v) stdout = %q, want bidi and spoofx with format characters removed", args, out)
		}
	}
}

// TestListKeepsJoiners checks that emoji ZWJ and tag sequences and ZWNJ words are
// listed as written.
func TestListKeepsJoiners(t *testing.T) {
	names := []string{"🧑\u200d💻 dev setup", "🏳\ufe0f\u200d🌈", scotlandFlag, "می\u200cخواهم"}
	var md strings.Builder
	for _, n := range names {
		md.WriteString("### " + n + "\n```bash\ntrue\n```\n")
	}
	path := writeTemp(t, "tasks.md", md.String())
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", path, "--list-names"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--list-names) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	want := strings.Join(names, "\t\n") + "\t\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// TestListedNameRuns checks that a name with invisible characters runs both under the
// name made lists for it and as written in the Madefile.
func TestListedNameRuns(t *testing.T) {
	requireCmd(t, "sh")
	tests := []struct {
		source string // heading text, also typed as the task argument
		listed string
	}{
		{"bid\u202ei\u200b", "bidi"},
		{"a\u200cb", "a\u200cb"},
		{"\u2066dev\u2069  setup\t", "dev setup"},
	}
	for _, tt := range tests {
		t.Run(tt.listed, func(t *testing.T) {
			path := writeTemp(t, "tasks.md", "### "+tt.source+"\n```sh\ntrue\n```\n")
			var stdout, stderr bytes.Buffer
			if code := newTestApp().run([]string{"-f", path, "--list-names"}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(--list-names) = %d, want 0 (stderr: %s)", code, &stderr)
			}
			name, _, _ := strings.Cut(stdout.String(), "\t")
			if name != tt.listed {
				t.Fatalf("listed name = %q, want %q", name, tt.listed)
			}
			for _, arg := range []string{tt.listed, tt.source} {
				stderr.Reset()
				if code := newTestApp().run([]string{"-f", path, arg}, &stdout, &stderr); code != 0 {
					t.Errorf("run(%q) = %d, want 0 (stderr: %s)", arg, code, &stderr)
				}
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
				col := display.Width(prefix)
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
		tasks []task.Task
		want  string
	}{
		{
			name:  "ascii matches tabwriter layout",
			tasks: []task.Task{{Name: "a", Description: "Short."}, {Name: "long-name"}, {Name: "mid", Description: "Two\nlines."}},
			want: "  a          Short.\n" +
				"  long-name  \n" +
				"  mid        Two lines.\n",
		},
		{
			name:  "cjk and emoji",
			tasks: []task.Task{{Name: "日本語", Description: "Japanese."}, {Name: "emoji🚀", Description: "Rocket."}, {Name: "build", Description: "Build it."}},
			want: "  日本語   Japanese.\n" +
				"  emoji🚀  Rocket.\n" +
				"  build    Build it.\n",
		},
		{
			name:  "emoji selector",
			tasks: []task.Task{{Name: "⚙\ufe0f setup", Description: "Gear."}, {Name: "build", Description: "Build it."}},
			want: "  ⚙\ufe0f setup  Gear.\n" +
				"  build     Build it.\n",
		},
		{
			name:  "emoji sequences",
			tasks: []task.Task{{Name: "👨\u200d👩\u200d👧family", Description: "Fam."}, {Name: "👩🏻\u200d💻tech", Description: "Tech."}, {Name: "👍🏽ok", Description: "OK."}, {Name: "✌🏽peace", Description: "Peace."}, {Name: "build", Description: "Build."}},
			want: "  👨\u200d👩\u200d👧family  Fam.\n" +
				"  👩🏻\u200d💻tech    Tech.\n" +
				"  👍🏽ok      OK.\n" +
				"  ✌🏽peace   Peace.\n" +
				"  build     Build.\n",
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

// writeSlowParseMadefile writes a task file whose description makes goldmark's inline
// parser slow (unclosed mixed emphasis): hundreds of milliseconds, far past the 1ms
// parseTimeout the caller sets even with coarse timers (Windows fires them late).
func writeSlowParseMadefile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "slow.md")
	src := "### t\n\n" + strings.Repeat("_a*", 10000) + "\n\n```bash\necho ok\n```\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseTimeout(t *testing.T) {
	a := newTestApp()
	a.parseTimeout = time.Millisecond
	f := writeSlowParseMadefile(t)
	a.checkRunCases(t, []runCase{
		{name: "list", args: []string{"-f", f, "-l"}, wantCode: 1,
			wantStderr: []string{"cannot parse", "parsing took longer than 1ms"}},
		{name: "list-names", args: []string{"-f", f, "--list-names"}, wantCode: 1,
			wantStderr: []string{"cannot parse", "parsing took longer than 1ms"}},
		{name: "run", args: []string{"-f", f, "t"}, wantCode: 1,
			wantStderr: []string{"cannot parse", "parsing took longer than 1ms"}},
		{name: "check", args: []string{"--check", "-f", f}, wantCode: 1,
			wantStdout: []string{f + ": error: parse-timeout: parsing took longer than 1ms"}},
	})
}
