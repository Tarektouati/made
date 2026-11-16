//go:build !windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/parser"
)

// stdinModes are the two teardown modes: a process group when stdin is not a terminal,
// and the descendant tree when it is (forced via stdinIsTerminal, without a real pty).
var stdinModes = []struct {
	name string
	tty  bool
}{{"grouped", false}, {"tty", true}}

// forEachStdinMode runs body as a subtest per stdin mode with stdinIsTerminal forced
// accordingly. body must not call t.Parallel itself (the modes would race on the
// override), but its own subtests may: the override lasts until they end.
func forEachStdinMode(t *testing.T, body func(t *testing.T, tty bool)) {
	for _, mode := range stdinModes {
		t.Run(mode.name, func(t *testing.T) {
			orig := stdinIsTerminal
			stdinIsTerminal = func() bool { return mode.tty }
			t.Cleanup(func() { stdinIsTerminal = orig })
			body(t, mode.tty)
		})
	}
}

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
	go func() { done <- Run(ctx, parser.Task{Name: "gc", Script: script}, dir, nil) }()

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
// writes "ready" once its children are running; a child that ignores SIGTERM must
// outlive killGrace to prove it was force-killed.
func TestRunCancelTearsDownDescendants(t *testing.T) {
	requireCmd(t, "bash")
	scripts := []struct {
		name   string
		script string
		delay  time.Duration // how long the children wait before writing their markers
	}{
		{"background children", "( sleep 1; echo alive > m1 ) &\n( sleep 1; echo alive > m2 ) &\necho ok > ready\nwait", time.Second},
		{"nested grandchild", "bash -c '( sleep 1; echo alive > m ) & echo ok > ready; wait' &\nwait", time.Second},
		{"TERM ignored", "trap '' TERM\n( sleep 4; echo alive > m ) &\necho ok > ready\nwait", 4 * time.Second},
	}
	forEachStdinMode(t, func(t *testing.T, _ bool) {
		for _, tc := range scripts {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir, cancelled := cancelTask(t, tc.script, nil)
				time.Sleep(time.Until(cancelled.Add(tc.delay + 500*time.Millisecond)))
				if m := markers(t, dir); len(m) > 0 {
					t.Errorf("descendants survived cancellation and wrote %v", m)
				}
			})
		}
	})
}

// TestRunCancelBySignal checks which cancelling signals tear down the descendant tree.
// With a terminal on stdin, SIGINT and SIGQUIT come from the terminal, which already
// delivered them to the whole foreground group, so only the interpreter is signaled and
// a child left to handle the signal itself (here: one that never receives it) survives.
// Every other signal, and every signal without a terminal, tears the task down.
func TestRunCancelBySignal(t *testing.T) {
	requireCmd(t, "bash")
	const script = "( sleep 1; echo alive > m ) &\necho ok > ready\nwait"
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
	forEachStdinMode(t, func(t *testing.T, tty bool) {
		for _, tc := range signals {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				var cause error
				if tc.sig != nil {
					cause = signalCause{tc.sig}
				}
				dir, cancelled := cancelTask(t, script, cause)
				time.Sleep(time.Until(cancelled.Add(1500 * time.Millisecond)))
				wantSurvivor := tty && fromTerminal(tc.sig)
				if got := len(markers(t, dir)) > 0; got != wantSurvivor {
					t.Errorf("child survived = %v, want %v", got, wantSurvivor)
				}
			})
		}
	})
}

// TestRunCancelGivesSurvivorsGrace checks that a child with a SIGTERM handler keeps the
// grace period to shut down cleanly after the interpreter has already exited on SIGTERM,
// instead of being force-killed as soon as the interpreter is gone. The child only forks
// short sleeps: one forked as SIGTERM arrives can miss it (bash's inherited trap catches
// it between fork and exec), and a long one would rightly be kept until killGrace.
func TestRunCancelGivesSurvivorsGrace(t *testing.T) {
	requireCmd(t, "bash")
	const script = "( trap 'sleep 1; echo done > graceful; exit 0' TERM; while :; do sleep 0.05; done ) &\necho ok > ready\nwait"
	forEachStdinMode(t, func(t *testing.T, _ bool) {
		dir, cancelled := cancelTask(t, script, signalCause{syscall.SIGTERM})
		if b, err := os.ReadFile(filepath.Join(dir, "graceful")); err != nil || len(b) == 0 {
			t.Errorf("child with a SIGTERM handler did not finish its shutdown (graceful: %v)", err)
		}
		if elapsed := time.Since(cancelled); elapsed >= killGrace {
			t.Errorf("Run took %v after cancel, want it to return once the child exited, before %v", elapsed, killGrace)
		}
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
