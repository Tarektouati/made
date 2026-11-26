//go:build unix

package main

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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

// feedFIFO writes content to the FIFO at path from a goroutine, once a reader opens
// it. Write errors are ignored: the reader may stop early. At cleanup, a writer still
// waiting for a reader is released by a reader that drains the pipe until the writer
// is done (closing it at once would leave the writer blocked on macOS).
func feedFIFO(t *testing.T, path string, content []byte) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = f.Write(content)
		_ = f.Close()
	}()
	t.Cleanup(func() {
		// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
		r, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			<-done
			return
		}
		for {
			select {
			case <-done:
				_ = r.Close()
				return
			default:
				_, _ = io.Copy(io.Discard, r)
				time.Sleep(10 * time.Millisecond)
			}
		}
	})
}
