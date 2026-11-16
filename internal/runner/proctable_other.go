//go:build !windows && !linux && !darwin

package runner

import "errors"

// processTable is not implemented on this OS, so a task sharing made's process group is
// torn down through its interpreter only.
func processTable() (map[int]int, error) {
	return nil, errors.New("process table not supported on this OS")
}
