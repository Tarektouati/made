//go:build windows

package runner

import "os/exec"

// configureProcessGroup is a no-op on Windows, which lacks POSIX process groups. Tearing
// down the full process tree (e.g. via a job object) is out of scope; teardown is
// best-effort against the interpreter process only.
func configureProcessGroup(cmd *exec.Cmd) bool {
	return false
}

func signalTerm(cmd *exec.Cmd, grouped bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func signalKill(cmd *exec.Cmd, grouped bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
