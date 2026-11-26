//go:build unix

package e2e

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Tarektouati/made/internal/testenv"
)

func TestTimeoutKillsTaskAndGrandchild(t *testing.T) {
	requireBash(t)
	for _, tc := range []struct {
		name, script string
	}{
		{"term", "sleep 30 &\necho $! > pids\nwait\n"},
		{"handled", "trap 'exit 0' TERM\nsleep 30 &\necho $! > pids\nwait\n"},
		{"kill", "trap '' TERM\nsleep 30 &\necho $! > pids\nwait\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTasks(t, "Madefile.md", "### long\n```bash\n"+tc.script+"```\n")
			stdout, stderr, code := runMade(t, dir, "--timeout", "1s", "long")
			pids := testenv.ReadPids(t, filepath.Join(dir, "pids"))
			t.Cleanup(func() {
				for _, pid := range pids {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			if code != 124 || stdout != "" || stderr != "made: task \"long\" timed out after 1s\n" {
				t.Errorf("exit = %d, stdout = %q, stderr = %q; want 124 and timeout message", code, stdout, stderr)
			}
			checkGone(t, pids)
		})
	}
}
