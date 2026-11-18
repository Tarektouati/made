//go:build linux

package runner

import (
	"sync"

	"golang.org/x/sys/unix"
)

var subreaperOnce sync.Once

// becomeSubreaper makes made a child subreaper, so a process of the task whose parent
// exits is reparented to made instead of init, where orphans finds it. made never
// reaps such processes; they are left as zombies until made exits.
func becomeSubreaper() {
	subreaperOnce.Do(func() { _ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) })
}
