//go:build unix

package selfupdate

import "os"

// syncDir flushes dir's entries to disk, so a rename into it survives a crash.
// Errors are ignored: the rename has already happened.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
