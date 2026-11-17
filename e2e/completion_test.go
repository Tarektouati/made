package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// otherMD is a second task file for -f forwarding. Its task name has a colon, which
// zsh's _describe would otherwise read as the name/description separator.
const otherMD = "### ns:task\n" +
	"Has a colon: here.\n" +
	"```bash\n" +
	"echo\n" +
	"```\n"

// completionDir returns a repository-like TempDir holding Madefile.md (madefileMD), and
// other.md and "my tasks.md" (both otherMD).
func completionDir(t *testing.T) string {
	t.Helper()
	dir := writeTasks(t, "Madefile.md", madefileMD)
	mkdir(t, filepath.Join(dir, ".git"))
	for _, name := range []string{"other.md", "my tasks.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(otherMD), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runShell runs shell with args in dir, with the made binary under test first on
// PATH, and returns its stdout. It fails the test on a non-zero exit.
func runShell(t *testing.T, dir, shell string, args ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell completion scripts are not tested on Windows")
	}
	requireCmd(t, shell)
	cmd := exec.CommandContext(t.Context(), shell, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Dir(madeBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+t.TempDir(), // keep the user's shell config out of the test
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %q: %v\nstdout: %s\nstderr: %s", shell, args, err, &out, &errb)
	}
	return out.String()
}

// completionCase is a command line (as shell words, the last being the word under the
// cursor) and the completions it must and must not offer. compLine is bash's COMP_LINE
// for line; when empty, it is line joined with spaces.
type completionCase struct {
	line     []string
	compLine string
	want     []string
	not      []string
}

func checkCompletions(t *testing.T, got []string, tc completionCase) {
	t.Helper()
	for _, w := range tc.want {
		if !slices.Contains(got, w) {
			t.Errorf("%q: completions %q, want %q among them", tc.line, got, w)
		}
	}
	for _, w := range tc.not {
		if slices.Contains(got, w) {
			t.Errorf("%q: completions %q, want no %q", tc.line, got, w)
		}
	}
}

func lines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' })
}

// TestCompletionBash drives _made directly with COMP_LINE/COMP_POINT and
// COMP_WORDS/COMP_CWORD, which is what bash sets before calling a completion function.
// bash splits words on ":" too, so "ns:t" is the words "ns" ":" "t".
func TestCompletionBash(t *testing.T) {
	dir := completionDir(t)
	const script = `eval "$(made completion bash)"
COMP_LINE=$1; COMP_POINT=${#COMP_LINE}; shift
COMP_WORDS=("$@"); COMP_CWORD=$(($# - 1)); _made
printf '%s\n' "${COMPREPLY[@]}"`

	for _, tc := range []completionCase{
		{line: []string{"made", ""}, want: []string{"say-hello", "build", "init", "completion"}},
		{line: []string{"made", "b"}, want: []string{"build"}, not: []string{"say-hello"}},
		{line: []string{"made", "-f", "other.md", ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "--file", "=", "other.md", ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "--"}, want: []string{"--file", "--list", "--version", "--no-chdir", "--check", "--strict"}, not: []string{"--list-names"}},
		{line: []string{"made", "completion", ""}, want: []string{"bash", "zsh", "fish"}, not: []string{"build"}},
		{line: []string{"made", "build", ""}, not: []string{"build", "--file"}},
		{line: []string{"made", "init", ""}, want: []string{"--force"}, not: []string{"build"}},
		{line: []string{"made", "init", "--"}, want: []string{"--force"}},
		{line: []string{"made", "init", "--force", ""}, not: []string{"--force"}},
		{line: []string{"made", "-f", "other.md", "ns", ":", ""}, compLine: "made -f other.md ns:", want: []string{"task"}},
		{line: []string{"made", "-f", "other.md", "ns", ":", "t"}, compLine: "made -f other.md ns:t", want: []string{"task"}, not: []string{"ns:task"}},
		{line: []string{"made", "-f", "'my tasks.md'", ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "-f", `my\ tasks.md`, ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "-f", `"my tasks.md"`, ""}, want: []string{"ns:task"}, not: []string{"build"}},
	} {
		compLine := tc.compLine
		if compLine == "" {
			compLine = strings.Join(tc.line, " ")
		}
		args := append([]string{"-c", script, "bash", compLine}, tc.line...)
		got := lines(runShell(t, dir, "bash", args...))
		checkCompletions(t, got, tc)
	}
}

// TestCompletionZsh checks the script parses and that _made_tasks offers the right
// tasks, with _describe stubbed to print its entries. Driving zsh's full completion
// system needs a terminal, so the _arguments wiring is checked by hand.
func TestCompletionZsh(t *testing.T) {
	dir := completionDir(t)
	runShell(t, dir, "zsh", "-f", "-c", `made completion zsh | zsh -n`)

	const script = `compdef() { :; }
eval "$(made completion zsh)"
_describe() { print -rl -- "${(@P)${@[-1]}}"; }
words=("$@"); CURRENT=$#
_made_tasks`
	for _, tc := range []completionCase{
		{line: []string{"made", ""}, want: []string{"say-hello:Prints a friendly greeting.", "build:Compiles all packages.", "init:create a starter Madefile.md"}},
		{line: []string{"made", "-f", "other.md", ""}, want: []string{`ns\:task:Has a colon: here.`}, not: []string{"build:Compiles all packages."}},
		{line: []string{"made", "--file=other.md", ""}, want: []string{`ns\:task:Has a colon: here.`}},
		{line: []string{"made", "-f", "'my tasks.md'", ""}, want: []string{`ns\:task:Has a colon: here.`}, not: []string{"build:Compiles all packages."}},
		{line: []string{"made", `--file=my\ tasks.md`, ""}, want: []string{`ns\:task:Has a colon: here.`}},
	} {
		got := lines(runShell(t, dir, "zsh", append([]string{"-f", "-c", script, "zsh"}, tc.line...)...))
		checkCompletions(t, got, tc)
	}
}

// TestCompletionFish uses fish's own `complete -C`, which prints what the shell
// would offer for a command line.
func TestCompletionFish(t *testing.T) {
	dir := completionDir(t)
	for _, tc := range []completionCase{
		{line: []string{"made "}, want: []string{"say-hello\tPrints a friendly greeting.", "build\tCompiles all packages.", "init\tCreate a starter Madefile.md"}},
		{line: []string{"made -f other.md "}, want: []string{"ns:task\tHas a colon: here."}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made --file=other.md "}, want: []string{"ns:task\tHas a colon: here."}},
		{line: []string{"made -f 'my tasks.md' "}, want: []string{"ns:task\tHas a colon: here."}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made --"}, want: []string{"--file\tMarkdown file to read tasks from", "--list\tList available tasks", "--check\tCheck the task file for problems"}, not: []string{"--list-names"}},
		{line: []string{"made completion "}, want: []string{"bash", "zsh", "fish"}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made build "}, want: []string{"Madefile.md", "other.md"}, not: []string{"build\tCompiles all packages."}},
		// fish lists options only once a "-" is typed.
		{line: []string{"made init "}, not: []string{"build\tCompiles all packages.", "Madefile.md"}},
		{line: []string{"made init -"}, want: []string{"--force\tOverwrite an existing Madefile"}},
		{line: []string{"made init --force -"}, not: []string{"--force\tOverwrite an existing Madefile"}},
	} {
		got := lines(runShell(t, dir, "fish", "-N", "-c", `made completion fish | source; complete -C "$argv[1]"`, tc.line[0]))
		checkCompletions(t, got, tc)
	}
}
