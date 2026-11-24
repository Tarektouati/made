//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
)

// TestMain clears the group and other write bits from the umask, so the trees the
// tests build are not refused as group-writable parent Madefiles (see
// checkParentMadefile) for a developer whose umask is 002.
func TestMain(m *testing.M) {
	syscall.Umask(0o022)
	os.Exit(m.Run())
}
