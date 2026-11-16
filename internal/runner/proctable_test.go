//go:build linux || darwin

package runner

import (
	"os"
	"testing"
)

func TestProcessTableHasSelf(t *testing.T) {
	table, err := processTable()
	if err != nil {
		t.Fatalf("processTable: %v", err)
	}
	ppid, ok := table[os.Getpid()]
	if !ok {
		t.Fatalf("processTable has no entry for pid %d", os.Getpid())
	}
	if ppid != os.Getppid() {
		t.Errorf("processTable[%d] = %d, want %d", os.Getpid(), ppid, os.Getppid())
	}
}
