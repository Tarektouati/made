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
const madefileMD = "# made test fixture\n\n" +
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
	return runMadeEnv(t, dir, nil, args...)
}

// runMadeEnv is runMade with extra KEY=VALUE environment entries, which override the
// inherited environment.
func runMadeEnv(t *testing.T, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), madeBin, args...)
	cmd.Dir = dir
	if env != nil {
		// Later entries win: os/exec dedups the environment, keeping the last value per key.
		cmd.Env = append(os.Environ(), env...)
	}
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

// requireCmd skips the test when name is not on PATH, so interpreter-specific tests
// stay green on machines lacking that interpreter while still running on CI.
func requireCmd(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found on PATH; skipping", name)
	}
}

// requireBash skips the test when bash is not on PATH (Git Bash provides it on
// windows-latest CI).
func requireBash(t *testing.T) {
	t.Helper()
	requireCmd(t, "bash")
}

func TestRunTask(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "Madefile.md", madefileMD)
	stdout, stderr, code := runMade(t, dir, "say-hello")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
}

func TestRunCRLFTask(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "crlf.md", "### crlf\r\n```bash\r\nif true; then\r\n  echo yes\r\nfi\r\n```\r\n")
	stdout, stderr, code := runMade(t, dir, "-f", "crlf.md", "crlf")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "yes\n" {
		t.Errorf("stdout = %q, want %q", stdout, "yes\n")
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

func TestRunTaskWithBOM(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "bom.md", "\xef\xbb\xbf### bom\n```\necho bom\n```\n")
	stdout, stderr, code := runMade(t, dir, "-f", "bom.md", "bom")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "bom\n" {
		t.Errorf("stdout = %q, want %q", stdout, "bom\n")
	}
}

func TestListNoArgs(t *testing.T) {
	dir := writeTasks(t, "Madefile.md", madefileMD)
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
	dir := writeTasks(t, "Madefile.md", madefileMD)
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
	dir := writeTasks(t, "Madefile.md", madefileMD)
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
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, ".git")) // repo boundary: keep the search inside dir
	_, stderr, code := runMade(t, dir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	for _, want := range []string{"made: no Madefile.md found in ", "use -f", "made init"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

func TestNoTasks(t *testing.T) {
	dir := writeTasks(t, "Madefile.md", proseMD)
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
		dir := writeTasks(t, "Madefile.md", content)
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
	dir := writeTasks(t, "Madefile.md", content)

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

func TestRunShFence(t *testing.T) {
	requireCmd(t, "sh")
	content := "### greet\n```sh\necho hi-from-sh\n```\n"
	dir := writeTasks(t, "Madefile.md", content)
	stdout, stderr, code := runMade(t, dir, "greet")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "hi-from-sh\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hi-from-sh\n")
	}
}

func TestShellTaskFailsFast(t *testing.T) {
	requireBash(t)
	// A failing command stops the task: the next line must not run, and the task's
	// exit code is the failing command's, not the last command's.
	content := "### check\n```bash\nfalse\necho after-failure\n```\n"
	dir := writeTasks(t, "Madefile.md", content)
	stdout, stderr, code := runMade(t, dir, "check")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "after-failure") {
		t.Errorf("stdout = %q; the command after the failure must not run", stdout)
	}
}

func TestShellTaskOptOut(t *testing.T) {
	requireBash(t)
	content := "### check\n```bash\nset +e\nfalse\necho after-failure\n```\n"
	dir := writeTasks(t, "Madefile.md", content)
	stdout, stderr, code := runMade(t, dir, "check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "after-failure") {
		t.Errorf("stdout = %q, want it to contain %q after `set +e`", stdout, "after-failure")
	}
}

func TestRunPythonFence(t *testing.T) {
	requireCmd(t, "python3")
	// A python fence proves a non-shell interpreter is actually used: this body is
	// valid Python and would fail under bash.
	content := "### pytask\n```python\nprint('hi-from-python')\n```\n"
	dir := writeTasks(t, "Madefile.md", content)
	stdout, stderr, code := runMade(t, dir, "pytask")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	// Python's text-mode stdout emits CRLF on Windows; normalize before comparing.
	if got := strings.ReplaceAll(stdout, "\r\n", "\n"); got != "hi-from-python\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hi-from-python\n")
	}
}

func TestUnsupportedLanguageFails(t *testing.T) {
	content := "### data\n```yaml\nkey: value\n```\n"
	dir := writeTasks(t, "Madefile.md", content)
	_, stderr, code := runMade(t, dir, "data")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "unsupported language") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "unsupported language")
	}
}

func TestMissingInterpreterFails(t *testing.T) {
	content := "### pytask\n```python\nprint('hi')\n```\n"
	dir := writeTasks(t, "Madefile.md", content)
	// An empty PATH makes python3 unresolvable; madeBin is absolute so it still runs.
	_, stderr, code := runMadeEnv(t, dir, []string{"PATH=" + t.TempDir()}, "pytask")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{`"python3" was not found on PATH`, `task "pytask"`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

func TestVersionFlag(t *testing.T) {
	dir := t.TempDir() // empty: no Madefile — --version must not require a task file
	for _, flag := range []string{"--version", "-V"} {
		stdout, stderr, code := runMade(t, dir, flag)
		if code != 0 {
			t.Fatalf("made %s exit = %d, want 0 (stderr: %s)", flag, code, stderr)
		}
		if !strings.HasPrefix(stdout, "made ") || strings.TrimSpace(stdout) == "made" {
			t.Errorf("made %s stdout = %q, want a non-empty %q line", flag, stdout, "made <version>")
		}
	}
}

func TestSIGINTKillsGrandchild(t *testing.T) {
	requireBash(t)
	if runtime.GOOS == "windows" {
		t.Skip("process-group teardown is unix-only")
	}

	// The task backgrounds a grandchild that writes a marker after a delay, then waits.
	// If made tears down the whole process group on cancel, the grandchild is killed
	// before it can write the marker.
	content := "### long\n```bash\n( sleep 3; echo alive > marker.txt ) &\necho started\nwait\n```\n"
	dir := writeTasks(t, "Madefile.md", content)

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

	if err := cmd.Start(); err != nil {
		t.Fatalf("start made: %v", err)
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

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
	case <-waitErr:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("made did not exit within 10s of SIGINT")
	}

	// Wait past the grandchild's delay; if its group was torn down it never wrote.
	time.Sleep(4 * time.Second)
	marker := filepath.Join(dir, "marker.txt")
	if _, err := os.Stat(marker); err == nil {
		t.Error("grandchild survived cancellation and wrote marker.txt; process group not torn down")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}

// catMarkerMD defines a task that prints marker.txt relative to its working directory,
// which reveals where the script ran without depending on how paths are spelled.
const catMarkerMD = "### where\n```bash\ncat marker.txt\n```\n"

// writeFile writes content to path, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// subdirLayout creates dir/sub/tasks.md (catMarkerMD) with markers in dir and dir/sub.
func subdirLayout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "tasks.md"), catMarkerMD)
	writeFile(t, filepath.Join(dir, "sub", "marker.txt"), "in-sub\n")
	writeFile(t, filepath.Join(dir, "marker.txt"), "in-cwd\n")
	return dir
}

func TestRunsInTaskFileDir(t *testing.T) {
	requireBash(t)
	dir := subdirLayout(t)
	stdout, stderr, code := runMade(t, dir, "-f", filepath.Join("sub", "tasks.md"), "where")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "in-sub\n" {
		t.Errorf("stdout = %q, want %q", stdout, "in-sub\n")
	}
}

func TestNoChdirRunsInCwd(t *testing.T) {
	requireBash(t)
	dir := subdirLayout(t)
	stdout, stderr, code := runMade(t, dir, "--no-chdir", "-f", filepath.Join("sub", "tasks.md"), "where")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "in-cwd\n" {
		t.Errorf("stdout = %q, want %q", stdout, "in-cwd\n")
	}
}

func TestFindsMadefileInParent(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Madefile.md"), catMarkerMD)
	writeFile(t, filepath.Join(dir, "marker.txt"), "in-root\n")
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runMade(t, nested, "where")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "in-root\n" {
		t.Errorf("stdout = %q, want %q", stdout, "in-root\n")
	}
	// The Madefile came from a parent directory, so made names it on stderr.
	if !strings.Contains(stderr, "made: using ") || !strings.Contains(stderr, "Madefile.md") {
		t.Errorf("stderr = %q, want a %q line naming the Madefile", stderr, "made: using")
	}
}

func TestNoUsingLineForMadefileInCwd(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Madefile.md"), catMarkerMD)
	writeFile(t, filepath.Join(dir, "marker.txt"), "here\n")
	_, stderr, code := runMade(t, dir, "where")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestExplicitFileNotSearchedUpward(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Madefile.md"), catMarkerMD)
	nested := filepath.Join(dir, "a")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runMade(t, nested, "-f", "Madefile.md", "where")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "cannot read") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "cannot read")
	}
}

func TestTaskArgsPassthrough(t *testing.T) {
	requireBash(t)
	content := "### echo-args\n```bash\nprintf '%s\\n' \"$@\"\n```\n"
	dir := writeTasks(t, "tasks.md", content)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"none", nil, "\n"}, // printf with no args still prints one empty line
		{"plain", []string{"a", "b c"}, "a\nb c\n"},
		{"dash args without separator", []string{"-x", "--flag"}, "-x\n--flag\n"},
		{"separator stripped", []string{"--", "-x"}, "-x\n"},
		{"only first separator stripped", []string{"--", "--"}, "--\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-f", "tasks.md", "echo-args"}, tc.args...)
			stdout, stderr, code := runMade(t, dir, args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
			}
			if stdout != tc.want {
				t.Errorf("stdout = %q, want %q", stdout, tc.want)
			}
		})
	}
}

// mkdir creates path and its parents.
func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// requireCaseSensitiveFS skips the test when dir's filesystem folds case, where
// Madefile.md and madefile.md cannot coexist.
func requireCaseSensitiveFS(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "case-probe"), "")
	if _, err := os.Stat(filepath.Join(dir, "CASE-PROBE")); err == nil {
		t.Skip("case-insensitive filesystem; skipping")
	}
}

func TestReadmeNotDiscovered(t *testing.T) {
	dir := writeTasks(t, "README.md", madefileMD)
	mkdir(t, filepath.Join(dir, ".git"))

	_, stderr, code := runMade(t, dir, "say-hello")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "no Madefile.md found") || !strings.Contains(stderr, "-f") {
		t.Errorf("stderr = %q, want a not-found error with a -f hint", stderr)
	}

	stdout, stderr, code := runMade(t, dir, "-f", "README.md", "-l")
	if code != 0 {
		t.Fatalf("-f README.md: exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "say-hello") {
		t.Errorf("stdout = %q, want it to list %q", stdout, "say-hello")
	}
}

func TestNestedMadefileShadowsRoot(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, ".git"))
	writeFile(t, filepath.Join(dir, "Madefile.md"), catMarkerMD)
	writeFile(t, filepath.Join(dir, "marker.txt"), "in-root\n")
	writeFile(t, filepath.Join(dir, "pkg", "Madefile.md"), catMarkerMD)
	writeFile(t, filepath.Join(dir, "pkg", "marker.txt"), "in-pkg\n")
	nested := filepath.Join(dir, "pkg", "sub")
	mkdir(t, nested)

	stdout, stderr, code := runMade(t, nested, "where")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "in-pkg\n" {
		t.Errorf("stdout = %q, want %q", stdout, "in-pkg\n")
	}
	want := filepath.Join("pkg", "Madefile.md") + "\n"
	if !strings.HasPrefix(stderr, "made: using ") || !strings.HasSuffix(stderr, want) {
		t.Errorf("stderr = %q, want a %q line naming %q", stderr, "made: using", want)
	}
}

func TestLowercaseMadefile(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "madefile.md", madefileMD)
	mkdir(t, filepath.Join(dir, ".git"))
	stdout, stderr, code := runMade(t, dir, "say-hello")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestGitBoundaryStopsSearch(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T, path string)
	}{
		{"git directory", mkdir},
		{"git file", func(t *testing.T, path string) { writeFile(t, path, "gitdir: elsewhere\n") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTasks(t, "Madefile.md", madefileMD)
			repo := filepath.Join(dir, "repo")
			tc.make(t, filepath.Join(repo, ".git"))
			_, stderr, code := runMade(t, repo, "-l")
			if code != 1 {
				t.Fatalf("exit = %d, want 1 (stderr: %s)", code, stderr)
			}
			if !strings.Contains(stderr, "no Madefile.md found") {
				t.Errorf("stderr = %q, want it to contain %q", stderr, "no Madefile.md found")
			}
		})
	}
}

func TestDuplicateMadefileWarns(t *testing.T) {
	dir := t.TempDir()
	requireCaseSensitiveFS(t, dir)
	mkdir(t, filepath.Join(dir, ".git"))
	writeFile(t, filepath.Join(dir, "Madefile.md"), madefileMD)
	writeFile(t, filepath.Join(dir, "madefile.md"), tasksMD)
	stdout, stderr, code := runMade(t, dir, "-l")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "say-hello") {
		t.Errorf("stdout = %q, want tasks from Madefile.md", stdout)
	}
	want := "made: using Madefile.md (also found madefile.md)"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func TestDuplicateMadefileInParent(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	requireCaseSensitiveFS(t, dir)
	mkdir(t, filepath.Join(dir, ".git"))
	writeFile(t, filepath.Join(dir, "Madefile.md"), catMarkerMD)
	writeFile(t, filepath.Join(dir, "madefile.md"), tasksMD)
	writeFile(t, filepath.Join(dir, "marker.txt"), "in-root\n")
	nested := filepath.Join(dir, "a")
	mkdir(t, nested)

	stdout, stderr, code := runMade(t, nested, "where")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "in-root\n" {
		t.Errorf("stdout = %q, want %q", stdout, "in-root\n")
	}
	// One line names both the parent path and the shadowed name.
	if n := strings.Count(stderr, "made: using "); n != 1 {
		t.Errorf("stderr = %q, want exactly one %q line, got %d", stderr, "made: using", n)
	}
	want := string(filepath.Separator) + "Madefile.md (also found madefile.md)\n"
	if !strings.HasSuffix(stderr, want) {
		t.Errorf("stderr = %q, want it to end with %q", stderr, want)
	}
}

// TestInit scaffolds a Madefile, then lists and runs its example task.
func TestInit(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, ".git")) // repo boundary: keep discovery inside dir

	stdout, stderr, code := runMade(t, dir, "init")
	if code != 0 || stdout != "made: created Madefile.md\n" {
		t.Fatalf("made init = (%d, %q), want (0, %q) (stderr: %s)", code, stdout, "made: created Madefile.md\n", stderr)
	}

	stdout, stderr, code = runMade(t, dir)
	if code != 0 || !strings.Contains(stdout, "hello") {
		t.Errorf("made = (%d, %q), want the hello task listed (stderr: %s)", code, stdout, stderr)
	}

	stdout, stderr, code = runMade(t, dir, "hello")
	if code != 0 || stdout != "hello\n" {
		t.Errorf("made hello = (%d, %q), want (0, %q) (stderr: %s)", code, stdout, "hello\n", stderr)
	}

	_, stderr, code = runMade(t, dir, "init")
	if code != 1 || !strings.Contains(stderr, "Madefile.md already exists") {
		t.Errorf("second made init = (%d, %q), want exit 1 and an already-exists error", code, stderr)
	}

	writeFile(t, filepath.Join(dir, "Madefile.md"), "### custom\n\n```bash\necho custom\n```\n")
	stdout, stderr, code = runMade(t, dir, "init", "--force")
	if code != 0 || stdout != "made: overwrote Madefile.md\n" {
		t.Errorf("made init --force = (%d, %q), want (0, %q) (stderr: %s)", code, stdout, "made: overwrote Madefile.md\n", stderr)
	}

	stdout, stderr, code = runMade(t, dir, "hello")
	if code != 0 || stdout != "hello\n" {
		t.Errorf("made hello after --force = (%d, %q), want (0, %q) (stderr: %s)", code, stdout, "hello\n", stderr)
	}

	if _, _, code = runMade(t, dir, "custom"); code == 0 {
		t.Error("made custom after --force exited 0, want the custom task gone")
	}
}

func TestListDoesNotHangOnFIFOMadefile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are not supported on Windows")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkFIFO(t, filepath.Join(dir, "Madefile.md"))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, madeBin, "-l")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("made -l hung on a FIFO Madefile.md")
	}
	if err == nil {
		t.Errorf("made -l on FIFO Madefile exited 0, want failure")
	}
	if !strings.Contains(stderr.String(), "no Madefile.md found") {
		t.Errorf("stderr = %q, want no Madefile.md found", stderr.String())
	}
}
