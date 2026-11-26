package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/lint"
)

func readJSONOutput(t *testing.T, args []string, wantCode int) ([]map[string]any, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	if code != wantCode {
		t.Fatalf("run(%q) = %d, want %d; stdout %q, stderr %q", args, code, wantCode, &stdout, &stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "[") {
		t.Fatalf("stdout = %q, want a JSON array", &stdout)
	}
	var result []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout %q: %v", &stdout, err)
	}
	return result, stderr.String()
}

func TestListJSON(t *testing.T) {
	path := writeTemp(t, "tasks.md", "### first\nFirst paragraph.\n\nSecond paragraph.\n```sh\necho hidden-script\n```\n### second\n```python\nprint(1)\n```\n")
	got, stderr := readJSONOutput(t, []string{"-f", path, "--list", "--json", "ignored-task"}, 0)
	want := []map[string]any{
		{"name": "first", "description": "First paragraph.\nSecond paragraph.", "language": "sh", "line": float64(1)},
		{"name": "second", "description": "", "language": "python", "line": float64(8)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %#v, want %#v", got, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestListJSONEmpty(t *testing.T) {
	for _, content := range []string{"# Only prose\n", "## Empty group\n", "### No code block\n"} {
		path := writeTemp(t, "empty.md", content)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-f", path, "--list", "--json"}, &stdout, &stderr); code != 0 {
			t.Fatalf("run = %d; stderr %q", code, &stderr)
		}
		if stdout.String() != "[]\n" || stderr.Len() != 0 {
			t.Errorf("stdout %q, stderr %q; want [] and no diagnostic", &stdout, &stderr)
		}
	}
}

func TestListJSONGroupsAndDuplicates(t *testing.T) {
	path := writeTemp(t, "tasks.md", "## Build &amp; test\n### one\n```sh\ntrue\n```\n## Other\n### one\n```sh\nfalse\n```\n### <br>\n```sh\nfalse\n```\n")
	got, stderr := readJSONOutput(t, []string{"-f", path, "-l", "--json"}, 0)
	want := []map[string]any{{"name": "one", "description": "", "language": "sh", "line": float64(2), "group": "Build & test"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %#v, want %#v", got, want)
	}
	for _, text := range []string{"defined again at line 7", "empty name"} {
		if !strings.Contains(stderr, text) {
			t.Errorf("stderr %q does not contain %q", stderr, text)
		}
	}
}

func TestCheckJSON(t *testing.T) {
	for _, tc := range []struct {
		name, source      string
		strict            bool
		code              int
		rules, severities []string
		lines             []int
	}{
		{name: "clean", source: "### ok\n```sh\ntrue\n```\n"},
		{name: "warning", source: "### no script\n", rules: []string{"no-code-block"}, severities: []string{"warning"}, lines: []int{1}},
		{name: "strict warning", source: "### no script\n", strict: true, code: 1, rules: []string{"no-code-block"}, severities: []string{"warning"}, lines: []int{1}},
		{name: "errors and warning", source: "### a\n```sh\ntrue\n```\n### a\n```sh\n```\n### b\n", code: 1, rules: []string{"duplicate-task", "empty-script", "no-code-block"}, severities: []string{"error", "error", "warning"}, lines: []int{5, 6, 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "tasks.md", tc.source)
			args := []string{"-f", path, "--check", "--format", "json"}
			if tc.strict {
				args = append(args, "--strict")
			}
			got, stderr := readJSONOutput(t, args, tc.code)
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
			if len(got) != len(tc.rules) {
				t.Fatalf("diagnostics = %#v, want %d", got, len(tc.rules))
			}
			for i, d := range got {
				if len(d) != 5 || d["file"] != path || d["line"] != float64(tc.lines[i]) || d["severity"] != tc.severities[i] || d["rule"] != tc.rules[i] {
					t.Errorf("diagnostic %d = %#v", i, d)
				}
				if message, ok := d["message"].(string); !ok || message == "" {
					t.Errorf("diagnostic message = %#v", d["message"])
				}
			}
		})
	}
}

func TestCheckGitHub(t *testing.T) {
	path := writeTemp(t, "bad%,line=99.md", "### a\n```sh\ntrue\n```\n### a\n```sh\n```\n### b\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-f", path, "--check", "--format", "github"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run = %d; stderr %q", code, &stderr)
	}
	file := strings.NewReplacer("%", "%25", ",", "%2C", ":", "%3A").Replace(path)
	want := "::error file=" + file + ",line=5,title=duplicate-task::\"a\" already defined at line 1\n" +
		"::error file=" + file + ",line=6,title=empty-script::task \"a\" has an empty code block\n" +
		"::warning file=" + file + ",line=8,title=no-code-block::heading \"b\" has no code block, so it is not a task\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("stdout:\n%s\nwant:\n%s\nstderr: %q", &stdout, want, &stderr)
	}
}

const groupedTasksMD = "### loose\nRoot.\n```sh\nexit 0\n```\n" +
	"## Build &amp; test\n### one\nFirst.\n```sh\nexit 0\n```\n" +
	"## Build &amp; test\n### two\nSecond.\n```sh\nexit 0\n```\n" +
	"# Restart\n### tail\nLast.\n```sh\nexit 0\n```\n"

func TestGroupedList(t *testing.T) {
	path := writeTemp(t, "tasks.md", groupedTasksMD)
	for _, flags := range [][]string{nil, {"-l"}} {
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"-f", path}, flags...), &stdout, &stderr); code != 0 {
			t.Fatalf("run = %d; stderr %q", code, &stderr)
		}
		want := "Tasks in " + path + ":\n  loose  Root.\n\n  Build & test:\n    one    First.\n\n  Build & test:\n    two    Second.\n\n  tail   Last.\n"
		if stdout.String() != want || stderr.Len() != 0 {
			t.Errorf("stdout:\n%s\nwant:\n%s\nstderr: %q", &stdout, want, &stderr)
		}
	}
}

func TestGroupedListNamesStayFlat(t *testing.T) {
	path := writeTemp(t, "tasks.md", groupedTasksMD)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-f", path, "--list-names"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d; stderr %q", code, &stderr)
	}
	want := "loose\tRoot.\none\tFirst.\ntwo\tSecond.\ntail\tLast.\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("stdout %q, want %q; stderr %q", &stdout, want, &stderr)
	}
}

func TestGroupedTaskLookupStaysFlat(t *testing.T) {
	requireCmd(t, "sh")
	path := writeTemp(t, "tasks.md", groupedTasksMD)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-f", path, "one"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(one) = %d; stderr %q", code, &stderr)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout %q, stderr %q; want no grouping output while running", &stdout, &stderr)
	}
}

func TestOutputModeConflicts(t *testing.T) {
	path := writeRunTasks(t)
	checkRunCases(t, []runCase{
		{name: "json alone", args: []string{"--json"}, wantCode: 2, wantStderr: []string{"--json requires --list"}},
		{name: "json list disabled", args: []string{"--json", "--list=false"}, wantCode: 2, wantStderr: []string{"--json requires --list"}},
		{name: "format alone", args: []string{"--format", "json"}, wantCode: 2, wantStderr: []string{"--format requires --check"}},
		{name: "text format alone", args: []string{"--format", "text"}, wantCode: 2, wantStderr: []string{"--format requires --check"}},
		{name: "format with list", args: []string{"-l", "--format", "json"}, wantCode: 2, wantStderr: []string{"--format requires --check"}},
		{name: "unknown format", args: []string{"--check", "--format", "xml"}, wantCode: 2, wantStderr: []string{"invalid format", "text, json, github"}},
		{name: "empty format", args: []string{"--check", "--format", ""}, wantCode: 2, wantStderr: []string{"invalid format"}},
		{name: "list and check", args: []string{"-f", path, "-l", "--check"}, wantCode: 2, wantStderr: []string{"--check does not take -l"}},
		{name: "json list and check", args: []string{"-f", path, "-l", "--json", "--check"}, wantCode: 2, wantStderr: []string{"--check does not take"}},
		{name: "json and version", args: []string{"--list", "--json", "--version"}, wantCode: 2, wantStderr: []string{"--json does not take --version"}},
		{name: "json and completion names", args: []string{"--list", "--json", "--list-names"}, wantCode: 2, wantStderr: []string{"--json does not take --list-names"}},
	})
}

// These are workflow commands as the runner sees them: every finding must occupy
// one physical line, and encoded input must not turn into a second command.
func TestGitHubAnnotationsEscapeUntrustedValues(t *testing.T) {
	path := "folder/a%0A,debug::x\r\n::error file=owned::bad.md"
	rule := "rule%,x:\r\n::warning::injected"
	message := "bad%0A\r\n::error file=pwn::injected\ncommas, colons: intact"
	diags := []lint.Diagnostic{
		{Line: 4, Severity: lint.Error, Rule: rule, Msg: message},
		{Severity: lint.Warning, Rule: "whole-file", Msg: "at file scope"},
	}
	var out bytes.Buffer
	errs, warns, err := printCheckDiagnostics(&out, path, diags, "github")
	if err != nil || errs != 1 || warns != 1 {
		t.Fatalf("printCheckDiagnostics = %d, %d, %v", errs, warns, err)
	}
	file := "folder/a%250A%2Cdebug%3A%3Ax%0D%0A%3A%3Aerror file=owned%3A%3Abad.md"
	want := "::error file=" + file + ",line=4,title=rule%25%2Cx%3A%0D%0A%3A%3Awarning%3A%3Ainjected::bad%250A%0D%0A::error file=pwn::injected%0Acommas, colons: intact\n" +
		"::warning file=" + file + ",title=whole-file::at file scope\n"
	if out.String() != want {
		t.Errorf("annotations = %q, want %q", &out, want)
	}
	if strings.Count(out.String(), "\n") != 2 || strings.Contains(out.String(), "\r") {
		t.Errorf("unescaped line ending in annotations: %q", &out)
	}
}

func TestCheckFormatsKeepExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		strict       bool
		code         int
	}{
		{name: "clean", source: "### ok\n```sh\ntrue\n```\n"},
		{name: "warning", source: "### no script\n"},
		{name: "strict warning", source: "### no script\n", strict: true, code: 1},
		{name: "error", source: "### bad\n```yaml\nx\n```\n", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "tasks.md", tc.source)
			args := []string{"-f", path, "--check"}
			if tc.strict {
				args = append(args, "--strict")
			}
			var plainOut, plainErr bytes.Buffer
			if code := run(args, &plainOut, &plainErr); code != tc.code {
				t.Fatalf("default text exit = %d, want %d", code, tc.code)
			}
			for _, format := range []string{"text", "json", "github"} {
				var stdout, stderr bytes.Buffer
				if code := run(append(args, "--format", format), &stdout, &stderr); code != tc.code {
					t.Errorf("format %s exit = %d, want %d; stderr %q", format, code, tc.code, &stderr)
				}
				if stderr.Len() != 0 {
					t.Errorf("format %s stderr = %q", format, &stderr)
				}
				if format == "text" && stdout.String() != plainOut.String() {
					t.Errorf("explicit text = %q, default = %q", &stdout, &plainOut)
				}
				if tc.name == "clean" {
					want := ""
					if format == "json" {
						want = "[]\n"
					}
					if stdout.String() != want {
						t.Errorf("clean %s output = %q, want %q", format, &stdout, want)
					}
				}
			}
		})
	}
}

func TestCheckStructuredReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.md")
	for _, format := range []string{"json", "github"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-f", path, "--check", "--format", format}, &stdout, &stderr); code != 1 {
			t.Errorf("format %s exit = %d, want 1", format, code)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "made: cannot read ") {
			t.Errorf("format %s stdout %q, stderr %q", format, &stdout, &stderr)
		}
	}
}

func TestCheckStructuredHost(t *testing.T) {
	path := writeTemp(t, "tasks.md", "### rb\n```ruby\nputs 'hi'\n```\n")
	t.Setenv("PATH", t.TempDir())
	got, stderr := readJSONOutput(t, []string{"-f", path, "--check", "--host", "--format", "json"}, 1)
	if len(got) != 1 || got[0]["rule"] != "interpreter-not-found" || got[0]["severity"] != "error" || stderr != "" {
		t.Errorf("host diagnostics = %#v, stderr %q", got, stderr)
	}
	var stdout, errout bytes.Buffer
	if code := run([]string{"-f", path, "--check", "--host", "--format", "github"}, &stdout, &errout); code != 1 {
		t.Errorf("GitHub host exit = %d, want 1", code)
	}
	if !strings.HasPrefix(stdout.String(), "::error file=") || !strings.Contains(stdout.String(), ",line=2,title=interpreter-not-found::") || errout.Len() != 0 {
		t.Errorf("GitHub host stdout %q, stderr %q", &stdout, &errout)
	}
}

func TestCheckStructuredWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.md")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxMadefileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, stderr := readJSONOutput(t, []string{"-f", path, "--check", "--format", "json"}, 1)
	if len(got) != 1 || got[0]["line"] != float64(0) || got[0]["rule"] != "file-too-large" || stderr != "" {
		t.Errorf("whole-file diagnostics = %#v, stderr %q", got, stderr)
	}
	var stdout, errout bytes.Buffer
	if code := run([]string{"-f", path, "--check", "--format", "github"}, &stdout, &errout); code != 1 {
		t.Errorf("GitHub whole-file exit = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), ",title=file-too-large::") || strings.Contains(stdout.String(), ",line=") || errout.Len() != 0 {
		t.Errorf("GitHub whole-file stdout %q, stderr %q", &stdout, &errout)
	}
}
