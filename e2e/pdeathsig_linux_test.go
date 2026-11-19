package e2e

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSIGKILLStopsInterpreter checks that the interpreter dies with made, even though
// SIGKILL gives made no chance to tear the task down itself.
func TestSIGKILLStopsInterpreter(t *testing.T) {
	requireBash(t)
	// exec keeps the pid, so the cleanup's kill leaves no sleep running.
	dir := writeTasks(t, "Madefile.md", "### sleeper\n```bash\necho $$ > pid\necho started\nexec sleep 30\n```\n")
	cmd, waitErr := startMade(t, dir, "sleeper")

	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", b, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL made: %v", err)
	}
	if werr := <-waitErr; werr == nil {
		t.Error("made exited 0 after SIGKILL")
	}

	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("interpreter (pid %d) still running 5s after made was SIGKILLed", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// alive reports whether pid exists and is not a zombie waiting to be reaped.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the parenthesized command name: "pid (comm) S ...".
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}
