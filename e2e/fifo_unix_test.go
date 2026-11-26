//go:build unix

package e2e

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// mkFIFO creates a named pipe at path, creating parent directories as needed.
func mkFIFO(t *testing.T, path string) {
	t.Helper()
	// #nosec G301 -- The fixture tree is inside a private temporary directory.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
