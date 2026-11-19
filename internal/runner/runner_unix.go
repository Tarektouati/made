//go:build !windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// stdinIsTerminal reports whether made's stdin is a terminal. It is a variable so tests
// can force the terminal code path without a pty.
var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// inForeground reports whether made's process group is the foreground group of the
// terminal on its stdin, the group Ctrl-C and Ctrl-\ are sent to. It is a variable so
// tests can force it without a pty.
var inForeground = func() bool {
	pgrp, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	return err == nil && pgrp == syscall.Getpgrp()
}

// maxFreezeRounds bounds how many times freezeTask rescans the process table for
// processes forked since the previous scan.
const maxFreezeRounds = 10

// configureProcessGroup puts the task in its own process group so it can be torn down
// as a unit, but only when stdin is not a terminal. In an interactive terminal a
// backgrounded process group that reads stdin would receive SIGTTIN and stop, and the
// terminal already delivers Ctrl-C and Ctrl-Z to the whole foreground group, so the task
// stays in made's group and is torn down through its descendant tree instead (see
// signalTerm). It reports whether the group was created.
func configureProcessGroup(cmd *exec.Cmd) bool {
	if stdinIsTerminal() {
		becomeSubreaper()
		return false
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return true
}

// signalTerm asks the task to stop. With a process group it signals the whole group
// (negative pid). Otherwise it freezes the interpreter and every process of the task it
// can find (see freezeTask), and sends them SIGTERM followed by SIGCONT, so children
// that would be orphaned when the interpreter exits are still reached. Teardown is
// best-effort.
func signalTerm(cmd *exec.Cmd, td *teardown) {
	if cmd.Process == nil {
		return
	}
	if td.grouped {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return
	}
	// The interpreter is signaled through cmd.Process, which knows once it has been
	// reaped, so its pid is never signaled after it may have been reused.
	_ = cmd.Process.Signal(syscall.SIGSTOP)
	stopped := freezeTask(cmd.Process.Pid, td)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	termAndContinue(td, stopped)
	_ = cmd.Process.Signal(syscall.SIGCONT)
}

// signalTermSurvivors sends SIGTERM to what is left of the task once its interpreter
// has exited: the process group, or, after freezing them, the processes of the task it
// can still find (see freezeTask).
func signalTermSurvivors(cmd *exec.Cmd, td *teardown) {
	if cmd.Process == nil {
		return
	}
	if td.grouped {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return
	}
	termAndContinue(td, freezeTask(0, td))
}

// termAndContinue sends SIGTERM to every running pid recorded in td, then SIGCONT to
// those in stopped.
func termAndContinue(td *teardown, stopped []int) {
	for _, pid := range td.pids {
		if running(pid) {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	for _, pid := range stopped {
		_ = syscall.Kill(pid, syscall.SIGCONT)
	}
}

// trackDescendants records the processes of the task while its interpreter runs (see
// taskPids), without signaling them, so those orphaned when it exits can still be torn
// down. It does nothing for a process group, which keeps track of its members.
func trackDescendants(cmd *exec.Cmd, td *teardown) {
	if cmd.Process == nil || td.grouped {
		return
	}
	trackTask(cmd.Process.Pid, td)
}

// trackSurvivors is trackDescendants once the interpreter has exited: it keeps
// following the descendants of the recorded processes still running, and orphans.
func trackSurvivors(cmd *exec.Cmd, td *teardown) {
	if cmd.Process == nil || td.grouped {
		return
	}
	trackTask(0, td)
}

// signalKill force-kills the task: the whole group, or the interpreter and every pid
// signalTerm recorded that is still in made's process group. Recorded pids that have
// exited may already belong to unrelated processes; the group check, unlike a process
// group id, does not guarantee they were not reused, but limits the risk to made's own
// group.
func signalKill(cmd *exec.Cmd, td *teardown) {
	if cmd.Process == nil {
		return
	}
	if td.grouped {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
	for _, pid := range td.pids {
		if inOwnGroup(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// survivors reports whether part of the cancelled task is still running after its
// interpreter exited: a member of its process group, or a recorded descendant still in
// made's group. Zombies have already exited and do not count.
func survivors(cmd *exec.Cmd, td *teardown) bool {
	if cmd.Process == nil {
		return false
	}
	if td.grouped {
		return groupAlive(cmd.Process.Pid)
	}
	return slices.ContainsFunc(td.pids, running)
}

// fromTerminal reports whether the signal that cancelled the task may have come from
// the terminal, which sends Ctrl-C and Ctrl-\ to its whole foreground group, so the
// task's processes have received it already: it is SIGINT or SIGQUIT, the task shares
// made's process group, and that group is in the foreground of the terminal. The same
// signal sent to made alone (kill -INT) cannot be told apart from it then.
func fromTerminal(td *teardown) bool {
	return !td.grouped && (td.sig == os.Interrupt || td.sig == syscall.SIGQUIT) && inForeground()
}

// inOwnGroup reports whether pid is alive and in made's process group.
func inOwnGroup(pid int) bool {
	pgid, err := unix.Getpgid(pid)
	return err == nil && pgid == syscall.Getpgrp()
}

// running reports whether pid is alive, not a zombie, and in made's process group.
func running(pid int) bool { return inOwnGroup(pid) && !zombie(pid) }

// freezeTask stops with SIGSTOP every process of the task taskPids finds, records them
// in td, and returns them. Stopped processes cannot fork, so rescanning until nothing
// new appears yields a stable set. Without a process table (unsupported OS) it stops
// none.
func freezeTask(root int, td *teardown) []int {
	var stopped []int
	for range maxFreezeRounds {
		table, err := processTable()
		if err != nil {
			break
		}
		found := false
		for _, pid := range taskPids(root, td, table) {
			if slices.Contains(stopped, pid) {
				continue
			}
			_ = syscall.Kill(pid, syscall.SIGSTOP)
			stopped = append(stopped, pid)
			td.record([]int{pid})
			found = true
		}
		if !found {
			break
		}
	}
	return stopped
}

// trackTask records in td, without signaling them, the processes of the task taskPids
// finds, so they can still be torn down once orphaned.
func trackTask(root int, td *teardown) {
	table, err := processTable()
	if err != nil {
		return
	}
	td.record(taskPids(root, td, table))
}

// taskPids returns the task's processes in made's group found in table, a pid-to-parent
// table: the descendants of root (the interpreter, or 0 once it has exited), of every
// pid recorded in td that is still running, and of every orphan made can see (see
// orphans). root itself is left out.
func taskPids(root int, td *teardown, table map[int]int) []int {
	roots := slices.DeleteFunc(slices.Clone(td.pids), func(pid int) bool { return !running(pid) })
	roots = append(roots, slices.DeleteFunc(orphans(), isInterpreter)...)
	if root > 0 {
		roots = append(roots, root)
	}
	var pids []int
	for _, r := range roots {
		for _, pid := range descendants(r, table) {
			if pid != root && !slices.Contains(pids, pid) && running(pid) {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// descendants returns root followed by every process descending from it in ppid, a
// pid-to-parent-pid table, in breadth-first order with siblings sorted by pid.
func descendants(root int, ppid map[int]int) []int {
	children := make(map[int][]int)
	for pid, parent := range ppid {
		if pid != parent {
			children[parent] = append(children[parent], pid)
		}
	}
	seen := map[int]bool{root: true}
	out := []int{root}
	for i := 0; i < len(out); i++ {
		kids := children[out[i]]
		slices.Sort(kids)
		for _, kid := range kids {
			if !seen[kid] {
				seen[kid] = true
				out = append(out, kid)
			}
		}
	}
	return out
}

// ArgLimit is the approximate size, in bytes, above which a script can no longer be
// passed as an argument: Linux caps a single argument at 128 KiB (MAX_ARG_STRLEN), and
// macOS's total limit (ARG_MAX) is larger.
const ArgLimit = 128 << 10

// ArgUnit names the unit of ArgLimit and argLen, for messages.
const ArgUnit = "bytes"

// argLen returns the size of s as the OS counts it against ArgLimit.
func argLen(s string) int { return len(s) }

// argListTooLong reports whether err is the kernel rejecting exec arguments as too large.
func argListTooLong(err error) bool { return errors.Is(err, syscall.E2BIG) }
