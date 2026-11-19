//go:build unix

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/testenv"
)

func TestSignalsKillGrandchild(t *testing.T) {
	requireBash(t)

	// The task backgrounds a grandchild that writes a marker after a delay, records its
	// pid, then waits. If made tears down the whole process group on cancel, the
	// grandchild is gone once made exits and never wrote the marker.
	tests := []struct {
		name string
		sig  syscall.Signal
	}{
		{"SIGINT", syscall.SIGINT},
		{"SIGTERM", syscall.SIGTERM},
		{"SIGHUP", syscall.SIGHUP},
		{"SIGQUIT", syscall.SIGQUIT},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			content := "### long\n```bash\n( sleep 3; echo alive > marker.txt ) &\necho $! > pids\necho started\nwait\n```\n"
			dir := writeTasks(t, "Madefile.md", content)

			outPath := filepath.Join(t.TempDir(), "out.txt")
			outFile, err := os.Create(outPath)
			if err != nil {
				t.Fatalf("create out file: %v", err)
			}
			defer func() { _ = outFile.Close() }()

			var stderr bytes.Buffer
			cmd := exec.CommandContext(t.Context(), madeBin, "long")
			cmd.Dir = dir
			cmd.Stdout = outFile
			cmd.Stderr = &stderr
			output := func() string {
				b, _ := os.ReadFile(outPath)
				return string(b)
			}

			if err := cmd.Start(); err != nil {
				t.Fatalf("start made: %v", err)
			}
			waitErr := make(chan error, 1)
			go func() { waitErr <- cmd.Wait() }()

			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(output(), "started") {
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatal("task never printed 'started'")
				}
				time.Sleep(10 * time.Millisecond)
			}
			pids := testenv.ReadPids(t, filepath.Join(dir, "pids"))
			t.Cleanup(func() { _ = syscall.Kill(pids[0], syscall.SIGKILL) })

			if err := cmd.Process.Signal(tt.sig); err != nil {
				t.Fatalf("send %s: %v", tt.name, err)
			}
			select {
			case werr := <-waitErr:
				if werr == nil {
					t.Errorf("made exited 0 after %s, want a failure status", tt.name)
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("made did not exit within 10s of %s", tt.name)
			}

			// Go's default SIGQUIT handler dumps goroutines; a handled signal must not.
			if strings.Contains(stderr.String(), "goroutine ") {
				t.Errorf("stderr contains a goroutine dump after %s; signal not handled:\n%s", tt.name, stderr.String())
			}

			checkGone(t, pids)
			marker := filepath.Join(dir, "marker.txt")
			if _, err := os.Stat(marker); err == nil {
				t.Errorf("grandchild survived %s and wrote marker.txt; process group not torn down", tt.name)
			} else if !os.IsNotExist(err) {
				t.Fatalf("stat marker: %v", err)
			}
		})
	}
}
