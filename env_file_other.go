//go:build !unix

package main

import "os"

func openEnvFile(path string) (*os.File, error) {
	return os.Open(path)
}
