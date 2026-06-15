// Package runner executes a parsed task's script.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	cmd  string // executable to run
	flag string // flag that precedes the inline script (e.g. -c, -e)
}

// interpreters maps a fenced code block's language to its interpreter. The language
// is matched case-insensitively; the empty language defaults to bash so a plain
// fence keeps working.
var interpreters = map[string]interpreter{
	"":           {"bash", "-c"},
	"bash":       {"bash", "-c"},
	"sh":         {"sh", "-c"},
	"shell":      {"bash", "-c"},
	"zsh":        {"zsh", "-c"},
	"fish":       {"fish", "-c"},
	"python":     {"python3", "-c"},
	"python3":    {"python3", "-c"},
	"node":       {"node", "-e"},
	"js":         {"node", "-e"},
	"javascript": {"node", "-e"},
	"ruby":       {"ruby", "-e"},
	"perl":       {"perl", "-e"},
}

// Run executes the task's script with the interpreter chosen by the code-fence
// language (defaulting to bash for a plain fence), inheriting the current process's
// stdin/stdout/stderr, environment, and working directory. An unsupported language
// returns an error before anything runs. A failing script returns an *exec.ExitError
// whose ExitCode the caller can propagate.
//
// Cancelling ctx (e.g. on SIGINT) tears down the task's whole process group, not just
// the interpreter, so spawned grandchildren are not orphaned: the group is sent SIGTERM
// and then SIGKILL.
func Run(ctx context.Context, t parser.Task) error {
	in, ok := interpreters[strings.ToLower(strings.TrimSpace(t.Lang))]
	if !ok {
		return fmt.Errorf("unsupported language %q; supported: %s", t.Lang, supportedLanguages())
	}

	cmd := exec.CommandContext(ctx, in.cmd, in.flag, t.Script)
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
