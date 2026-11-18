//go:build !windows && !linux

package runner

import "syscall"

// zombie reports false: outside Linux, getpgid already fails for a zombie, so
// inOwnGroup does not count one.
func zombie(int) bool { return false }

// groupAlive reports whether process group pgid still has a member; outside Linux,
// kill(-pgid, 0) fails once only zombies are left.
func groupAlive(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }
