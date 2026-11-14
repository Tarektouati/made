// Package runner executes a parsed task's script.
package runner

import (
	"context"
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

// interpreters maps a fenced code block's language to its interpreter. The language
// is matched case-insensitively; the empty language defaults to bash so a plain
// fence keeps working. fish has no errexit option and non-shell interpreters already
// fail on an uncaught error, so only bash, sh, and zsh get strict-mode options.
var interpreters = map[string]interpreter{
	"":           {"bash", strictPipefail, "-c"},
	"bash":       {"bash", strictPipefail, "-c"},
	"sh":         {"sh", strictPOSIX, "-c"},
	"shell":      {"bash", strictPipefail, "-c"},
	"zsh":        {"zsh", strictPipefail, "-c"},
	"fish":       {"fish", nil, "-c"},
	"python":     {"python3", nil, "-c"},
	"python3":    {"python3", nil, "-c"},
	"node":       {"node", nil, "-e"},
	"js":         {"node", nil, "-e"},
	"javascript": {"node", nil, "-e"},
	"ruby":       {"ruby", nil, "-e"},
	"perl":       {"perl", nil, "-e"},
}

// Run executes the task's script with the interpreter chosen by the code-fence
// language (defaulting to bash for a plain fence), inheriting the current process's
// stdin/stdout/stderr and environment. The script runs in dir; an empty dir inherits
// the current process's working directory. An unsupported language
// returns an error before anything runs. A failing script returns an *exec.ExitError
// whose ExitCode the caller can propagate.
//
// Shell tasks run in strict mode: bash and zsh with errexit and pipefail, sh with
// errexit only. A failing command therefore stops the task with its exit code rather
// than being masked by a later successful one. A script that wants the shell's lenient
// defaults opts out itself with `set +e` (and `set +o pipefail`).
//
// Cancelling ctx (e.g. on SIGINT) tears down the task's whole process group, not just
// the interpreter, so spawned grandchildren are not orphaned: the group is sent SIGTERM
// and then SIGKILL.
func Run(ctx context.Context, t parser.Task, dir string) error {
	in, ok := interpreters[strings.ToLower(strings.TrimSpace(t.Lang))]
	if !ok {
		return fmt.Errorf("unsupported language %q; supported: %s", t.Lang, supportedLanguages())
	}

	args := append(slices.Clone(in.opts), in.flag, t.Script)
	cmd := exec.CommandContext(ctx, in.cmd, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// We tear down the process group ourselves on cancellation; neutralize os/exec's
	// built-in kill, which would target only the interpreter and orphan its children.
	cmd.Cancel = func() error { return nil }
	grouped := configureProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
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
