//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// mkFIFO creates a named pipe at path, creating parent directories as needed.
func mkFIFO(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
