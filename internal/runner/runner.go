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
	"sync"
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
// when it has one, otherwise through pids, the interpreter's descendants recorded during
// teardown so the same set can be force-killed after they are orphaned. sig is
// the signal that cancelled the task, if known.
type teardown struct {
	grouped bool
	pids    []int
	sig     os.Signal
}

// record adds the pids not recorded yet to td.
func (td *teardown) record(pids []int) {
	for _, pid := range pids {
		if !slices.Contains(td.pids, pid) {
			td.pids = append(td.pids, pid)
		}
	}
}

// runningInterpreters holds the pids of the interpreters Run has started and not yet
// returned from, so they are not taken for orphans of a task (see orphans), which on
// Linux are made's children too.
var (
	runningMu           sync.Mutex
	runningInterpreters = map[int]bool{}
)

// setInterpreter records pid as a running interpreter, or forgets it.
func setInterpreter(pid int, on bool) {
	runningMu.Lock()
	defer runningMu.Unlock()
	if on {
		runningInterpreters[pid] = true
	} else {
		delete(runningInterpreters, pid)
	}
}

// isInterpreter reports whether pid is an interpreter Run is running.
func isInterpreter(pid int) bool {
	runningMu.Lock()
	defer runningMu.Unlock()
	return runningInterpreters[pid]
}

// SignalHandledError is returned by Run when the task, cancelled by a signal the
// terminal sent to all its processes (Ctrl-C, Ctrl-\), exited by itself within the
// grace period instead of being torn down. Err is the task's own result (nil when it
// exited 0), which the caller can report as a shell would.
type SignalHandledError struct {
	Err error
}

func (e *SignalHandledError) Error() string {
	if e.Err == nil {
		return "task exited on its own after the signal"
	}
	return e.Err.Error()
}

func (e *SignalHandledError) Unwrap() error { return e.Err }

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
// fence keeps working. fish has no errexit option (a fish task continues past
// failures) and non-shell interpreters fail on an uncaught error, so only bash, sh,
// and zsh get strict-mode options.
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
// sent SIGTERM, and whatever outlives the grace period is sent SIGKILL. When the
// cancelling signal (see cancelSignal) may have come from the terminal on stdin, which
// sends SIGINT and SIGQUIT to its whole foreground group, nothing is signaled during a
// first grace period, so the task's processes handle Ctrl-C as if they had been run
// directly; whatever is still running after it is torn down as above. A task that exits
// by itself then returns a *SignalHandledError wrapping its result.
// Closing force during the grace period sends SIGKILL right away instead of waiting
// killGrace; a nil force never fires.
//
// A script containing a NUL byte cannot be passed as an argument and returns an error
// before anything runs. On Linux the interpreter is also killed if made itself dies.
func Run(ctx context.Context, force <-chan struct{}, t parser.Task, dir string, args []string) error {
	lang := NormalizeLang(t.Lang)
	in, ok := interpreters[lang]
	if !ok {
		return unsupportedError(t)
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
			return fmt.Errorf("task %q: script is %d %s, too large to pass to %q as a command-line argument (the OS limit is about %d %s); move the bulk of it into a file the task runs: %w",
				t.Name, ScriptLen(t.Lang, t.Script), ArgUnit, filepath.Base(cmd.Path), ArgLimit, ArgUnit, err)
		}
		return startError(err, cmd, cands, t)
	}

	setInterpreter(cmd.Process.Pid, true)
	defer setInterpreter(cmd.Process.Pid, false)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		td.sig = cancelSignal(ctx)
		if fromTerminal(td) {
			return awaitTerminalSignal(cmd, td, done, force)
		}
		return tearDown(cmd, td, done, force)
	}
}

// tearDown stops a cancelled task: SIGTERM, then SIGKILL for whatever outlives the
// grace period or right away once force is closed. It returns the interpreter's result.
func tearDown(cmd *exec.Cmd, td *teardown, done <-chan error, force <-chan struct{}) error {
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

// awaitTerminalSignal handles a cancel by a signal the terminal already sent to every
// process of the task (see fromTerminal): nothing is sent during the grace period, so
// the task handles it as if run directly, while its descendants are recorded. A task
// still running after the grace period is torn down; one whose interpreter exited
// returns a *SignalHandledError, once its surviving descendants are gone or, after the
// grace period, have been sent SIGTERM and, after another one, SIGKILL.
func awaitTerminalSignal(cmd *exec.Cmd, td *teardown, done <-chan error, force <-chan struct{}) error {
	deadline := time.After(killGrace)
	for {
		trackDescendants(cmd, td)
		select {
		case err := <-done:
			awaitSurvivors(cmd, td, deadline, force)
			if survivors(cmd, td) {
				signalTermSurvivors(cmd, td)
				awaitSurvivors(cmd, td, time.After(killGrace), force)
				signalKill(cmd, td)
			}
			return &SignalHandledError{Err: err}
		case <-force:
			trackDescendants(cmd, td)
			signalKill(cmd, td)
			return <-done
		case <-deadline:
			return tearDown(cmd, td, done, force)
		case <-time.After(survivorPoll):
		}
	}
}

// awaitSurvivors returns once no member of the cancelled task outlives its interpreter,
// or when deadline fires or force is closed. Meanwhile it keeps recording the processes
// the survivors start (see trackSurvivors).
func awaitSurvivors(cmd *exec.Cmd, td *teardown, deadline <-chan time.Time, force <-chan struct{}) {
	for trackSurvivors(cmd, td); survivors(cmd, td); trackSurvivors(cmd, td) {
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

// unsupportedError is the error for a task whose fence language Run does not accept.
func unsupportedError(t parser.Task) error {
	return fmt.Errorf("unsupported language %q; supported: %s", t.Lang, supportedLanguages())
}

// supportedLanguages returns the named languages, sorted, for error messages; the
// empty default is omitted.
func supportedLanguages() string {
	return strings.Join(Languages(), ", ")
}

// NormalizeLang folds a fence language the way Run and Supported match it: trimmed
// and lowercased.
func NormalizeLang(lang string) string {
	return strings.ToLower(strings.TrimSpace(lang))
}

// Supported reports whether Run accepts a fence language. Matching ignores case and
// surrounding whitespace; the empty language is supported (it runs with bash).
func Supported(lang string) bool {
	_, ok := interpreters[NormalizeLang(lang)]
	return ok
}

// Languages returns the supported fence languages, sorted, without the empty default.
func Languages() []string {
	keys := make([]string, 0, len(interpreters))
	for k := range interpreters {
		if k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
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

// Resolve reports whether the task's interpreter can be found on PATH the way Run
// looks for it, without running anything. It returns nil when it is found, the error
// Run would return when the interpreter is missing or not executable (an
// *InterpreterError), or an unsupported-language error. An interpreter that is found
// can still fail to start, e.g. through a broken #! line.
func Resolve(t parser.Task) error {
	lang := NormalizeLang(t.Lang)
	in, ok := interpreters[lang]
	if !ok {
		return unsupportedError(t)
	}
	cands := append([]string{in.cmd}, fallbacks[lang]...)
	_, err := exec.LookPath(pickInterpreter(cands))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, exec.ErrNotFound):
		return notFoundError(cands, t)
	default:
		return err
	}
}

// ScriptLen returns the size of a script in the fence language lang once passed to its
// interpreter, in ArgUnit as the OS counts it against ArgLimit: it includes the pad
// added to a script starting with "-" or "+". An unsupported language gets no pad.
func ScriptLen(lang, script string) int {
	if in, ok := interpreters[NormalizeLang(lang)]; ok {
		script = inlineScript(in, script)
	}
	return argLen(script)
}

// ScriptTooLarge reports whether ScriptLen is at least ArgLimit, so Run cannot start
// the script on Linux or Windows. It is false for an unsupported language.
func ScriptTooLarge(lang, script string) bool {
	return Supported(lang) && ScriptLen(lang, script) >= ArgLimit
}

// startError turns a cmd.Start failure into an *InterpreterError when the interpreter
// was missing, not executable, or could not be executed; other errors pass through.
func startError(err error, cmd *exec.Cmd, cands []string, t parser.Task) error {
	if errors.Is(err, exec.ErrNotFound) {
		return notFoundError(cands, t)
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Op == "fork/exec" {
		return &InterpreterError{Cmd: filepath.Base(cmd.Args[0]), Path: cmd.Path, Task: t.Name, Lang: t.Lang, Err: pathErr.Err}
	}
	return err
}

// notFoundError returns the *InterpreterError for none of cands being on PATH: a
// not-executable one when a file named after a candidate is on PATH anyway.
func notFoundError(cands []string, t parser.Task) *InterpreterError {
	for _, c := range cands {
		if p := findNonExecutable(c); p != "" {
			return &InterpreterError{Cmd: c, Path: p, Task: t.Name, Lang: t.Lang, Err: fs.ErrPermission, NotExecutable: true}
		}
	}
	return &InterpreterError{Cmd: cands[0], Fallbacks: cands[1:], Task: t.Name, Lang: t.Lang, Err: exec.ErrNotFound}
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
