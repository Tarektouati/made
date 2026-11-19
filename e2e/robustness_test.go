package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// startMade starts made in dir with stdout routed to a file (so orphaned children
// holding it open cannot block Wait), and waits until the task prints "started".
func startMade(t *testing.T, dir string, args ...string) (cmd *exec.Cmd, waitErr <-chan error) {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "out.txt")
	outFile, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create out file: %v", err)
	}
	t.Cleanup(func() { _ = outFile.Close() })

	cmd = exec.CommandContext(t.Context(), madeBin, args...)
	cmd.Dir = dir
	cmd.Stdout = outFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start made: %v", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(outPath)
		if strings.Contains(string(b), "started") {
			return cmd, errc
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("task never printed 'started'")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForFile polls until path exists and is non-empty, failing after 10s.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSecondSIGINTSkipsGrace(t *testing.T) {
	requireBash(t)
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt cannot be delivered to a child on windows")
	}
	// The task survives SIGTERM (|| : keeps errexit from ending it when the sleep is
	// killed), so without the second SIGINT made would wait the full 3-second grace
	// period before SIGKILL. Its trap writes "got" once made has acted on the first
	// SIGINT, so the two signals cannot merge into one pending SIGINT.
	dir := writeTasks(t, "Madefile.md", "### stubborn\n```bash\ntrap 'echo term > got' TERM\necho started\nwhile :; do sleep 0.05 || :; done\n```\n")
	cmd, waitErr := startMade(t, dir, "stubborn")

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send first SIGINT: %v", err)
	}
	waitForFile(t, filepath.Join(dir, "got"))
	begin := time.Now()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send second SIGINT: %v", err)
	}
	select {
	case err := <-waitErr:
		if err == nil {
			t.Error("made exited 0 after two SIGINTs, want a failure status")
		}
		if elapsed := time.Since(begin); elapsed > 2*time.Second {
			t.Errorf("made took %v to exit after the second SIGINT; want it to skip the 3s grace period", elapsed)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("made did not exit within 10s of two SIGINTs")
	}
}

// TestReadWriteDevNullStdioRunsTask covers callers such as Python's
// subprocess.DEVNULL, which hand made a /dev/null opened read-write.
func TestReadWriteDevNullStdioRunsTask(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "Madefile.md", "### touch\n```bash\necho ran > marker.txt\n```\n")
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = null.Close() })

	cmd := exec.CommandContext(t.Context(), madeBin, "touch")
	cmd.Dir = dir
	cmd.Stdout = null
	cmd.Stderr = null
	if err := cmd.Run(); err != nil {
		t.Fatalf("made touch with read-write /dev/null stdio: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "marker.txt")); err != nil {
		t.Errorf("task did not run: %v", err)
	}
}
