// Package testenv holds helpers shared by made's tests.
package testenv

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// RequireEnv names the environment variable listing, space-separated, the commands
// RequireCmd must find. CI sets it so that a missing interpreter fails the run instead
// of skipping its tests unnoticed.
const RequireEnv = "MADE_TEST_REQUIRE"

// RequireCmd skips the test when name is not on PATH, so interpreter-specific tests
// stay green on machines lacking that interpreter, and fails it instead when name is
// listed in RequireEnv.
func RequireCmd(t testing.TB, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err == nil {
		return
	}
	if slices.Contains(strings.Fields(os.Getenv(RequireEnv)), name) {
		t.Fatalf("%s not found on PATH, but %s requires it", name, RequireEnv)
		return
	}
	t.Skipf("%s not found on PATH; skipping", name)
}
