//go:build !windows

package runner

import (
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

// maxFreezeRounds bounds how many times freezeDescendants rescans the process table for
// descendants forked since the previous scan.
const maxFreezeRounds = 10

// configureProcessGroup puts the task in its own process group so it can be torn down
// as a unit, but only when stdin is not a terminal. In an interactive terminal a
// backgrounded process group that reads stdin would receive SIGTTIN and stop, and the
// terminal already delivers Ctrl-C and Ctrl-Z to the whole foreground group, so the task
// stays in made's group and is torn down through its descendant tree instead (see
// signalTerm). It reports whether the group was created.
func configureProcessGroup(cmd *exec.Cmd) bool {
	if stdinIsTerminal() {
		return false
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return true
}

// signalTerm asks the task to stop. With a process group it signals the whole group
// (negative pid). Otherwise, unless the cancelling signal came from the terminal (see
// fromTerminal), it freezes the interpreter and its descendants, records the descendants
// in td, and sends each SIGTERM followed by SIGCONT, so children that would be orphaned
// when the interpreter exits are still reached. Teardown is best-effort.
func signalTerm(cmd *exec.Cmd, td *teardown) {
	if cmd.Process == nil {
		return
	}
	if td.grouped {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return
	}
	if fromTerminal(td.sig) {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		return
	}
	// The interpreter is signaled through cmd.Process, which knows once it has been
	// reaped, so its pid is never signaled after it may have been reused.
	_ = cmd.Process.Signal(syscall.SIGSTOP)
	td.pids = freezeDescendants(cmd.Process.Pid)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	for _, pid := range td.pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	_ = cmd.Process.Signal(syscall.SIGCONT)
	for _, pid := range td.pids {
		_ = syscall.Kill(pid, syscall.SIGCONT)
	}
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
// made's group.
func survivors(cmd *exec.Cmd, td *teardown) bool {
	if cmd.Process == nil {
		return false
	}
	if td.grouped {
		return syscall.Kill(-cmd.Process.Pid, 0) == nil
	}
	return slices.ContainsFunc(td.pids, inOwnGroup)
}

// fromTerminal reports whether sig is one the terminal sends to its whole foreground
// group (Ctrl-C, Ctrl-\), so the task's processes have received it already.
func fromTerminal(sig os.Signal) bool {
	return sig == os.Interrupt || sig == syscall.SIGQUIT
}

// inOwnGroup reports whether pid is alive and in made's process group.
func inOwnGroup(pid int) bool {
	pgid, err := unix.Getpgid(pid)
	return err == nil && pgid == syscall.Getpgrp()
}

// freezeDescendants stops every descendant of root still in made's process group with
// SIGSTOP and returns their pids. Stopped processes cannot fork, so rescanning until no
// new descendant appears yields a stable set. Without a process table (unsupported OS)
// it returns none.
func freezeDescendants(root int) []int {
	var stopped []int
	for range maxFreezeRounds {
		table, err := processTable()
		if err != nil {
			break
		}
		found := false
		for _, pid := range descendants(root, table)[1:] {
			if slices.Contains(stopped, pid) || !inOwnGroup(pid) {
				continue
			}
			_ = syscall.Kill(pid, syscall.SIGSTOP)
			stopped = append(stopped, pid)
			found = true
		}
		if !found {
			break
		}
	}
	return stopped
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
