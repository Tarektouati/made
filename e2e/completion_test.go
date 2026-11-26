package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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

// edgeMD is a task file with names the scripts must handle: the task back\slash, whose
// description has a backslash and a colon, and a task with an empty name.
const edgeMD = "### back\\\\slash\n" +
	"has \\\\ a: b\n" +
	"```bash\n" +
	"echo\n" +
	"```\n" +
	"### \n" +
	"empty\n" +
	"```bash\n" +
	"echo\n" +
	"```\n"

// quoteNames are the task names in quoteMD: each has a character the shell would
// read as syntax if completion inserted it unquoted.
var quoteNames = []string{
	"a b", "it's", `say "hi"`, "tick`x", "$(id)", "dollar $(touch MARK)",
	"with space", "ns:task", "a=b", `back\slash`, "~root", "bang!", "a=~root", "x:~y",
	"user@host",
}

// quoteMD is a task file with the quoteNames, escaped as Markdown where needed.
var quoteMD = func() string {
	var b strings.Builder
	for _, name := range quoteNames {
		name = strings.NewReplacer(`\`, `\\`, "`", "\\`").Replace(name)
		b.WriteString("### " + name + "\n```sh\n:\n```\n")
	}
	return b.String()
}()

// completionDir returns a repository-like TempDir holding Madefile.md (madefileMD),
// other.md and "my tasks.md" (both otherMD), edge.md (edgeMD) and quote.md (quoteMD).
func completionDir(t *testing.T) string {
	t.Helper()
	dir := writeTasks(t, "Madefile.md", madefileMD)
	mkdir(t, filepath.Join(dir, ".git"))
	for name, content := range map[string]string{"other.md": otherMD, "my tasks.md": otherMD, "edge.md": edgeMD, "quote.md": quoteMD} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runShell runs shell with args in dir, with the made binary under test first on
// PATH, and returns its stdout. It fails the test on a non-zero exit.
func runShell(t *testing.T, dir, shell string, args ...string) string {
	t.Helper()
	out, _ := runShellOut(t, dir, shell, args...)
	return out
}

// runShellOut is runShell returning stderr too.
func runShellOut(t *testing.T, dir, shell string, args ...string) (stdout, stderr string) {
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
	return out.String(), errb.String()
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

// noRun is a -f value that would create the file PWNED in the test directory if
// completion ran the command substitution in it.
const noRun = `$(touch PWNED)/other.md`

// checkNoRun fails the test if a completion created dir/PWNED.
func checkNoRun(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "PWNED")); err == nil {
		t.Error("completion ran a command substitution from the command line")
	}
}

// TestCompletionBash drives _made directly with COMP_LINE/COMP_POINT and
// COMP_WORDS/COMP_CWORD, which is what bash sets before calling a completion function.
// bash splits words on ":" too, so "ns:t" is the words "ns" ":" "t".
func TestCompletionBash(t *testing.T) {
	dir := completionDir(t)
	// The user variables name, v, cur and first share names with locals of the script
	// once, which hid them from the -f expansion.
	const script = `eval "$(made completion bash)"
name=$PWD v=$PWD cur=$PWD first=$PWD
COMP_LINE=$1; COMP_POINT=${#COMP_LINE}; shift
COMP_WORDS=("$@"); COMP_CWORD=$(($# - 1)); _made
printf '%s\n' "${COMPREPLY[@]}"`

	for _, tc := range []completionCase{
		{line: []string{"made", ""}, want: []string{"say-hello", "build", "init", "completion", "self-update"}},
		{line: []string{"made", "b"}, want: []string{"build"}, not: []string{"say-hello"}},
		{line: []string{"made", "se"}, want: []string{"self-update"}, not: []string{"say-hello"}},
		{line: []string{"made", "self-update", ""}, want: []string{"--dry-run"}, not: []string{"build", "self-update"}},
		{line: []string{"made", "self-update", "--"}, want: []string{"--dry-run"}},
		{line: []string{"made", "self-update", "v1.2.3", ""}, want: []string{"--dry-run"}, not: []string{"build"}},
		{line: []string{"made", "self-update", "--dry-run", ""}, not: []string{"--dry-run", "build"}},
		{line: []string{"made", "-f", "other.md", ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "--env-file", "missing.env", "-f", "other.md", "--print", ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "--env-file=missing.env", "--dry-run", "b"}, want: []string{"build"}},
		{line: []string{"made", "--env-file", noRun, "b"}, want: []string{"build"}},
		{line: []string{"made", "--env-file", ""}, not: []string{"build", "self-update"}},
		// bash 4+ splits "--file=other.md" on "=", bash 3.2 does not. With spaces, Go's
		// flag package reads "=" as the file name and other.md as the task.
		{line: []string{"made", "--file", "=", "other.md", ""}, compLine: "made --file=other.md ", want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "--file=other.md", ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "--file", "=", "other.md", ""}, not: []string{"ns:task", "build"}},
		{line: []string{"made", "-f", "=", ""}, want: []string{"init"}, not: []string{"build", "ns:task"}},
		{line: []string{"made", "--file", "="}, compLine: "made --file=", not: []string{"build", "--file"}},
		{line: []string{"made", "--file="}, not: []string{"build", "--file"}},
		{line: []string{"made", "--"}, want: []string{"--file", "--list", "--version", "--no-chdir", "--env-file", "--print", "--dry-run", "--check", "--strict", "--host"}, not: []string{"--list-names"}},
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
		{line: []string{"made", "-f", "edge.md", ""}, want: []string{`back\\slash`}, not: []string{"empty"}},
		// --check takes no task, and --strict and --host need --check.
		{line: []string{"made", "--check", ""}, not: []string{"build", "init", "completion", "self-update"}},
		{line: []string{"made", "--strict", ""}, not: []string{"build", "init", "completion"}},
		{line: []string{"made", "-host", ""}, not: []string{"build", "init", "completion"}},
		{line: []string{"made", "-f", "other.md", "--check", ""}, not: []string{"ns:task"}},
		{line: []string{"made", "--check", "--"}, want: []string{"--strict", "--host", "--file", "--no-chdir"}, not: []string{"--list", "--version", "--env-file", "--print", "--dry-run"}},
		// A leading $NAME or ${NAME} is expanded, outside single quotes only, and
		// nothing else on the line is evaluated. The test runs in $PWD.
		{line: []string{"made", "-f", "$PWD/other.md", ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "-f", `"${PWD}/my tasks.md"`, ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "-f", `"$PWD"/other.md`, ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "-f", `"${PWD}"/other.md`, ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "-f", `"my "tasks'.md'`, ""}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{"made", "-f", "$name/other.md", ""}, want: []string{"ns:task"}},
		{line: []string{"made", "-f", "$v/other.md", ""}, want: []string{"ns:task"}},
		{line: []string{"made", "-f", "$cur/other.md", ""}, want: []string{"ns:task"}},
		{line: []string{"made", "-f", "${first}/other.md", ""}, want: []string{"ns:task"}},
		{line: []string{"made", "--file", "=", "$PWD/other.md", ""}, compLine: "made --file=$PWD/other.md ", want: []string{"ns:task"}},
		{line: []string{"made", "-f", "'$PWD/other.md'", ""}, not: []string{"ns:task"}},
		{line: []string{"made", "-f", `\$PWD/other.md`, ""}, not: []string{"ns:task"}},
		{line: []string{"made", "-f", "${PWD:-x}/other.md", ""}, not: []string{"ns:task"}},
		{line: []string{"made", "-f", noRun, ""}, not: []string{"ns:task"}},
		{line: []string{"made", "-f", "${PWD[$(touch PWNED)]}/other.md", ""}, not: []string{"ns:task"}},
		{line: []string{"made", "-f", "$PWD[$(touch PWNED)]/other.md", ""}, not: []string{"ns:task"}},
	} {
		compLine := tc.compLine
		if compLine == "" {
			compLine = strings.Join(tc.line, " ")
		}
		args := append([]string{"-c", script, "bash", compLine}, tc.line...)
		got := lines(runShell(t, dir, "bash", args...))
		checkCompletions(t, got, tc)
	}
	checkNoRun(t, dir)
}

// TestCompletionBashQuoting checks that each task name bash completion offers, once
// inserted the way readline does it, is one shell word equal to the name, and that no
// insertion runs a command. Readline replaces the text after an open quote, or else
// after the last unquoted ":" or "=" (keep is what it leaves before the reply), and
// then closes an open quote unless the reply already ends with it (close). Each line
// is read by a bash with history expansion on, as in an interactive shell. words is
// the word under the cursor as bash 3.2 and as bash 4+ split it into COMP_WORDS.
func TestCompletionBashQuoting(t *testing.T) {
	dir := completionDir(t)
	const script = `eval "$(made completion bash)"
keep=$1 close=$2 COMP_LINE=$3; COMP_POINT=${#COMP_LINE}; shift 3
COMP_WORDS=("$@"); COMP_CWORD=$(($# - 1)); _made
for r in "${COMPREPLY[@]}"; do
    line=$keep$r
    if [[ -n $close && ${r: -1} != "$close" ]]; then line=$line$close; fi
    out=$(printf 'set -o history -H\nset -- %s\nprintf "%%s\\t%%s\\n" "$#" "$1"\n' "$line" | bash 2>/dev/null)
    [[ -n $out ]] || out="error"$'\t'"$line"
    printf '%s\n' "$out"
done`
	t.Logf("bash %s", runShell(t, dir, "bash", "-c", `printf %s "$BASH_VERSION"`))

	type quoteCase struct {
		words       [][]string
		keep, close string
		want        []string
	}
	for _, tc := range []quoteCase{
		{words: [][]string{{""}}, want: quoteNames},
		{words: [][]string{{`with\ s`}}, want: []string{"with space"}},
		{words: [][]string{{"'with s"}}, keep: "'", close: "'", want: []string{"with space"}},
		{words: [][]string{{`"with s`}}, keep: `"`, close: `"`, want: []string{"with space"}},
		{words: [][]string{{"'it"}}, keep: "'", close: "'", want: []string{"it's"}},
		{words: [][]string{{`"say`}}, keep: `"`, close: `"`, want: []string{`say "hi"`}},
		{words: [][]string{{`"dol`}}, keep: `"`, close: `"`, want: []string{"dollar $(touch MARK)"}},
		{words: [][]string{{`"ban`}}, keep: `"`, close: `"`, want: []string{"bang!"}},
		{words: [][]string{{`\$\(i`}}, want: []string{"$(id)"}},
		{words: [][]string{{`\~r`}}, want: []string{"~root"}},
		{words: [][]string{{`back\\s`}}, want: []string{`back\slash`}},
		{words: [][]string{{"'ns:t"}, {"'ns", ":", "t"}}, keep: "'", close: "'", want: []string{"ns:task"}},
		{words: [][]string{{`ns\:t`}, {`ns\`, ":", "t"}}, want: []string{"ns:task"}},
		{words: [][]string{{"ns:t"}, {"ns", ":", "t"}}, keep: "ns:", want: []string{"ns:task"}},
		{words: [][]string{{"a="}, {"a", "="}, {"a", "=", ""}}, keep: "a=", want: []string{"a=b", "a=~root"}},
		{words: [][]string{{"'a="}, {"'a", "="}}, keep: "'", close: "'", want: []string{"a=b", "a=~root"}},
		{words: [][]string{{"x:"}, {"x", ":"}}, keep: "x:", want: []string{"x:~y"}},
		// Readline keeps an "@" in the text it replaces.
		{words: [][]string{{"user@h"}, {"user", "@", "h"}}, keep: "user", want: []string{"user@host"}},
		{words: [][]string{{"user@"}, {"user", "@"}, {"user", "@", ""}}, keep: "user", want: []string{"user@host"}},
		{words: [][]string{{`user\@h`}, {`user\`, "@", "h"}}, want: []string{"user@host"}},
		{words: [][]string{{"'user@h"}, {"'user", "@", "h"}}, keep: "'", close: "'", want: []string{"user@host"}},
	} {
		for _, words := range tc.words {
			compLine := "made -f quote.md " + strings.Join(words, "")
			args := append([]string{"-c", script, "bash", tc.keep, tc.close, compLine, "made", "-f", "quote.md"}, words...)
			got := lines(runShell(t, dir, "bash", args...))
			for _, g := range got {
				if !strings.HasPrefix(g, "1\t") {
					t.Errorf("%q: inserted completion is not one word: %q", compLine, g)
				}
			}
			for _, name := range tc.want {
				if n := strings.Count("\n"+strings.Join(got, "\n")+"\n", "\n1\t"+name+"\n"); n != 1 {
					t.Errorf("%q: %q offered %d times, want once; got %q", compLine, name, n, got)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "MARK")); err == nil {
		t.Error("an inserted completion ran a command substitution")
	}
	checkNoRun(t, dir)
}

// TestCompletionBashStrict checks that _made works in a shell with set -e and set -u,
// which some users run interactively: no statement may return non-zero or expand an
// unset variable, on bash 3.2 or bash 5. Each line is completed by a plain shell and by
// one with set -eu, which must survive, write nothing to stderr (an error inside $(...)
// only ends that subshell) and offer the same completions. noHome unsets HOME first.
func TestCompletionBashStrict(t *testing.T) {
	dir := completionDir(t)
	noMadefile := t.TempDir()
	mkdir(t, filepath.Join(noMadefile, ".git"))
	const script = `if [[ $2 == nohome ]]; then unset HOME; fi
if [[ $1 == strict ]]; then set -eu; fi
eval "$(made completion bash)"
COMP_LINE=$3; COMP_POINT=${#COMP_LINE}; shift 3
COMP_WORDS=("$@"); COMP_CWORD=$(($# - 1)); COMPREPLY=(); _made
printf '%s\n' ${COMPREPLY[@]+"${COMPREPLY[@]}"}
echo survived`

	for _, tc := range []struct {
		dir    string
		noHome bool
		completionCase
	}{
		{dir, false, completionCase{line: []string{"made", "b"}, want: []string{"build"}}},
		{dir, false, completionCase{line: []string{"made", "-f", "quote.md", "wi"}, want: []string{`with\ space`}}},
		{dir, false, completionCase{line: []string{"made", "-f", "quote.md", "'with s"}, want: []string{"with space"}}},
		{dir, false, completionCase{line: []string{"made", "-f", "quote.md", `"dol`}, want: []string{`dollar \$(touch MARK)`}}},
		{dir, false, completionCase{line: []string{"made", "-f", ""}, not: []string{"build"}}},
		{dir, false, completionCase{line: []string{"made", "-f", "other.md", ""}, want: []string{"ns:task"}}},
		{dir, false, completionCase{line: []string{"made", "--f"}, want: []string{"--file"}}},
		{dir, false, completionCase{line: []string{"made", "--zz"}}},
		{dir, false, completionCase{line: []string{"made", "completion", ""}, want: []string{"bash", "zsh", "fish"}}},
		{dir, false, completionCase{line: []string{"made", "completion", "q"}}},
		{dir, false, completionCase{line: []string{"made", "init", ""}, want: []string{"--force"}}},
		{dir, false, completionCase{line: []string{"made", "self-update", ""}, want: []string{"--dry-run"}}},
		{dir, false, completionCase{line: []string{"made", "self-update", "--dry-run", ""}}},
		{dir, false, completionCase{line: []string{"made", "--check", "--"}, want: []string{"--strict"}}},
		{dir, false, completionCase{line: []string{"made", "-f", "$PWD/other.md", ""}, want: []string{"ns:task"}}},
		{dir, false, completionCase{line: []string{"made", "--file=other.md", ""}, want: []string{"ns:task"}}},
		{dir, true, completionCase{line: []string{"made", "-f", "other.md", ""}, want: []string{"ns:task"}}},
		{dir, true, completionCase{line: []string{"made", "-f", "~/other.md", ""}, want: []string{"init"}, not: []string{"ns:task"}}},
		{noMadefile, false, completionCase{line: []string{"made", ""}, want: []string{"init", "completion"}}},
	} {
		compLine := strings.Join(tc.line, " ")
		home := ""
		if tc.noHome {
			home = "nohome"
		}
		t.Run(strings.TrimSpace(home+" "+compLine), func(t *testing.T) {
			plain := runShell(t, tc.dir, "bash", append([]string{"-c", script, "bash", "plain", home, compLine}, tc.line...)...)
			strict, stderr := runShellOut(t, tc.dir, "bash", append([]string{"-c", script, "bash", "strict", home, compLine}, tc.line...)...)
			if !strings.HasSuffix(strict, "survived\n") {
				t.Fatalf("%q: the set -eu shell did not finish: %q", compLine, strict)
			}
			if stderr != "" {
				t.Errorf("%q: the set -eu shell wrote to stderr: %q", compLine, stderr)
			}
			if strict != plain {
				t.Errorf("%q: completions under set -eu %q, want %q as without it", compLine, lines(strict), lines(plain))
			}
			checkCompletions(t, lines(strict), tc.completionCase)
		})
	}
	checkNoRun(t, dir)
}

// TestCompletionZsh checks the script parses and that _made_tasks offers the right
// tasks, with _describe stubbed to print its entries. Driving zsh's full completion
// system needs a terminal, so the _arguments wiring is checked by hand.
func TestCompletionZsh(t *testing.T) {
	dir := completionDir(t)
	runShell(t, dir, "zsh", "-f", "-c", `made completion zsh | zsh -n`)

	// The user variables rest, name, desc and REPLY share names with locals of the
	// script once, which hid them from the -f expansion.
	const script = `compdef() { :; }
eval "$(made completion zsh)"
rest=$PWD name=$PWD desc=$PWD REPLY=$PWD
_describe() { print -rl -- "${(@P)${@[-1]}}"; }
words=("$@"); CURRENT=$#
_made_tasks`
	for _, tc := range []completionCase{
		{line: []string{"made", ""}, want: []string{"say-hello:Prints a friendly greeting.", "build:Compiles all packages.", "init:create a starter Madefile.md", "self-update:update made to the latest release"}},
		{line: []string{"made", "-f", "other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}, not: []string{"build:Compiles all packages."}},
		{line: []string{"made", "--file=other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "--env-file", "-f", "--file=other.md", "--print", ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "'my tasks.md'", ""}, want: []string{`ns\:task:Has a colon\: here.`}, not: []string{"build:Compiles all packages."}},
		{line: []string{"made", `--file=my\ tasks.md`, ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		// _describe removes backslashes, so they are doubled.
		{line: []string{"made", "-f", "edge.md", ""}, want: []string{`back\\slash:has \\ a\: b`}, not: []string{":empty"}},
		// A leading $NAME or ${NAME} is expanded, outside single quotes only, and
		// nothing else on the line is evaluated. The test runs in $PWD.
		{line: []string{"made", "-f", "$PWD/other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}, not: []string{"build:Compiles all packages."}},
		{line: []string{"made", "-f", `"${PWD}/my tasks.md"`, ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", `"$PWD"/other.md`, ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", `"${PWD}"/other.md`, ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", `"my "tasks'.md'`, ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "$rest/other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "$name/other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "$desc/other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "--file=${REPLY}/other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "--file=$PWD/other.md", ""}, want: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "'$PWD/other.md'", ""}, not: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", `\$PWD/other.md`, ""}, not: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "${PWD:-x}/other.md", ""}, not: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", noRun, ""}, not: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "${PWD[$(touch PWNED)]}/other.md", ""}, not: []string{`ns\:task:Has a colon\: here.`}},
		{line: []string{"made", "-f", "$PWD[$(touch PWNED)]/other.md", ""}, not: []string{`ns\:task:Has a colon\: here.`}},
	} {
		got := lines(runShell(t, dir, "zsh", append([]string{"-f", "-c", script, "zsh"}, tc.line...)...))
		checkCompletions(t, got, tc)
	}
	checkNoRun(t, dir)
}

// TestCompletionFish uses fish's own `complete -C`, which prints what the shell
// would offer for a command line.
func TestCompletionFish(t *testing.T) {
	dir := completionDir(t)
	cases := []completionCase{
		{line: []string{"made "}, want: []string{"say-hello\tPrints a friendly greeting.", "build\tCompiles all packages.", "init\tCreate a starter Madefile.md"}},
		{line: []string{"made -f other.md "}, want: []string{"ns:task\tHas a colon: here."}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made --file=other.md "}, want: []string{"ns:task\tHas a colon: here."}},
		{line: []string{"made --env-file missing.env -f other.md --print "}, want: []string{"ns:task\tHas a colon: here."}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made --env-file=missing.env --dry-run "}, want: []string{"build\tCompiles all packages."}},
		{line: []string{"made --env-file (touch PWNED)/vars.env "}, want: []string{"build\tCompiles all packages."}},
		{line: []string{"made --env-file other"}, want: []string{"other.md"}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made -f 'my tasks.md' "}, want: []string{"ns:task\tHas a colon: here."}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made -f edge.md "}, want: []string{"back\\slash\thas \\ a: b"}, not: []string{"\tempty"}},
		{line: []string{"made --"}, want: []string{"--file\tMarkdown file to read tasks from", "--list\tList available tasks", "--check\tCheck the task file for problems", "--env-file\tLoad missing task environment variables", "--print\tPrint the command and script without running", "--dry-run\tPrint the command and script without running"}, not: []string{"--list-names"}},
		{line: []string{"made completion "}, want: []string{"bash", "zsh", "fish"}, not: []string{"build\tCompiles all packages."}},
		{line: []string{"made build "}, want: []string{"Madefile.md", "other.md"}, not: []string{"build\tCompiles all packages."}},
		// fish lists options only once a "-" is typed.
		{line: []string{"made init "}, not: []string{"build\tCompiles all packages.", "Madefile.md"}},
		{line: []string{"made init -"}, want: []string{"--force\tOverwrite an existing Madefile"}},
		{line: []string{"made init --force -"}, not: []string{"--force\tOverwrite an existing Madefile"}},
		{line: []string{"made "}, want: []string{"self-update\tUpdate made to the latest release"}},
		{line: []string{"made self-update "}, not: []string{"build\tCompiles all packages.", "Madefile.md", "self-update\tUpdate made to the latest release"}},
		{line: []string{"made self-update -"}, want: []string{"--dry-run\tPrint what would happen without changing anything"}},
		{line: []string{"made self-update v1.2.3 -"}, want: []string{"--dry-run\tPrint what would happen without changing anything"}},
		{line: []string{"made self-update --dry-run -"}, not: []string{"--dry-run\tPrint what would happen without changing anything"}},
		// --check takes no task, and --strict and --host need --check.
		{line: []string{"made --check "}, not: []string{"build\tCompiles all packages.", "init\tCreate a starter Madefile.md", "completion\tPrint a shell completion script", "self-update\tUpdate made to the latest release"}},
		{line: []string{"made --strict "}, not: []string{"build\tCompiles all packages.", "init\tCreate a starter Madefile.md"}},
		{line: []string{"made -host "}, not: []string{"build\tCompiles all packages.", "init\tCreate a starter Madefile.md"}},
		{line: []string{"made --check -"}, want: []string{"--strict\tWith --check, fail on warnings too", "--file\tMarkdown file to read tasks from"}, not: []string{"--list\tList available tasks", "--version\tPrint version and exit", "--env-file\tLoad missing task environment variables", "--print\tPrint the command and script without running"}},
		{line: []string{"made -f " + noRun + " "}, not: []string{"ns:task\tHas a colon: here."}},
		{line: []string{"made -f (touch PWNED)/other.md "}, not: []string{"ns:task\tHas a colon: here."}},
	}
	// fish 4 expands the -f value with its own rules (commandline -x); older fish
	// only removes its quoting.
	if fishMajor(t, dir) >= 4 {
		cases = append(cases,
			completionCase{line: []string{"made -f $PWD/other.md "}, want: []string{"ns:task\tHas a colon: here."}, not: []string{"build\tCompiles all packages."}},
			completionCase{line: []string{`made -f "$PWD/my tasks.md" `}, want: []string{"ns:task\tHas a colon: here."}},
			completionCase{line: []string{"made --file=$PWD/other.md "}, want: []string{"ns:task\tHas a colon: here."}},
			completionCase{line: []string{"made -f '$PWD/other.md' "}, not: []string{"ns:task\tHas a colon: here."}},
		)
	}
	for _, tc := range cases {
		got := lines(runShell(t, dir, "fish", "-N", "-c", `made completion fish | source; complete -C "$argv[1]"`, tc.line[0]))
		checkCompletions(t, got, tc)
	}
	checkNoRun(t, dir)
}

// fishMajor returns the major version of fish, from "fish, version 4.0.2".
func fishMajor(t *testing.T, dir string) int {
	t.Helper()
	out := runShell(t, dir, "fish", "--version")
	v := out[strings.LastIndex(out, " ")+1:]
	major, err := strconv.Atoi(v[:strings.IndexByte(v+".", '.')])
	if err != nil {
		t.Fatalf("fish --version = %q: %v", out, err)
	}
	return major
}
