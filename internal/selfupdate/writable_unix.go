//go:build unix

package selfupdate

import "golang.org/x/sys/unix"

// writable reports whether the current user may create files in dir.
func writable(dir string) bool {
	return unix.Access(dir, unix.W_OK) == nil
}
