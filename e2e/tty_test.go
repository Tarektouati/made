//go:build darwin || linux

package e2e

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSIGTERMOnTTYKillsDescendants sends SIGTERM to made alone, from outside, while it
// runs a task on a real terminal, and checks that the task's background children are
// gone once made exits. On a terminal the task shares made's process group, so this
// covers the descendant-tree teardown rather than the process-group one.
func TestSIGTERMOnTTYKillsDescendants(t *testing.T) {
	requireBash(t)
	tests := []struct {
		name   string
		script string
		// minElapsed is how long made must take to exit: a task ignoring SIGTERM only
		// ends through the SIGKILL sent after the grace period.
		minElapsed time.Duration
	}{
		{"background children", "sleep 300 & echo $! >> pids\nsleep 301 & echo $! >> pids\necho ok > ready\nwait", 0},
		{"TERM ignored", "trap '' TERM\nsleep 30 & echo $! >> pids\necho ok > ready\nwait", 2500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// test -t 0 fails the task (it never writes ready) if stdin is not the terminal.
			script := "test -t 0\necho $PPID > made.pid\n" + tt.script
			dir := writeTasks(t, "Madefile.md", "### gc\n```bash\n"+script+"\n```\n")
			startOnTTY(t, dir, "gc")

			waitForFile(t, filepath.Join(dir, "ready"))
			madePid := readPids(t, filepath.Join(dir, "made.pid"))[0]
			pids := readPids(t, filepath.Join(dir, "pids"))
			t.Cleanup(func() {
				for _, pid := range pids {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})

			begin := time.Now()
			if err := syscall.Kill(madePid, syscall.SIGTERM); err != nil {
				t.Fatalf("send SIGTERM to made (pid %d): %v", madePid, err)
			}
			waitForFile(t, filepath.Join(dir, "status"))
			elapsed := time.Since(begin)
			if b, _ := os.ReadFile(filepath.Join(dir, "status")); strings.TrimSpace(string(b)) != "143" {
				t.Errorf("made exit status = %q, want 143", strings.TrimSpace(string(b)))
			}
			if elapsed < tt.minElapsed {
				t.Errorf("made exited %v after SIGTERM, want at least %v", elapsed, tt.minElapsed)
			}

			// Orphans are reparented to init; give it a moment to reap any that died.
			deadline := time.Now().Add(2 * time.Second)
			for _, pid := range pids {
				for syscall.Kill(pid, 0) == nil {
					if time.Now().After(deadline) {
						t.Errorf("descendant pid %d still running after made exited on SIGTERM", pid)
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
		})
	}
}

// startOnTTY runs made with args in dir on a new pseudo-terminal, under a bash that
// leads the terminal's session and writes made's exit status to dir/status. The session
// outlives made, so the hangup the kernel sends when a session leader exits cannot
// clean up after it.
func startOnTTY(t *testing.T, dir string, args ...string) {
	t.Helper()
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("cannot open a pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	go func() { _, _ = io.Copy(io.Discard, master) }()

	wrapper := `"$0" "$@"; echo $? > status; exec sleep 60`
	cmd := exec.CommandContext(t.Context(), "bash", append([]string{"-c", wrapper, madeBin}, args...)...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	err = cmd.Start()
	_ = slave.Close()
	if err != nil {
		t.Fatalf("start made on a terminal: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
}

// waitForFile polls until path exists and is non-empty, failing after 10s.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readPids reads the whitespace-separated pids in path.
func readPids(t *testing.T, path string) []int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("parse pid %q in %s: %v", f, path, err)
		}
		pids = append(pids, pid)
	}
	if len(pids) == 0 {
		t.Fatalf("%s has no pids", path)
	}
	return pids
}
