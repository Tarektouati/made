//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// taskExitCode is the exit status made reports for a failed task: the task's exit
// code, or 128+n when it was killed by signal n, as shells report it.
func taskExitCode(err *exec.ExitError) int {
	if ws, ok := err.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return err.ExitCode()
}
