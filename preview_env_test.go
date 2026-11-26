package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/runner"
)

func TestPrintDoesNotStartInterpreter(t *testing.T) {
	dir := t.TempDir()
	name := "bash"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// Even the interpreter must not start: this deliberately cannot produce a
	// successful task exit. LookPath only needs an executable file to resolve it.
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 79\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	path := writeRunTasks(t)
	checkRunCases(t, []runCase{{name: "preview", args: []string{"-f", path, "--print", "hello"}, wantStdout: []string{"Interpreter:"}, notStderr: []string{"made:"}}})
}

func TestPrintEscapesEveryField(t *testing.T) {
	unsafe := "before\x1b[31m\r\b\u202eafter"
	p := &runner.Prepared{Path: unsafe, Dir: unsafe, Args: []string{unsafe, "", "line\nnext"}, Script: unsafe + "\nsecond line\n"}
	var out bytes.Buffer
	if err := printPrepared(&out, unsafe, p); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\r\b\u202e") {
		t.Errorf("preview retained terminal controls: %q", &out)
	}
	if strings.Count(out.String(), `\x1b[31m\r\b\u202e`) != 5 {
		t.Errorf("not every field was escaped: %q", &out)
	}
	if !strings.Contains(out.String(), "after\nsecond line\n") || !strings.Contains(out.String(), `"" "line\nnext"`) {
		t.Errorf("script line breaks or argument boundaries were lost: %q", &out)
	}
}

func TestPrintTaskDoesNotRun(t *testing.T) {
	requireCmd(t, "bash")
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	script := "printf 'ran' > ran\nprintf '\x1b\\n'\n# hidden \u202e\n"
	if err := os.WriteFile(path, []byte("### task\n```bash\n"+script+"```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--print", "--dry-run"} {
		t.Run(flag, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{"-f", path, flag, "task", "--", "", "-x", "two words", "line\n\x1b\u202e"}, &out, &errb)
			if code != 0 || errb.Len() != 0 {
				t.Fatalf("preview = %d, stderr %q", code, &errb)
			}
			for _, want := range []string{
				"Task file: " + strconv.Quote(path), "Working directory: " + strconv.Quote(dir),
				"Interpreter: ", `"-e" "-o" "pipefail" "-c"`,
				`"made" "" "-x" "two words" "line\n\x1b\u202e"`,
				"Script:\nprintf 'ran' > ran\nprintf '\\x1b\\n'\n# hidden \\u202e\n",
			} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("preview %q does not contain %q", out.String(), want)
				}
			}
			if strings.ContainsAny(out.String(), "\x1b\u202e") {
				t.Errorf("preview contains terminal controls: %q", &out)
			}
			if _, err := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(err) {
				t.Errorf("preview ran the script: %v", err)
			}
		})
	}
}

func TestPreviewAndEnvFileUsage(t *testing.T) {
	checkRunCases(t, []runCase{
		{name: "print without task", args: []string{"--print"}, wantCode: 2, wantStderr: []string{"--print requires a task"}},
		{name: "dry-run without task", args: []string{"--dry-run"}, wantCode: 2, wantStderr: []string{"--print requires a task"}},
		{name: "print list", args: []string{"--print", "-l", "task"}, wantCode: 2, wantStderr: []string{"--print requires a task"}},
		{name: "print names", args: []string{"--print", "--list-names", "task"}, wantCode: 2, wantStderr: []string{"--print requires a task"}},
		{name: "print check", args: []string{"--check", "--print"}, wantCode: 2, wantStderr: []string{"--check does not take --print"}},
		{name: "print init", args: []string{"--print", "init"}, wantCode: 2, wantStderr: []string{"init does not take --print"}},
		{name: "print completion", args: []string{"--print", "completion", "bash"}, wantCode: 2, wantStderr: []string{"completion does not take --print"}},
		{name: "print update", args: []string{"--dry-run", "self-update"}, wantCode: 2, wantStderr: []string{"self-update does not take --dry-run"}},
		{name: "env without task", args: []string{"--env-file", ".env"}, wantCode: 2, wantStderr: []string{"--env-file requires a task"}},
		{name: "env list", args: []string{"--env-file", ".env", "--list"}, wantCode: 2, wantStderr: []string{"--env-file requires a task"}},
		{name: "env check", args: []string{"--env-file", ".env", "--check"}, wantCode: 2, wantStderr: []string{"--check does not take --env-file"}},
		{name: "env init", args: []string{"--env-file", ".env", "init"}, wantCode: 2, wantStderr: []string{"init does not take --env-file"}},
		{name: "empty env path", args: []string{"--env-file=", "task"}, wantCode: 2, wantStderr: []string{"--env-file requires a file path"}},
		{name: "print version", args: []string{"--print", "--version"}, wantCode: 2, wantStderr: []string{"--version does not take"}},
		{name: "env version", args: []string{"--env-file", ".env", "--version"}, wantCode: 2, wantStderr: []string{"--version does not take"}},
	})
}

func TestPrintUsesExecutionErrors(t *testing.T) {
	path := writeRunTasks(t)
	t.Setenv("PATH", t.TempDir())
	for _, preview := range []bool{false, true} {
		args := []string{"-f", path}
		if preview {
			args = append(args, "--print")
		}
		checkRunCases(t, []runCase{{name: "missing interpreter", args: append(args, "hello"), wantCode: 127, wantStderr: []string{"bash", "not found"}}})
	}
}

func TestEnvFileIsExplicitAndDoesNotOverride(t *testing.T) {
	requireCmd(t, "bash")
	const inherited = "MADE_TEST_ENV_INHERITED"
	const empty = "MADE_TEST_ENV_EMPTY"
	const loaded = "MADE_TEST_ENV_LOADED"
	t.Setenv(inherited, "process")
	t.Setenv(empty, "")
	t.Setenv(loaded, "")
	if err := os.Unsetenv(loaded); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "Madefile.md")
	script := "[ \"$" + inherited + "\" = process ]\n[ \"$" + empty + "\" = '' ]\n[ \"${" + loaded + "-unset}\" = \"$1\" ]\n"
	if err := os.WriteFile(path, []byte("### task\n```bash\n"+script+"```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := inherited + "=file\n" + empty + "=file\n" + loaded + "=first\n" + loaded + "='last $HOME $(false)'\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	checkRunCases(t, []runCase{
		{name: "not automatic", args: []string{"-f", path, "task", "unset"}},
		{name: "explicit", args: []string{"-f", path, "--env-file", ".env", "task", "last $HOME $(false)"}},
	})
	if _, ok := os.LookupEnv(loaded); ok {
		t.Error("dotenv changed made's own process environment")
	}
}

func TestPreviewEnvFileLocationAndPrivacy(t *testing.T) {
	requireCmd(t, "bash")
	path := writeRunTasks(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".env"), []byte("MADE_TEST_SECRET=secret-never-display-7263\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, noChdir := range []bool{false, true} {
		args := []string{"-f", path, "--env-file", ".env", "--print"}
		wantDir := filepath.Dir(path)
		if noChdir {
			args = append(args, "--no-chdir")
			wantDir = cwd
		}
		var out, errb bytes.Buffer
		if code := run(append(args, "hello"), &out, &errb); code != 0 {
			t.Fatalf("preview = %d, stderr %q", code, &errb)
		}
		if !strings.Contains(out.String(), "Working directory: "+strconv.Quote(wantDir)) {
			t.Errorf("preview working directory: %q", &out)
		}
		if strings.Contains(out.String()+errb.String(), "secret-never-display-7263") {
			t.Error("preview exposed an env-file value")
		}
	}
}

func TestEnvFileErrorsDoNotExposeValues(t *testing.T) {
	path := writeRunTasks(t)
	envPath := filepath.Join(filepath.Dir(path), ".env")
	for _, data := range []string{"BAD KEY=secret-never-display-7263\n", "VALUE='secret-never-display-7263\n", "VALUE=secret-never-display-7263\x00\n"} {
		if err := os.WriteFile(envPath, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if code := run([]string{"-f", path, "--env-file", ".env", "--print", "hello"}, &out, &errb); code != 1 {
			t.Errorf("malformed env = %d, stderr %q", code, &errb)
		}
		if !strings.Contains(errb.String(), "line 1:") || strings.Contains(out.String()+errb.String(), "secret-never-display-7263") {
			t.Errorf("parse error must identify the line without its value: %q", &errb)
		}
	}
}
