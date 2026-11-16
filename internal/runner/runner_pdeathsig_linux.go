package runner

import (
	"os/exec"
	"syscall"
)

// setParentDeathSignal asks the kernel to SIGKILL the interpreter if made dies, even
// by SIGKILL, which made cannot handle. Only the interpreter is signalled; processes
// it spawned may survive. The signal fires when the OS thread that started the child
// exits (go.dev/issue/27505), which the Go runtime only does for goroutines locked
// with runtime.LockOSThread; Run never locks one.
func setParentDeathSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
