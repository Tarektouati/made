//go:build unix

package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestEnvFileFIFOIsNonblocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vars.fifo")
	mkFIFO(t, path)
	done := make(chan error, 1)
	go func() {
		_, err := readEnvFile(path)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errNotRegular) {
			t.Errorf("read FIFO = %v, want not regular", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading env-file blocked on a FIFO")
	}
	// Exercise the opener itself too: a FIFO replacing a regular file between Stat
	// and Open must not block before readEnvFile can check the opened handle.
	go func() {
		f, err := openEnvFile(path)
		if err == nil {
			err = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("nonblocking open: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("env-file opener blocked on a FIFO")
	}
}
