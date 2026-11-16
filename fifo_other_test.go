//go:build !unix

package main

import "testing"

// mkFIFO skips the test: syscall.Mkfifo only exists on unix systems.
func mkFIFO(t *testing.T, _ string) {
	t.Helper()
	t.Skip("FIFOs are only supported on unix")
}
