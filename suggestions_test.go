package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUnknownTaskSuggestions(t *testing.T) {
	f := writeTemp(t, "tasks.md", "### build\n```sh\nexit 99\n```\n### bundle\n```sh\nexit 99\n```\n### deploy\n```sh\nexit 99\n```\n### préparer\n```sh\nexit 99\n```\n")
	for _, tc := range []struct {
		name string
		want string
	}{
		{"biuld", `made: did you mean "build"?`},
		{"bu", `made: did you mean "build", "bundle"?`},
		{"BUILD", `made: did you mean "build"?`},
		{"préparerx", `made: did you mean "préparer"?`},
		{"unrelated", ""},
		{"", ""},
		{strings.Repeat("x", 1024), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{"-f", f, tc.name}, &stdout, &stderr); code != 1 {
				t.Fatalf("run = %d, want unknown-task exit 1", code)
			}
			if !strings.Contains(stdout.String(), "Tasks in ") || !strings.Contains(stderr.String(), "unknown task ") {
				t.Fatalf("missing error or task list: stdout=%q stderr=%q", &stdout, &stderr)
			}
			if tc.want == "" {
				if strings.Contains(stderr.String(), "did you mean") {
					t.Fatalf("unexpected suggestion: %q", &stderr)
				}
			} else if !strings.Contains(stderr.String(), tc.want+"\n\n") {
				t.Fatalf("stderr=%q, want suggestion %q before the list", &stderr, tc.want)
			}
		})
	}
}

func TestUnknownTaskSuggestionsAreLimitedAndQuoted(t *testing.T) {
	f := writeTemp(t, "tasks.md", "### tab\n```sh\nexit 99\n```\n### tag\n```sh\nexit 99\n```\n### tap\n```sh\nexit 99\n```\n### tar\n```sh\nexit 99\n```\n### say\"hi\n```sh\nexit 99\n```\n")
	checkRunCases(t, []runCase{
		{name: "limited", args: []string{"-f", f, "ta"}, wantCode: 1, wantStderr: []string{`did you mean "tab", "tag", "tap"?`}, notStderr: []string{`"tar"`}},
		{name: "quoted", args: []string{"-f", f, "sayhi"}, wantCode: 1, wantStderr: []string{`did you mean "say\"hi"?`}},
		{name: "listing does not suggest", args: []string{"-f", f, "--list", "ta"}, notStderr: []string{"did you mean", "unknown task"}},
	})
}
