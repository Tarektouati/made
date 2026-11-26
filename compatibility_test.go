package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestMadefileVersionWarning(t *testing.T) {
	oldVersion := version
	t.Cleanup(func() { version = oldVersion })
	const task = "### hello\n```sh\nexit 0\n```\n"
	for _, tc := range []struct {
		name, current, header string
		wantWarning           bool
	}{
		{name: "newer patch", current: "0.7.0", header: "<!-- made v0.7.1 -->\n", wantWarning: true},
		{name: "newer minor", current: "0.7.0", header: "<!-- made v0.8.0 -->\n", wantWarning: true},
		{name: "newer major", current: "0.7.0", header: "<!-- made v1.0.0 -->\n", wantWarning: true},
		{name: "numeric comparison", current: "0.9.0", header: "<!-- made v0.10.0 -->\n", wantWarning: true},
		{name: "BOM and CRLF", current: "0.7.0", header: "\ufeff<!-- made v0.8.0 -->\r\n", wantWarning: true},
		{name: "CR only", current: "0.7.0", header: "<!-- made v0.8.0 -->\r", wantWarning: true},
		{name: "horizontal whitespace", current: "0.7.0", header: " \t<!-- made v0.8.0 -->\t\n", wantWarning: true},
		{name: "build metadata", current: "0.7.0+local", header: "<!-- made v0.8.0+build.1 -->\n", wantWarning: true},
		{name: "equal", current: "0.7.0", header: "<!-- made v0.7.0 -->\n"},
		{name: "equal with metadata", current: "0.7.0+local", header: "<!-- made v0.7.0+other -->\n"},
		{name: "older", current: "0.7.0", header: "<!-- made v0.6.0 -->\n"},
		{name: "no marker", current: "0.7.0"},
		{name: "development build", current: "dev", header: "<!-- made v0.8.0 -->\n"},
		{name: "prerelease build", current: "0.7.0-rc.1", header: "<!-- made v0.8.0 -->\n"},
		{name: "recorded prerelease", current: "0.7.0", header: "<!-- made v0.8.0-rc.1 -->\n"},
		{name: "incomplete version", current: "0.7.0", header: "<!-- made v0.8 -->\n"},
		{name: "leading zero", current: "0.7.0", header: "<!-- made v00.8.0 -->\n"},
		{name: "wrong comment", current: "0.7.0", header: "<!-- tool v0.8.0 -->\n"},
		{name: "later comment", current: "0.7.0", header: "# Tasks\n<!-- made v0.8.0 -->\n"},
		{name: "comment inside code", current: "0.7.0", header: "```text\n<!-- made v0.8.0 -->\n```\n"},
		{name: "inline comment", current: "0.7.0", header: "text <!-- made v0.8.0 -->\n"},
		{name: "extra comment", current: "0.7.0", header: "<!-- made v0.8.0 --> <!-- extra -->\n"},
		{name: "terminal escape in version", current: "0.7.0", header: "<!-- made v0.8.0+\x1b[31m -->\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version = tc.current
			source := tc.header + task
			if tc.name == "CR only" {
				source = strings.ReplaceAll(source, "\n", "\r")
			}
			path := writeTemp(t, "Madefile.md", source)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"-f", path, "--list"}, &stdout, &stderr); code != 0 {
				t.Fatalf("exit = %d; stderr %q", code, &stderr)
			}
			if !strings.Contains(stdout.String(), "hello") || strings.Contains(stdout.String(), "written with") {
				t.Errorf("task list = %q", &stdout)
			}
			if tc.wantWarning {
				if !strings.Contains(stderr.String(), "written with made v") || !strings.Contains(stderr.String(), "you have "+resolveVersion()) || !strings.HasSuffix(stderr.String(), "tasks may behave differently\n") {
					t.Errorf("warning = %q", &stderr)
				}
			} else if stderr.Len() != 0 {
				t.Errorf("unexpected warning: %q", &stderr)
			}
		})
	}
}

func TestMadefileVersionWarningLeavesQuietModesQuiet(t *testing.T) {
	oldVersion := version
	t.Cleanup(func() { version = oldVersion })
	version = "0.7.0"
	path := writeTemp(t, "Madefile.md", "<!-- made v0.8.0 -->\n### hello\n```sh\nexit 0\n```\n")
	for _, args := range [][]string{
		{"-f", path, "--list-names"},
		{"-f", path, "--check"},
		{"-f", path, "--check", "--strict"},
		{"-f", path, "--version"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
			t.Errorf("run(%q): exit %d, stderr %q", args, code, &stderr)
		}
	}
}

func TestMadefileVersionWarningKeepsTaskResult(t *testing.T) {
	requireCmd(t, "sh")
	oldVersion := version
	t.Cleanup(func() { version = oldVersion })
	version = "0.7.0"
	for _, want := range []int{0, 7} {
		path := writeTemp(t, "Madefile.md", fmt.Sprintf("<!-- made v0.8.0 -->\n### task\n```sh\n[ \"$#\" -eq 2 ] && [ \"$1\" = 'a b' ] && [ \"$2\" = '-x' ] || exit 9\nexit %d\n```\n", want))
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-f", path, "task", "a b", "-x"}, &stdout, &stderr); code != want {
			t.Errorf("exit = %d, want %d; stderr %q", code, want, &stderr)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "written with made v0.8.0") {
			t.Errorf("stdout %q, stderr %q", &stdout, &stderr)
		}
	}
}
