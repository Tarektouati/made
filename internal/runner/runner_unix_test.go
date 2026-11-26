//go:build !windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/task"
	"github.com/Tarektouati/made/internal/testenv"
)

// stdinMode is a teardown mode: a process group when stdin is not a terminal, and the
// descendant tree when it is (forced via stdinIsTerminal, without a real pty), with made
// in the terminal's foreground process group or not (forced via inForeground).
type stdinMode struct {
	name       string
	tty        bool
	foreground bool
}

var stdinModes = []stdinMode{{"grouped", false, false}, {"tty", true, true}, {"tty background", true, false}}

// forEachStdinMode runs body as a subtest per stdin mode with stdinIsTerminal and
// inForeground forced accordingly. body must not call t.Parallel itself (the modes would
// race on the overrides), but its own subtests may: the overrides last until they end.
func forEachStdinMode(t *testing.T, body func(t *testing.T, mode stdinMode)) {
	for _, mode := range stdinModes {
		t.Run(mode.name, func(t *testing.T) {
			setMode(t, mode)
			body(t, mode)
		})
	}
}

// setMode forces mode's stdinIsTerminal and inForeground until t ends.
func setMode(t *testing.T, mode stdinMode) {
	origTTY, origFG := stdinIsTerminal, inForeground
	stdinIsTerminal = func() bool { return mode.tty }
	inForeground = func() bool { return mode.foreground }
	t.Cleanup(func() { stdinIsTerminal, inForeground = origTTY, origFG })
}

// terminalCancel forces made into the foreground of a terminal on stdin until t ends and
// returns a cancellation cause of SIGINT, as for Ctrl-C.
func terminalCancel(t *testing.T) error {
	setMode(t, stdinMode{"tty", true, true})
	return signalCause{os.Interrupt}
}

// TestRunEnvTooLarge checks that an environment too large to exec is reported as such,
// with its size only. One 4 MiB variable is over Linux's per-string limit and macOS's
// total. Windows is not covered: its environment is not part of the command line.
func TestRunEnvTooLarge(t *testing.T) {
	requireCmd(t, "bash")
	t.Setenv("MADE_TEST_BIG_ENV", strings.Repeat("e", 4<<20))
	err := Run(context.Background(), nil, task.Task{Name: "s", Script: "echo ok"}, "", nil)
	wantArgLimit(t, err,
		[]string{`task "s": arguments plus environment are too large`, "script 7 bytes", "0 arguments", "environment about "},
		[]string{"script is", "eeee", "MADE_TEST_BIG_ENV"})
}

// TestRunScriptAndEnvTooLarge checks that a script over 128 KiB is blamed only where the
// OS rejects it by itself: Linux with 4 KiB pages caps one argument at 128 KiB, while
// macOS caps only the total, here mostly the 1 MiB environment variable.
func TestRunScriptAndEnvTooLarge(t *testing.T) {
	requireCmd(t, "bash")
	script := ":" + strings.Repeat(" ", 200<<10-1)
	t.Setenv("MADE_TEST_BIG_ENV", strings.Repeat("e", 1<<20))
	err := Run(context.Background(), nil, task.Task{Name: "s", Script: script}, "", nil)
	notWant := []string{"eeee", "MADE_TEST_BIG_ENV"}
	switch {
	case runtime.GOOS == "linux" && os.Getpagesize() == 4096:
		wantArgLimit(t, err, []string{`task "s": script is 204800 bytes`, "Linux rejects a single argument of 131072 bytes or more"}, notWant)
	case runtime.GOOS == "darwin":
		wantArgLimit(t, err, []string{`task "s": arguments plus environment are too large`, "script 204800 bytes", "macOS limits their total to about "},
			append(notWant, "script is"))
	default:
		t.Skipf("no expectation for %s with %d-byte pages", runtime.GOOS, os.Getpagesize())
	}
}

// terminalSignal reports whether sig is one a terminal sends (Ctrl-C, Ctrl-\).
func terminalSignal(sig os.Signal) bool { return sig == os.Interrupt || sig == syscall.SIGQUIT }

// signalCause is a cancellation cause that reports the signal which cancelled the task,
// like main's.
type signalCause struct{ sig os.Signal }

func (c signalCause) Error() string     { return c.sig.String() }
func (c signalCause) Signal() os.Signal { return c.sig }

// cancelTask runs script in a fresh directory, waits until it writes "ready", cancels
// it with cause (nil for a plain cancel), and returns the directory once Run returns
// along with when Run was cancelled.
func cancelTask(t *testing.T, script string, cause error) (dir string, cancelled time.Time) {
	t.Helper()
	dir = t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, nil, task.Task{Name: "gc", Script: script}, dir, nil) }()

	waitForFile(t, filepath.Join(dir, "ready"))
	cancelled = time.Now()
	cancel(cause)
	select {
	case err := <-done:
		if err == nil {
			t.Error("Run after cancel = nil, want an error")
		}
	case <-time.After(killGrace + 5*time.Second):
		t.Fatal("Run did not return after cancel")
	}
	return dir, cancelled
}

// markers returns the m* files the task's surviving children wrote in dir.
func markers(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "m*"))
	if err != nil {
		t.Fatalf("glob markers: %v", err)
	}
	return m
}

// TestRunCancelTearsDownDescendants cancels tasks whose background children write a
// marker after a delay, and checks that none survives in either stdin mode. Each script
// appends its children's pids to "pids" and writes "ready" once they are running; a
// child that ignores SIGTERM must outlive killGrace to prove it was force-killed.
func TestRunCancelTearsDownDescendants(t *testing.T) {
	requireCmd(t, "bash")
	scripts := []struct {
		name   string
		script string
	}{
		{"background children", "( sleep 1; echo alive > m1 ) &\necho $! >> pids\n( sleep 1; echo alive > m2 ) &\necho $! >> pids\necho ok > ready\nwait"},
		{"nested grandchild", "bash -c '( sleep 1; echo alive > m ) & echo $! >> pids; echo ok > ready; wait' &\nwait"},
		{"TERM ignored", "trap '' TERM\n( sleep 4; echo alive > m ) &\necho $! >> pids\necho ok > ready\nwait"},
	}
	forEachStdinMode(t, func(t *testing.T, _ stdinMode) {
		for _, tc := range scripts {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir, _ := cancelTask(t, tc.script, nil)
				waitGone(t, filepath.Join(dir, "pids"))
				if m := markers(t, dir); len(m) > 0 {
					t.Errorf("descendants survived cancellation and wrote %v", m)
				}
			})
		}
	})
}

// TestRunCancelBySignal checks which cancelling signals tear down the descendant tree
// at once. With made in the foreground of the terminal on stdin, SIGINT and SIGQUIT may
// come from the terminal, which already delivered them to the whole foreground group, so
// nothing is signaled during the grace period and a child left to handle the signal
// itself (here: one that never receives it) survives it. Every other signal, and every
// signal without a terminal or with made in the background, tears the task down.
// A surviving child has written its marker by the time Run returns, since the
// interpreter waits for it.
func TestRunCancelBySignal(t *testing.T) {
	requireCmd(t, "bash")
	const script = "( sleep 1; echo alive > m ) &\necho $! >> pids\necho ok > ready\nwait"
	signals := []struct {
		name string
		sig  os.Signal // nil: plain cancel without a signal cause
	}{
		{"no signal", nil},
		{"SIGTERM", syscall.SIGTERM},
		{"SIGHUP", syscall.SIGHUP},
		{"SIGINT", os.Interrupt},
		{"SIGQUIT", syscall.SIGQUIT},
	}
	forEachStdinMode(t, func(t *testing.T, mode stdinMode) {
		for _, tc := range signals {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				var cause error
				if tc.sig != nil {
					cause = signalCause{tc.sig}
				}
				dir, _ := cancelTask(t, script, cause)
				wantSurvivor := mode.foreground && terminalSignal(tc.sig)
				if !wantSurvivor {
					waitGone(t, filepath.Join(dir, "pids"))
				}
				if got := len(markers(t, dir)) > 0; got != wantSurvivor {
					t.Errorf("child survived = %v, want %v", got, wantSurvivor)
				}
			})
		}
	})
}

// TestRunTerminalSignalEscalatesAfterGrace checks that a task cancelled by a signal
// that may have come from the terminal, and still running once the grace period is
// over, is then torn down with its descendants.
func TestRunTerminalSignalEscalatesAfterGrace(t *testing.T) {
	requireCmd(t, "bash")
	setMode(t, stdinMode{"tty", true, true})
	const script = "( sleep 4; echo alive > m ) &\necho $! >> pids\necho ok > ready\nwait"
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGQUIT} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			dir, cancelled := cancelTask(t, script, signalCause{sig})
			if elapsed := time.Since(cancelled); elapsed < killGrace {
				t.Errorf("Run returned %v after cancel, want it to wait out the %v grace period first", elapsed, killGrace)
			}
			waitGone(t, filepath.Join(dir, "pids"))
			if m := markers(t, dir); len(m) > 0 {
				t.Errorf("child survived the escalation after the grace period and wrote %v", m)
			}
		})
	}
}

// TestRunCancelGivesSurvivorsGrace checks that a child with a SIGTERM handler keeps the
// grace period to shut down cleanly after the interpreter has already exited on SIGTERM,
// instead of being force-killed as soon as the interpreter is gone. The child only forks
// short sleeps: one forked as SIGTERM arrives can miss it (bash's inherited trap catches
// it between fork and exec), and a long one would rightly be kept until killGrace.
// ready must come from the trapping subshell, after its trap, so the handler is
// installed before the cancel.
func TestRunCancelGivesSurvivorsGrace(t *testing.T) {
	requireCmd(t, "bash")
	const script = "( trap 'sleep 1; echo done > graceful; exit 0' TERM; echo ok > ready; while :; do sleep 0.05; done ) &\nwait"
	forEachStdinMode(t, func(t *testing.T, _ stdinMode) {
		dir, cancelled := cancelTask(t, script, signalCause{syscall.SIGTERM})
		// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
		if b, err := os.ReadFile(filepath.Join(dir, "graceful")); err != nil || len(b) == 0 {
			t.Errorf("child with a SIGTERM handler did not finish its shutdown (graceful: %v)", err)
		}
		if elapsed := time.Since(cancelled); elapsed >= killGrace {
			t.Errorf("Run took %v after cancel, want it to return once the child exited, before %v", elapsed, killGrace)
		}
	})
}

// TestRunTerminalSignalTracksLateDescendants checks that after a signal that may have
// come from the terminal, a process started by a surviving descendant once the
// interpreter has exited is still torn down after the grace period.
func TestRunTerminalSignalTracksLateDescendants(t *testing.T) {
	requireCmd(t, "bash")
	setMode(t, stdinMode{"tty", true, true})
	const script = "( sleep 1; sleep 30 & echo $! > late; wait ) &\necho ok > ready\nsleep 0.5"
	dir, _ := cancelTask(t, script, signalCause{os.Interrupt})
	pid := readPid(t, filepath.Join(dir, "late"))
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(2 * time.Second)
	for running(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("late descendant (pid %d) still running after Run returned", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readPid reads the pid in path.
func readPid(t *testing.T, path string) int {
	t.Helper()
	// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("parse pid in %s: %v", path, err)
	}
	return pid
}

// waitGone fails the test for each pid listed in path that is still running 2s
// later, and kills it when the test ends. Orphans are reaped by init, not by the
// test, so a zombie counts as gone.
func waitGone(t *testing.T, path string) {
	t.Helper()
	pids := testenv.ReadPids(t, path)
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for _, pid := range pids {
		for testenv.Alive(t, pid) {
			if time.Now().After(deadline) {
				t.Errorf("descendant pid %d still running after Run returned", pid)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestRunForceSkipsGrace(t *testing.T) {
	requireCmd(t, "bash")
	tests := []struct {
		name  string
		force bool
		// terminal cancels with a signal that may have come from the terminal, which
		// starts with a grace period where nothing is signaled (see terminalCancel).
		terminal   bool
		minElapsed time.Duration
		maxElapsed time.Duration
	}{
		{name: "force skips grace", force: true, maxElapsed: 2 * time.Second},
		{name: "force skips terminal grace", force: true, terminal: true, maxElapsed: 2 * time.Second},
		{name: "no force waits grace", minElapsed: killGrace, maxElapsed: killGrace + 5*time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var cause error
			if tt.terminal {
				cause = terminalCancel(t)
			}
			// The script and its child ignore SIGTERM, so only SIGKILL ends them; the
			// child writes "survived" if it outlives a forced teardown.
			task := task.Task{Name: "stubborn", Script: "trap '' TERM\n( sleep 1.5; touch survived ) &\necho $! > pids\necho ok > marker\nsleep 5\n"}
			ctx, cancelCause := context.WithCancelCause(context.Background())
			cancel := func() { cancelCause(cause) }
			defer cancel()
			var force chan struct{}
			if tt.force {
				force = make(chan struct{})
			}
			errc := make(chan error, 1)
			go func() { errc <- Run(ctx, force, task, dir, nil) }()

			waitForFile(t, filepath.Join(dir, "marker"))
			begin := time.Now()
			cancel()
			if tt.force {
				time.Sleep(100 * time.Millisecond)
				close(force)
			}
			select {
			case err := <-errc:
				if err == nil {
					t.Error("Run after cancel = nil, want an error")
				}
			case <-time.After(tt.maxElapsed):
				t.Fatalf("Run did not return within %v of cancel", tt.maxElapsed)
			}
			if elapsed := time.Since(begin); elapsed < tt.minElapsed {
				t.Errorf("Run returned after %v, want at least %v", elapsed, tt.minElapsed)
			}
			if tt.force {
				waitGone(t, filepath.Join(dir, "pids"))
				if _, err := os.Stat(filepath.Join(dir, "survived")); err == nil {
					t.Error("the task's child survived the forced teardown")
				}
			}
		})
	}
}

// TestSurvivorsIgnoresZombies checks that a task member which has exited but not yet
// been reaped does not count as a survivor, so a cancel does not wait for the reaper.
func TestSurvivorsIgnoresZombies(t *testing.T) {
	t.Run("grouped", func(t *testing.T) {
		leader := startProc(t, "sleep", []string{"30"}, &syscall.SysProcAttr{Setpgid: true})
		zombie := startProc(t, "true", nil, &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid})
		waitZombie(t, zombie.Process.Pid)
		_ = leader.Process.Kill()
		_ = leader.Wait()
		if survivors(leader, &teardown{grouped: true}) {
			t.Error("survivors = true for a group whose only member left is a zombie, want false")
		}
	})
	t.Run("tty", func(t *testing.T) {
		zombie := startProc(t, "true", nil, nil)
		waitZombie(t, zombie.Process.Pid)
		if survivors(zombie, &teardown{pids: []int{zombie.Process.Pid}}) {
			t.Error("survivors = true for a recorded descendant that is a zombie, want false")
		}
	})
}

// startProc starts name with args and attr, leaving it unreaped until the test ends.
func startProc(t *testing.T, name string, args []string, attr *syscall.SysProcAttr) *exec.Cmd {
	t.Helper()
	// #nosec G204 -- Tests choose the process and arguments to exercise process-group teardown.
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// waitZombie polls ps until pid has exited and is waiting to be reaped, failing after 10s.
func waitZombie(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		// #nosec G204 -- ps receives a test PID as a separate decimal argument, without a shell.
		out, _ := exec.CommandContext(t.Context(), "ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never became a zombie (ps stat %q)", pid, out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForFile polls until path exists and is non-empty, failing after 10s.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDescendants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		root  int
		table map[int]int
		want  []int
	}{
		{"lone root", 10, map[int]int{10: 1}, []int{10}},
		{"root missing from table", 10, map[int]int{20: 1}, []int{10}},
		{"chain", 10, map[int]int{10: 1, 11: 10, 12: 11, 13: 12}, []int{10, 11, 12, 13}},
		{"branching", 10, map[int]int{10: 1, 12: 10, 11: 10, 13: 11, 14: 12}, []int{10, 11, 12, 13, 14}},
		{"unrelated excluded", 10, map[int]int{10: 1, 11: 10, 20: 1, 21: 20}, []int{10, 11}},
		{"parent of root excluded", 10, map[int]int{1: 0, 10: 1, 11: 10}, []int{10, 11}},
		{"self parent", 10, map[int]int{10: 10, 11: 10}, []int{10, 11}},
		{"cycle", 10, map[int]int{10: 11, 11: 10}, []int{10, 11}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := descendants(tc.root, tc.table); !slices.Equal(got, tc.want) {
				t.Errorf("descendants(%d) = %v, want %v", tc.root, got, tc.want)
			}
		})
	}
}
