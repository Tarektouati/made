// Package runner executes a parsed task's script.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Tarektouati/made/internal/parser"
)

// killGrace is how long a cancelled task's process group is given to exit on SIGTERM
// before it is force-killed with SIGKILL.
const killGrace = 3 * time.Second

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
	"":           {"bash", strictPipefail, "-c", shellSep},
	"bash":       {"bash", strictPipefail, "-c", shellSep},
	"sh":         {"sh", strictPOSIX, "-c", shellSep},
	"shell":      {"bash", strictPipefail, "-c", shellSep},
	"zsh":        {"zsh", strictPipefail, "-c", shellSep},
	"fish":       {"fish", nil, "-c", endOfOpts},
	"python":     {"python3", nil, "-c", nil},
	"python3":    {"python3", nil, "-c", nil},
	"node":       {"node", nil, "-e", endOfOpts},
	"js":         {"node", nil, "-e", endOfOpts},
	"javascript": {"node", nil, "-e", endOfOpts},
	"ruby":       {"ruby", nil, "-e", endOfOpts},
	"perl":       {"perl", nil, "-e", endOfOpts},
}

// Run executes the task's script with the interpreter chosen by the code-fence
// language (defaulting to bash for a plain fence), inheriting the current process's
// stdin/stdout/stderr and environment. The script runs in dir; an empty dir inherits
// the current process's working directory. An unsupported language
// returns an error before anything runs, and an interpreter missing from PATH returns
// an error naming it, the task, and its language (wrapping exec.ErrNotFound). A
// failing script returns an *exec.ExitError whose ExitCode the caller can propagate.
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
// Cancelling ctx (e.g. on SIGINT) tears down the task's whole process group, not just
// the interpreter, so spawned grandchildren are not orphaned: the group is sent SIGTERM
// and then SIGKILL.
func Run(ctx context.Context, t parser.Task, dir string, args []string) error {
	in, ok := interpreters[strings.ToLower(strings.TrimSpace(t.Lang))]
	if !ok {
		return fmt.Errorf("unsupported language %q; supported: %s", t.Lang, supportedLanguages())
	}

	argv := append(slices.Clone(in.opts), in.flag, t.Script)
	argv = append(argv, in.argSep...)
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, in.cmd, argv...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// We tear down the process group ourselves on cancellation; neutralize os/exec's
	// built-in kill, which would target only the interpreter and orphan its children.
	cmd.Cancel = func() error { return nil }
	grouped := configureProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%q was not found on PATH (required by task %q, language %s); install it or change the code fence language: %w",
				in.cmd, t.Name, langLabel(t.Lang), exec.ErrNotFound)
		}
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		signalTerm(cmd, grouped)
		select {
		case err := <-done:
			// Leader exited on SIGTERM; force-kill any group member that survived it
			// (ESRCH is harmless if the group is already empty).
			signalKill(cmd, grouped)
			return err
		case <-time.After(killGrace):
			signalKill(cmd, grouped)
			return <-done
		}
	}
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
