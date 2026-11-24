//go:build unix

package selfupdate

import (
	"os"
	"syscall"
)

// preserveGroup tries to keep a shared installation's group without changing the
// owner. A user who can replace the binary may not be allowed to use its group.
func preserveGroup(f *os.File, info os.FileInfo) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = f.Chown(-1, int(stat.Gid))
	}
}
