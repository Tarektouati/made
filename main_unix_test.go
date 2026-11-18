//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

// TestRunEscapesControlCharsInPaths covers every message that echoes a path: a
// checkout directory or -f argument can carry terminal escape sequences or invisible
// characters. They are printed escaped, as %q would, so the path stays traceable.
func TestRunEscapesControlCharsInPaths(t *testing.T) {
	requireCmd(t, "sh")
	root := t.TempDir()
	evil := filepath.Join(root, "x\x1b]0;PWNED\x07dir")
	mkTree(t, evil, ".git/", "sub/", "bin/")
	md := "### t\n```sh\ntrue\n```\n### t\n```sh\ntrue\n```\n### py\n```python3\npass\n```\n"
	if err := os.WriteFile(filepath.Join(evil, "Madefile.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evil, "empty.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	deep := strings.Repeat("> ", 257) + "\n"
	if err := os.WriteFile(filepath.Join(evil, "deep.md"), []byte(deep), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evil, "bin", "python3"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lost := filepath.Join(root, "n\x1b[2Jo")
	mkTree(t, lost, ".git/")
	mkSymlink(t, "gone\x1b]0;t\x07.md", filepath.Join(lost, "Madefile.md"))

	sub := filepath.Join(evil, "sub")
	const evilEsc = `x\x1b]0;PWNED\adir`
	tests := []struct {
		name string
		dir  string
		args []string
		want []string // the message and the escaped path in it
		path string   // PATH for the task; empty keeps the inherited one
	}{
		{"list header", sub, []string{"-l"}, []string{"Tasks in ", evilEsc + "/Madefile.md:"}, ""},
		{"unknown task", sub, []string{"nope"}, []string{"Tasks in ", evilEsc}, ""},
		{"using parent", sub, []string{"t"}, []string{"made: using ", evilEsc}, ""},
		{"check location", sub, []string{"--check"}, []string{evilEsc + "/Madefile.md:5: error: duplicate-task"}, ""},
		{"no tasks", sub, []string{"-f", filepath.Join(evil, "empty.md")}, []string{"made: no tasks found in ", evilEsc}, ""},
		{"cannot read", sub, []string{"-f", filepath.Join(evil, "none\x1b[2J.md")}, []string{"made: cannot read ", `none\x1b[2J.md`}, ""},
		{"check cannot read", sub, []string{"--check", "-f", filepath.Join(evil, "none\x1b[2J.md")}, []string{"made: cannot read ", `none\x1b[2J.md`}, ""},
		{"zero width in path", sub, []string{"-f", "a\u200bb.md"}, []string{`made: cannot read a\u200bb.md`}, ""},
		{"cannot parse", sub, []string{"-f", filepath.Join(evil, "deep.md")}, []string{"made: cannot parse ", evilEsc}, ""},
		{"not found and skipped", lost, []string{"-l"}, []string{`n\x1b[2Jo`, `broken symlink to gone\x1b]0;t\a.md`, "no Madefile.md found in "}, ""},
		{"interpreter path", sub, []string{"py"}, []string{"is not executable", evilEsc}, filepath.Join(evil, "bin")},
		{"init exists", evil, []string{"init"}, []string{"already exists in ", evilEsc}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(tt.dir)
			if tt.path != "" {
				t.Setenv("PATH", tt.path)
			}
			var stdout, stderr bytes.Buffer
			run(tt.args, &stdout, &stderr)
			out := stdout.String() + stderr.String()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("run(%q) output = %q, want it to contain %q", tt.args, out, w)
				}
			}
			if strings.ContainsAny(out, "\x1b\x07\u200b") {
				t.Errorf("run(%q) output contains a raw ESC, BEL or zero width space: %q", tt.args, out)
			}
		})
	}
}
