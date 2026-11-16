//go:build !unix

package main

import "os/exec"

// taskExitCode is the exit status made reports for a failed task: the task's exit
// code. Only unix reports a process killed by a signal.
func taskExitCode(err *exec.ExitError) int {
	return err.ExitCode()
}
