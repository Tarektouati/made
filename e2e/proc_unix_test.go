//go:build unix

package e2e

import (
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/testenv"
)

// checkGone fails the test for each of pids still running. Orphans are reparented to
// init, so a zombie waiting for it to reap them counts as gone.
func checkGone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for _, pid := range pids {
		for testenv.Alive(t, pid) {
			if time.Now().After(deadline) {
				t.Errorf("descendant pid %d still running after made exited", pid)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
