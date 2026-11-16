//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// signalTasksMD has tasks that kill their own shell with a signal, plus plain exits
// for contrast.
const signalTasksMD = "# Tasks\n\n" +
	"### kill\n```bash\nkill -KILL $$\n```\n\n" +
	"### term\n```bash\nkill -TERM $$\n```\n\n" +
	"### int\n```bash\nkill -INT $$\n```\n\n" +
	"### pipe\n```bash\nkill -PIPE $$\n```\n\n" +
	"### exit3\n```bash\nexit 3\n```\n\n" +
	"### exit255\n```bash\nexit 255\n```\n"

// TestRunSignalExitCode checks that a task killed by signal n makes run return 128+n,
// as shells report it, while a real exit status is passed through unchanged.
func TestRunSignalExitCode(t *testing.T) {
	requireCmd(t, "bash")
	f := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(f, []byte(signalTasksMD), 0o644); err != nil {
		t.Fatal(err)
	}
	noRunnerErr := []string{"made: "}

	checkRunCases(t, []runCase{
		{name: "SIGKILL", args: []string{"-f", f, "kill"}, wantCode: 137, notStderr: noRunnerErr},
		{name: "SIGTERM", args: []string{"-f", f, "term"}, wantCode: 143, notStderr: noRunnerErr},
		{name: "SIGINT", args: []string{"-f", f, "int"}, wantCode: 130, notStderr: noRunnerErr},
		{name: "SIGPIPE", args: []string{"-f", f, "pipe"}, wantCode: 141, notStderr: noRunnerErr},
		{name: "exit 3", args: []string{"-f", f, "exit3"}, wantCode: 3, notStderr: noRunnerErr},
		{name: "exit 255", args: []string{"-f", f, "exit255"}, wantCode: 255, notStderr: noRunnerErr},
	})
}
