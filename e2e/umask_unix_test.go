//go:build unix

package e2e

import "syscall"

// setTestUmask clears the group and other write bits from the umask, so the trees the
// tests build are not refused as group-writable parent Madefiles for a developer whose
// umask is 002. made inherits it.
func setTestUmask() { syscall.Umask(0o022) }
