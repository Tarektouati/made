//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/term"
)

// configureProcessGroup puts the task in its own process group so it can be torn down
// as a unit, but only when stdin is not a terminal. In an interactive terminal a
// backgrounded process group that reads stdin would receive SIGTTIN and stop, and the
// terminal already delivers Ctrl-C to the whole foreground group, so isolation is both
// unnecessary and harmful there. It reports whether the group was created.
func configureProcessGroup(cmd *exec.Cmd) bool {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return true
}

// signalTerm asks the task to stop. With a process group it signals the whole group
// (negative pid); otherwise just the interpreter. Teardown is best-effort.
func signalTerm(cmd *exec.Cmd, grouped bool) {
	if cmd.Process == nil {
		return
	}
	if grouped {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}

// signalKill force-kills the task. With a process group it kills the whole group.
func signalKill(cmd *exec.Cmd, grouped bool) {
	if cmd.Process == nil {
		return
	}
	if grouped {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}
