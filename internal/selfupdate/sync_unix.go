//go:build unix

package selfupdate

import "os"

// syncDir flushes dir's entries to disk, so a rename into it survives a crash.
// Errors are ignored: the rename has already happened.
func syncDir(dir string) {
	// #nosec G304 -- The updater passes its resolved executable directory for fsync after replacement.
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
