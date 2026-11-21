package runner

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/testenv"
)

// TestRunnerDoesNotDependOnParser pins the layering: the runner takes a task.Task
// and must not pull in the Markdown parser, on any target OS.
func TestRunnerDoesNotDependOnParser(t *testing.T) {
	testenv.RequireCmd(t, "go")
	const (
		module = "github.com/Tarektouati/made/"
		self   = module + "internal/runner"
		parser = module + "internal/parser"
	)
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			// -e keeps listing when a module only another GOOS needs is missing from the
			// cache and GOPROXY=off forbids fetching it. Only packages of this module can
			// import internal/parser, so only their errors matter.
			cmd := exec.CommandContext(t.Context(), "go", "list", "-e", "-deps", "-f", "{{.ImportPath}}\t{{with .Error}}{{.Err}}{{end}}", ".")
			// GOPROXY=off and GOTOOLCHAIN=local keep go list off the network.
			cmd.Env = append(os.Environ(), "GOOS="+goos, "GOPROXY=off", "GOTOOLCHAIN=local")
			out, err := cmd.Output()
			if err != nil {
				var stderr []byte
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					stderr = exitErr.Stderr
				}
				t.Fatalf("go list -e -deps: %v\n%s", err, stderr)
			}
			var deps []string
			for line := range strings.Lines(string(out)) {
				path, loadErr, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
				if loadErr != "" && strings.HasPrefix(path, module) {
					t.Errorf("go list -e -deps: %s: %s", path, loadErr)
				}
				deps = append(deps, path)
			}
			if !slices.Contains(deps, self) {
				t.Fatalf("go list -e -deps output lacks %s:\n%s", self, out)
			}
			if slices.Contains(deps, parser) {
				t.Errorf("%s depends on %s", self, parser)
			}
		})
	}
}
