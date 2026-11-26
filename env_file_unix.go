//go:build unix

package main

import (
	"os"
	"syscall"
)

func openEnvFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
