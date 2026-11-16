//go:build !linux

package runner

import "os/exec"

// setParentDeathSignal is a no-op outside Linux, which has no equivalent of
// PR_SET_PDEATHSIG.
func setParentDeathSignal(*exec.Cmd) {}
