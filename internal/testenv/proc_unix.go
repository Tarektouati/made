//go:build unix

package testenv

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// ReadPids reads the whitespace-separated pids a task script wrote to path. It fails
// the test when path cannot be read, holds something else, or holds no pid, so a check
// over the pids cannot pass vacuously.
func ReadPids(t testing.TB, path string) []int {
	t.Helper()
	// #nosec G304 -- The PID file is written by a test task in its temporary directory.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("parse pid %q in %s: %v", f, path, err)
			return nil
		}
		pids = append(pids, pid)
	}
	if len(pids) == 0 {
		t.Fatalf("%s has no pids", path)
	}
	return pids
}

// Alive reports whether pid exists and is not a zombie, in any process group.
func Alive(t testing.TB, pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// #nosec G204 -- ps receives a decimal PID as a separate argument, without a shell.
	out, err := exec.CommandContext(t.Context(), "ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && !strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
