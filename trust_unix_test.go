//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/display"
)

// parentTree makes a repository root with a Madefile.md and a subdirectory a, changes
// to a, and returns the physical root. The .git entry keeps the walk inside it.
func parentTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkTree(t, root, ".git/", "Madefile.md", "a/")
	t.Chdir(filepath.Join(root, "a"))
	return root
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// TestRunRefusesUntrustedParentMadefile checks the message and exit code of every
// command that reads the task file, and that the task does not run.
func TestRunRefusesUntrustedParentMadefile(t *testing.T) {
	root := parentTree(t)
	mf := filepath.Join(root, "Madefile.md")
	marker := filepath.Join(root, "ran")
	if err := os.WriteFile(mf, []byte("### touch\n```sh\ntouch "+marker+"\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chmod(t, mf, 0o666)
	refusal := []string{"made: refusing " + mf + " found in a parent directory: it is world-writable (mode 0666)",
		"made: hint: ", "made -f " + display.ShellQuotePath(mf)}
	checkRunCases(t, []runCase{
		{name: "task", args: []string{"touch"}, wantCode: 1, wantStderr: refusal},
		{name: "list", args: []string{"--list"}, wantCode: 1, wantStderr: refusal, notStdout: []string{"touch"}},
		{name: "check", args: []string{"--check"}, wantCode: 1, wantStderr: refusal},
		{name: "list-names", args: []string{"--list-names"}, wantCode: 1, notStdout: []string{"touch"}},
	})
	if _, err := os.Stat(marker); err == nil {
		t.Error("the refused task ran")
	}

	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"-f", mf, "--list-names"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "touch") {
		t.Errorf("run(-f %q --list-names) = %d, stdout %q, stderr %q; want the task listed", mf, code, &stdout, &stderr)
	}
}
