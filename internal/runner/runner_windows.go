//go:build windows

package runner

import "os/exec"

// configureProcessGroup is a no-op on Windows, which lacks POSIX process groups. Tearing
// down the full process tree (e.g. via a job object) is out of scope; teardown is
// best-effort against the interpreter process only.
func configureProcessGroup(cmd *exec.Cmd) bool {
	return false
}

func signalTerm(cmd *exec.Cmd, td *teardown) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func signalKill(cmd *exec.Cmd, td *teardown) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// survivors always reports false: without process groups only the interpreter, which
// has already exited, is tracked.
func survivors(cmd *exec.Cmd, td *teardown) bool {
	return false
}
