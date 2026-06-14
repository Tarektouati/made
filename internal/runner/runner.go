// Package runner executes a parsed task's script.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/Tarektouati/made/internal/parser"
)

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
// whose ExitCode the caller can propagate. Cancelling ctx (e.g. on SIGINT) kills the
// spawned process.
func Run(ctx context.Context, t parser.Task) error {
	in, ok := interpreters[strings.ToLower(strings.TrimSpace(t.Lang))]
	if !ok {
		return fmt.Errorf("unsupported language %q; supported: %s", t.Lang, supportedLanguages())
	}
	cmd := exec.CommandContext(ctx, in.cmd, in.flag, t.Script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
