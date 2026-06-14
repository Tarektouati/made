package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// madeBin is the path to the compiled binary under test, set by TestMain.
var madeBin string

// Markdown fixtures. These use double-quoted strings (not raw strings) because the
// fenced code blocks contain backticks, which would terminate a Go raw string.
const readmeMD = "# made test fixture\n\n" +
	"## Tasks\n\n" +
	"### say-hello\n" +
	"Prints a friendly greeting.\n" +
	"```bash\n" +
	"echo hello\n" +
	"```\n\n" +
	"### build\n" +
	"Compiles all packages.\n" +
	"```bash\n" +
	"echo building\n" +
	"```\n"

const tasksMD = "### greet\n" +
	"```bash\n" +
	"echo hi-from-tasks\n" +
	"```\n"

// proseMD has no H3 + code block, so parser.Parse yields zero tasks.
const proseMD = "# made\n\nJust prose. No tasks defined here.\n"

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "made-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mktemp:", err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "made")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "github.com/Tarektouati/made")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build made:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	madeBin = bin

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// runMade execs the binary in dir and returns its stdout, stderr, and exit code
// (0 on success, the process exit code otherwise).
func runMade(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), madeBin, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("running made %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), errb.String(), code
}

// writeTasks creates a TempDir containing name with the given content.
func writeTasks(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return dir
}

// requireBash skips the test when bash is not on PATH, so task-execution tests stay
// green on machines without bash while still running on CI (Git Bash on windows-latest).
func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found on PATH; skipping task-execution test")
	}
}

func TestRunTask(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "README.md", readmeMD)
	stdout, stderr, code := runMade(t, dir, "say-hello")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
}

func TestRunTaskCustomFile(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "tasks.md", tasksMD)
	stdout, stderr, code := runMade(t, dir, "-f", "tasks.md", "greet")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "hi-from-tasks\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hi-from-tasks\n")
	}
}

func TestListNoArgs(t *testing.T) {
	dir := writeTasks(t, "README.md", readmeMD)
	stdout, stderr, code := runMade(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{
		"say-hello", "Prints a friendly greeting.",
		"build", "Compiles all packages.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list output missing %q; got:\n%s", want, stdout)
		}
	}
}

func TestListFlags(t *testing.T) {
	dir := writeTasks(t, "README.md", readmeMD)
	base, _, code := runMade(t, dir)
	if code != 0 {
		t.Fatalf("no-arg list exit = %d", code)
	}
	for _, flag := range []string{"-l", "--list"} {
		stdout, stderr, c := runMade(t, dir, flag)
		if c != 0 {
			t.Fatalf("made %s exit = %d (stderr: %s)", flag, c, stderr)
		}
		if stdout != base {
			t.Errorf("made %s stdout = %q, want same as no-arg list %q", flag, stdout, base)
		}
	}
}

func TestUnknownTask(t *testing.T) {
	dir := writeTasks(t, "README.md", readmeMD)
	stdout, stderr, code := runMade(t, dir, "nope")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "unknown task") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "unknown task")
	}
	if !strings.Contains(stdout, "say-hello") {
		t.Errorf("stdout = %q, want it to contain the task list (%q)", stdout, "say-hello")
	}
}

func TestMissingFile(t *testing.T) {
	dir := t.TempDir() // empty: no README.md
	_, stderr, code := runMade(t, dir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "cannot read") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "cannot read")
	}
}

func TestNoTasks(t *testing.T) {
	dir := writeTasks(t, "README.md", proseMD)
	_, stderr, code := runMade(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "no tasks found") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "no tasks found")
	}
}

func TestExitCodePropagation(t *testing.T) {
	requireBash(t)
	// Use two distinct codes to prove made returns the task's real exit code,
	// not a constant non-zero value.
	for _, want := range []int{3, 7} {
		content := fmt.Sprintf("### fail\n```bash\nexit %d\n```\n", want)
		dir := writeTasks(t, "README.md", content)
		_, stderr, code := runMade(t, dir, "fail")
		if code != want {
			t.Errorf("exit = %d, want %d (stderr: %s)", code, want, stderr)
		}
	}
}

func TestSIGINTCancellation(t *testing.T) {
	requireBash(t)
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt cannot be delivered to a child the same way on windows")
	}

	// "started" proves the task began; "done" must never appear once interrupted.
	content := "### long\n```bash\necho started; sleep 30; echo done\n```\n"
	dir := writeTasks(t, "README.md", content)

	// Route stdout to a file, not a pipe: when made kills bash on SIGINT, the
	// orphaned `sleep` keeps the inherited stdout open, so a pipe would not EOF
	// until the sleep ends (~30s). A file lets exec pass the fd directly, so
	// cmd.Wait returns as soon as made itself exits, and we can read it anytime.
	outPath := filepath.Join(t.TempDir(), "out.txt")
	outFile, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create out file: %v", err)
	}
	defer func() { _ = outFile.Close() }()

	cmd := exec.CommandContext(t.Context(), madeBin, "long")
	cmd.Dir = dir
	cmd.Stdout = outFile

	output := func() string {
		b, _ := os.ReadFile(outPath)
		return string(b)
	}

	begin := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start made: %v", err)
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	// Wait until the task has actually started running before interrupting.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(output(), "started") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("task never printed 'started'")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}

	select {
	case err := <-waitErr:
		if elapsed := time.Since(begin); elapsed > 10*time.Second {
			t.Errorf("made took %v to exit after SIGINT; want well under the 30s sleep", elapsed)
		}
		if err == nil {
			t.Error("made exited 0 after SIGINT; want non-zero")
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("made did not exit within 10s of SIGINT")
	}

	got := output()
	if !strings.Contains(got, "started") {
		t.Errorf("output = %q, want it to contain %q", got, "started")
	}
	if strings.Contains(got, "done") {
		t.Errorf("output = %q, should not contain %q after cancellation", got, "done")
	}
}
