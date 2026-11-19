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

	"github.com/Tarektouati/made/internal/testenv"
)

// killGrace is made's grace period between asking a cancelled task to stop and
// force-killing it (internal/runner's killGrace).
const killGrace = 3 * time.Second

// TestSignalToMadeOnTTYKillsDescendants sends a signal to made alone, from outside,
// while it runs a task on a real terminal, and checks that the task's background
// children are gone once made exits. On a terminal the task shares made's process
// group, so this covers the descendant-tree teardown rather than the process-group one.
// SIGINT and SIGQUIT sent this way look like Ctrl-C and Ctrl-\ to made, which then only
// tears the task down after the grace period, but must not orphan anything either.
func TestSignalToMadeOnTTYKillsDescendants(t *testing.T) {
	requireBash(t)
	scripts := []struct {
		name   string
		script string
		// minElapsed is how long made must take to exit: a task ignoring SIGTERM only
		// ends through the SIGKILL sent after the grace period.
		minElapsed time.Duration
	}{
		{"background children", "sleep 300 & echo $! >> pids\nsleep 301 & echo $! >> pids\necho ok > ready\nwait", 0},
		{"TERM ignored", "trap '' TERM\nsleep 30 & echo $! >> pids\necho ok > ready\nwait", 2500 * time.Millisecond},
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT} {
		for _, tt := range scripts {
			// A foreground made takes SIGINT and SIGQUIT for Ctrl-C and Ctrl-\, so it
			// must not tear the task down before the grace period is over.
			if sig != syscall.SIGTERM {
				tt.minElapsed = max(tt.minElapsed, killGrace)
			}
			t.Run(sig.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				dir, madePid, pids := startTTYTask(t, tt.script)

				begin := time.Now()
				if err := syscall.Kill(madePid, sig); err != nil {
					t.Fatalf("send %v to made (pid %d): %v", sig, madePid, err)
				}
				status := waitStatus(t, dir)
				elapsed := time.Since(begin)
				if want := strconv.Itoa(128 + int(sig)); status != want {
					t.Errorf("made exit status = %q, want %s", status, want)
				}
				if elapsed < tt.minElapsed {
					t.Errorf("made exited %v after %v, want at least %v", elapsed, sig, tt.minElapsed)
				}
				checkGone(t, pids)
			})
		}
	}
}

// TestCtrlCOnTTYLetsTaskCleanUp presses Ctrl-C on the terminal of a task that traps
// SIGINT to clean up: as when run directly, its trap must run to the end and its exit
// status be made's. The background child, which a non-interactive shell starts with
// SIGINT ignored, is torn down after the grace period.
func TestCtrlCOnTTYLetsTaskCleanUp(t *testing.T) {
	requireBash(t)
	script := "trap 'echo start >> clean; sleep 1; echo done >> clean; exit 3' INT\n" +
		"sleep 300 & echo $! >> pids\necho ok > ready\nwait"
	dir, _, pids := startTTYTask(t, script, func(master *os.File) {
		if _, err := master.Write([]byte{0x03}); err != nil {
			t.Fatalf("press Ctrl-C: %v", err)
		}
	})
	if status := waitStatus(t, dir); status != "3" {
		t.Errorf("made exit status = %q, want the trap's 3", status)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "clean")); string(b) != "start\ndone\n" {
		t.Errorf("trap output = %q, want it to run to the end (%q)", b, "start\ndone\n")
	}
	checkGone(t, pids)
}

// TestCtrlCOnTTYTearsDownTrapChild presses Ctrl-C on the terminal of a task whose INT
// trap starts a background child and exits at once, orphaning it before made could
// see it as a descendant of the interpreter: it must still be torn down. made scans for
// descendants as soon as it gets the signal and then every 50ms; the trap's short sleep
// starts the child after that first scan.
func TestCtrlCOnTTYTearsDownTrapChild(t *testing.T) {
	requireBash(t)
	script := "trap 'sleep 0.1; sleep 300 & echo $! > orphan; exit 3' INT\n" +
		"sleep 30 & echo $! >> pids\necho ok > ready\nwait"
	dir, _, pids := startTTYTask(t, script, func(master *os.File) {
		if _, err := master.Write([]byte{0x03}); err != nil {
			t.Fatalf("press Ctrl-C: %v", err)
		}
	})
	if status := waitStatus(t, dir); status != "3" {
		t.Errorf("made exit status = %q, want the trap's 3", status)
	}
	orphan := testenv.ReadPids(t, filepath.Join(dir, "orphan"))
	t.Cleanup(func() { _ = syscall.Kill(orphan[0], syscall.SIGKILL) })
	checkGone(t, append(pids, orphan...))
}

// startTTYTask runs script as a task of made on a new pseudo-terminal, waits until it
// writes dir/ready, and returns its directory, made's pid and the pids the script
// appended to dir/pids. Each act then gets the terminal's master end. The script fails
// (and never writes ready) if its stdin is not the terminal.
func startTTYTask(t *testing.T, script string, acts ...func(master *os.File)) (dir string, madePid int, pids []int) {
	t.Helper()
	script = "test -t 0\necho $PPID > made.pid\n" + script
	dir = writeTasks(t, "Madefile.md", "### gc\n```bash\n"+script+"\n```\n")
	master := startOnTTY(t, dir, "gc")

	waitForFile(t, filepath.Join(dir, "ready"))
	madePid = testenv.ReadPids(t, filepath.Join(dir, "made.pid"))[0]
	pids = testenv.ReadPids(t, filepath.Join(dir, "pids"))
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	for _, act := range acts {
		act(master)
	}
	return dir, madePid, pids
}

// startOnTTY runs made with args in dir on a new pseudo-terminal, under a bash that
// leads the terminal's session and writes made's exit status to dir/status, and returns
// the terminal's master end. The session outlives made, so the hangup the kernel sends
// when a session leader exits cannot clean up after it. The bash traps SIGINT and
// SIGQUIT to survive Ctrl-C and Ctrl-\ without passing an ignored disposition to made.
func startOnTTY(t *testing.T, dir string, args ...string) *os.File {
	t.Helper()
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("cannot open a pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	// Reading the master end keeps the terminal's output buffer from filling up.
	go func() { _, _ = io.Copy(io.Discard, master) }()

	wrapper := `trap : INT QUIT; "$0" "$@"; echo $? > status; exec sleep 60`
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
	return master
}

// waitStatus waits for made's exit status in dir/status and returns it.
func waitStatus(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "status")
	waitForFile(t, path)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}
