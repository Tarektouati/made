// Package runner executes a parsed task's script.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Tarektouati/made/internal/parser"
)

// killGrace is how long a cancelled task is given to exit on SIGTERM before whatever is
// left of it is force-killed with SIGKILL.
const killGrace = 3 * time.Second

// survivorPoll is how often a cancelled task is checked for members that outlived the
// interpreter during the grace period.
const survivorPoll = 50 * time.Millisecond

// teardown records how a running task is torn down on cancellation: as a process group
// when it has one, otherwise through pids, the interpreter's descendants captured when
// SIGTERM is sent so the same set can be force-killed after they are orphaned. sig is
// the signal that cancelled the task, if known.
type teardown struct {
	grouped bool
	pids    []int
	sig     os.Signal
}

// cancelSignal returns the signal that cancelled ctx, or nil. It is found through the
// context's cause: an error in its chain with a Signal() os.Signal method.
func cancelSignal(ctx context.Context) os.Signal {
	var s interface{ Signal() os.Signal }
	if errors.As(context.Cause(ctx), &s) {
		return s.Signal()
	}
	return nil
}

// interpreter is the program that runs a script passed as a single inline argument.
type interpreter struct {
	cmd  string   // executable to run
	opts []string // options passed before flag (e.g. strict-mode shell options)
	flag string   // flag that precedes the inline script (e.g. -c, -e)
	// argSep is always inserted between the script and the task arguments: "made" for
	// shells (it becomes $0, so the first argument is $1, with or without arguments), and
	// "--" for interpreters that keep parsing options after the script (fish, node, ruby,
	// perl) and would otherwise take dash-prefixed arguments as their own options. python
	// needs none: -c ends its option parsing.
	argSep []string
	// noPad disables the leading space inlineScript adds to a script starting with "-"
	// or "+", for interpreters where leading whitespace is significant (python).
	noPad bool
}

// Strict-mode options for shell interpreters, so a multi-line task stops at the first
// failing command instead of reporting only the last command's status. They are passed
// as interpreter options rather than prepended to the script, which keeps the script's
// line numbers intact. A script can opt out with `set +e` / `set +o pipefail`.
var (
	// strictPipefail is errexit plus pipefail, for shells that support pipefail.
	strictPipefail = []string{"-e", "-o", "pipefail"}
	// strictPOSIX is errexit only: pipefail is not portable across sh implementations
	// (e.g. older dash).
	strictPOSIX = []string{"-e"}
)

// Argument separators (see interpreter.argSep).
var (
	shellSep  = []string{"made"}
	endOfOpts = []string{"--"}
)

// interpreters maps a fenced code block's language to its interpreter. The language
// is matched case-insensitively; the empty language defaults to bash so a plain
// fence keeps working. fish has no errexit option and non-shell interpreters already
// fail on an uncaught error, so only bash, sh, and zsh get strict-mode options.
var interpreters = map[string]interpreter{
	"":           {"bash", strictPipefail, "-c", shellSep, false},
	"bash":       {"bash", strictPipefail, "-c", shellSep, false},
	"sh":         {"sh", strictPOSIX, "-c", shellSep, false},
	"shell":      {"bash", strictPipefail, "-c", shellSep, false},
	"zsh":        {"zsh", strictPipefail, "-c", shellSep, false},
	"fish":       {"fish", nil, "-c", endOfOpts, false},
	"python":     {"python3", nil, "-c", nil, true},
	"python3":    {"python3", nil, "-c", nil, true},
	"node":       {"node", nil, "-e", endOfOpts, false},
	"js":         {"node", nil, "-e", endOfOpts, false},
	"javascript": {"node", nil, "-e", endOfOpts, false},
	"ruby":       {"ruby", nil, "-e", endOfOpts, false},
	"perl":       {"perl", nil, "-e", endOfOpts, false},
}

// fallbacks lists interpreters tried in order when a language's interpreter is not on
// PATH. Only the "python" fence falls back; "python3" stays explicit. The fallback
// python may be Python 2 on older systems; a python3 fence never runs it.
var fallbacks = map[string][]string{"python": {"python"}}

// Run executes the task's script with the interpreter chosen by the code-fence
// language (defaulting to bash for a plain fence), inheriting the current process's
// stdin/stdout/stderr and environment. The script runs in dir; an empty dir inherits
// the current process's working directory. An unsupported language
// returns an error before anything runs. An interpreter that cannot be started returns
// an *InterpreterError naming it, the task, and its language; it wraps exec.ErrNotFound
// when the interpreter is not on PATH and fs.ErrPermission when a file of that name is
// on PATH but not executable. The python fence falls back to python when python3 is
// not on PATH. A script too large for the OS argument limit returns an error giving
// its size, before anything runs. A failing script returns an *exec.ExitError whose
// ExitCode the caller can propagate.
//
// Shell tasks run in strict mode: bash and zsh with errexit and pipefail, sh with
// errexit only. A failing command therefore stops the task with its exit code rather
// than being masked by a later successful one. A script that wants the shell's lenient
// defaults opts out itself with `set +e` (and `set +o pipefail`).
//
// Any args are passed to the script as positional parameters: $1, "$@" in shells,
// $argv in fish, sys.argv[1:] in python, process.argv.slice(1) in node, ARGV in ruby,
// and @ARGV in perl.
//
// Cancelling ctx (e.g. on SIGINT) tears down the whole task, not just the interpreter,
// so spawned grandchildren are not orphaned: the task's process group, or when stdin is
// a terminal (and the task shares made's group) the interpreter and its descendants, is
// sent SIGTERM, and whatever outlives the grace period is sent SIGKILL. When stdin is a
// terminal and the cancelling signal (see cancelSignal) is one the terminal itself sends
// to the whole foreground group (SIGINT, SIGQUIT), only the interpreter is signaled, so
// the task's processes handle Ctrl-C as if they had been run directly.
// Closing force during the grace period sends SIGKILL right away instead of waiting
// killGrace; a nil force never fires.
//
// A script containing a NUL byte cannot be passed as an argument and returns an error
// before anything runs. On Linux the interpreter is also killed if made itself dies.
func Run(ctx context.Context, force <-chan struct{}, t parser.Task, dir string, args []string) error {
	lang := strings.ToLower(strings.TrimSpace(t.Lang))
	in, ok := interpreters[lang]
	if !ok {
		return fmt.Errorf("unsupported language %q; supported: %s", t.Lang, supportedLanguages())
	}
	if i := strings.IndexByte(t.Script, 0); i >= 0 {
		return fmt.Errorf("task %q: script contains a NUL byte (line %d of the script)", t.Name, strings.Count(t.Script[:i], "\n")+1)
	}

	argv := append(slices.Clone(in.opts), in.flag, inlineScript(in, t.Script))
	argv = append(argv, in.argSep...)
	argv = append(argv, args...)
	cands := append([]string{in.cmd}, fallbacks[lang]...)
	cmd := exec.CommandContext(ctx, pickInterpreter(cands), argv...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// We tear down the process group ourselves on cancellation; neutralize os/exec's
	// built-in kill, which would target only the interpreter and orphan its children.
	cmd.Cancel = func() error { return nil }
	td := &teardown{grouped: configureProcessGroup(cmd)}
	setParentDeathSignal(cmd)

	if err := cmd.Start(); err != nil {
		if argListTooLong(err) {
			return fmt.Errorf("task %q: script is %d bytes, too large to pass to %q as a command-line argument (the OS limit is about %d bytes); move the bulk of it into a file the task runs: %w",
				t.Name, len(t.Script), filepath.Base(cmd.Path), argLimit, err)
		}
		return startError(err, cmd, cands, t)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		td.sig = cancelSignal(ctx)
		signalTerm(cmd, td)
		deadline := time.After(killGrace)
		select {
		case err := <-done:
			// Leader exited on SIGTERM; the rest of the task keeps the remainder of the
			// grace period before any survivor is force-killed.
			awaitSurvivors(cmd, td, deadline, force)
			signalKill(cmd, td)
			return err
		case <-force:
			signalKill(cmd, td)
			return <-done
		case <-deadline:
			signalKill(cmd, td)
			return <-done
		}
	}
}

// awaitSurvivors returns once no member of the cancelled task outlives its interpreter,
// or when deadline fires or force is closed.
func awaitSurvivors(cmd *exec.Cmd, td *teardown, deadline <-chan time.Time, force <-chan struct{}) {
	for survivors(cmd, td) {
		select {
		case <-deadline:
			return
		case <-force:
			return
		case <-time.After(survivorPoll):
		}
	}
}

// inlineScript returns the script as passed after the interpreter's inline flag. A
// script starting with "-" or "+" gets a leading space so the interpreter doesn't parse
// it as an option (shells read both as option prefixes), unless in.noPad is set.
func inlineScript(in interpreter, script string) string {
	if !in.noPad && (strings.HasPrefix(script, "-") || strings.HasPrefix(script, "+")) {
		return " " + script
	}
	return script
}

// supportedLanguages returns the named languages, sorted, for error messages; the
// empty default is omitted.
func supportedLanguages() string {
	keys := make([]string, 0, len(interpreters))
	for k := range interpreters {
		if k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// langLabel describes a fence language for error messages, naming the bash default
// when the fence has no language.
func langLabel(lang string) string {
	if l := strings.TrimSpace(lang); l != "" {
		return fmt.Sprintf("%q", l)
	}
	return `"bash" (default for a plain fence)`
}

// InterpreterError reports that a task's interpreter could not be started.
type InterpreterError struct {
	Cmd       string   // interpreter name, e.g. "python3"
	Fallbacks []string // interpreters also tried when Cmd was not on PATH
	Path      string   // resolved or found path; empty when nothing was found
	Task      string
	Lang      string // raw fence language
	Err       error  // exec.ErrNotFound, fs.ErrPermission, or the start errno
	// NotExecutable is set when a file named Cmd is on PATH without execute
	// permission. A start errno such as EACCES (e.g. a noexec mount) also matches
	// fs.ErrPermission but leaves this false.
	NotExecutable bool
}

func (e *InterpreterError) Error() string {
	req := fmt.Sprintf("required by task %q, language %s", e.Task, langLabel(e.Lang))
	switch {
	case errors.Is(e.Err, exec.ErrNotFound):
		if len(e.Fallbacks) > 0 {
			names := make([]string, 0, 1+len(e.Fallbacks))
			for _, n := range append([]string{e.Cmd}, e.Fallbacks...) {
				names = append(names, fmt.Sprintf("%q", n))
			}
			return fmt.Sprintf("neither %s was found on PATH (%s); install one or change the code fence language",
				strings.Join(names, " nor "), req)
		}
		return fmt.Sprintf("%q was not found on PATH (%s); install it or change the code fence language", e.Cmd, req)
	case e.NotExecutable:
		return fmt.Sprintf("%q at %s is not executable (%s); make it executable or fix PATH", e.Cmd, e.Path, req)
	case errors.Is(e.Err, fs.ErrNotExist):
		return fmt.Sprintf("cannot run %q at %s (%s): %v; check its #! line", e.Cmd, e.Path, req, e.Err)
	default:
		return fmt.Sprintf("cannot run %q at %s (%s): %v", e.Cmd, e.Path, req, e.Err)
	}
}

func (e *InterpreterError) Unwrap() error { return e.Err }

// ExitCode follows the shell convention: 127 when the interpreter is not found, 126
// when it is found but cannot be executed.
func (e *InterpreterError) ExitCode() int {
	if errors.Is(e.Err, exec.ErrNotFound) {
		return 127
	}
	return 126
}

// pickInterpreter returns the first of cands that is on PATH, or cands[0] if none is.
func pickInterpreter(cands []string) string {
	if len(cands) > 1 {
		for _, c := range cands {
			if _, err := exec.LookPath(c); err == nil {
				return c
			}
		}
	}
	return cands[0]
}

// startError turns a cmd.Start failure into an *InterpreterError when the interpreter
// was missing, not executable, or could not be executed; other errors pass through.
func startError(err error, cmd *exec.Cmd, cands []string, t parser.Task) error {
	ie := &InterpreterError{Cmd: cands[0], Fallbacks: cands[1:], Task: t.Name, Lang: t.Lang, Err: err}
	if errors.Is(err, exec.ErrNotFound) {
		ie.Err = exec.ErrNotFound
		for _, c := range cands {
			if p := findNonExecutable(c); p != "" {
				ie.Cmd, ie.Fallbacks, ie.Path, ie.Err, ie.NotExecutable = c, nil, p, fs.ErrPermission, true
				break
			}
		}
		return ie
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Op == "fork/exec" {
		ie.Cmd, ie.Fallbacks, ie.Path, ie.Err = filepath.Base(cmd.Args[0]), nil, cmd.Path, pathErr.Err
		return ie
	}
	return err
}

// findNonExecutable returns the first regular file named name in a PATH directory, or
// "" if there is none. exec.LookPath skips such files when they are not executable.
func findNonExecutable(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}
