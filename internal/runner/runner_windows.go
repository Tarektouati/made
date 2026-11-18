//go:build windows

package runner

import (
	"errors"
	"os/exec"
	"syscall"
)

// errorFilenameExcedRange is ERROR_FILENAME_EXCED_RANGE, which CreateProcess returns for
// a command line longer than 32,767 characters.
const errorFilenameExcedRange = syscall.Errno(206)

// argLimit is the approximate size, in bytes, above which a script can no longer be
// passed as an argument: CreateProcess caps the whole command line at 32,767 characters.
const argLimit = 32767

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

// signalTermSurvivors does nothing: only the interpreter, which has exited, is tracked.
func signalTermSurvivors(cmd *exec.Cmd, td *teardown) {}

// trackDescendants does nothing: descendants are not tracked on Windows.
func trackDescendants(cmd *exec.Cmd, td *teardown) {}

// trackSurvivors does nothing: descendants are not tracked on Windows.
func trackSurvivors(cmd *exec.Cmd, td *teardown) {}

// fromTerminal always reports false: there is no terminal foreground group on Windows.
func fromTerminal(td *teardown) bool { return false }

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

// argListTooLong reports whether err is Windows rejecting the command line as too long.
func argListTooLong(err error) bool { return errors.Is(err, errorFilenameExcedRange) }
