package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// PowerShell exposes its real completion engine without a terminal. Unlike the
// POSIX shells, this helper also runs on Windows.
func runPowerShell(t *testing.T, dir, script string, args ...string) string {
	t.Helper()
	requireCmd(t, "pwsh")
	cmd := exec.CommandContext(t.Context(), "pwsh", append([]string{"-NoProfile", "-NonInteractive", "-CommandWithArgs", script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Dir(madeBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"POWERSHELL_TELEMETRY_OPTOUT=1", "POWERSHELL_UPDATECHECK=Off",
		"MADE_COMPLETION_FILE="+filepath.Join(dir, "my tasks.md"),
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil || errb.Len() != 0 {
		t.Fatalf("pwsh: %v\nstdout: %s\nstderr: %s", err, &out, &errb)
	}
	return out.String()
}

func TestCompletionPowerShell(t *testing.T) {
	dir := completionDir(t)
	const script = `Invoke-Expression (& made completion powershell | Out-String)
$line = $args[0]
$result = [System.Management.Automation.CommandCompletion]::CompleteInput($line, $line.Length, $null)
ConvertTo-Json -Compress -InputObject @($result.CompletionMatches | ForEach-Object {
    @{name=$_.ListItemText; text=$_.CompletionText; description=$_.ToolTip}
})`
	for _, tc := range []completionCase{
		{line: []string{"made "}, want: []string{"build", "say-hello", "init", "completion", "self-update"}},
		{line: []string{"made b"}, want: []string{"build"}, not: []string{"say-hello"}},
		{line: []string{"made -f other.md "}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{`made --file="my tasks.md" `}, want: []string{"ns:task"}, not: []string{"build"}},
		{line: []string{`made -file 'my tasks.md' `}, want: []string{"ns:task"}},
		{line: []string{"made -f $PWD/other.md "}, want: []string{"ns:task"}},
		{line: []string{`made --file="$env:MADE_COMPLETION_FILE" `}, want: []string{"ns:task"}},
		{line: []string{`made -f '$env:MADE_COMPLETION_FILE' `}, not: []string{"ns:task", "build"}},
		{line: []string{`made -f "$([IO.File]::WriteAllText('PWNED', 'x'))/other.md" `}, not: []string{"ns:task", "build"}},
		{line: []string{`made -f $([IO.File]::WriteAllText('PWNED', 'x'))/other.md `}, not: []string{"ns:task", "build"}},
		{line: []string{`made -f other.md --file=Madefile.md b`}, want: []string{"build"}, not: []string{"ns:task"}},
		{line: []string{`made -f quote.md 'with s`}, want: []string{"with space"}},
		{line: []string{`made -f quote.md "dollar `}, want: []string{"dollar $(touch MARK)"}},
		{line: []string{"made -f my"}, want: []string{"my tasks.md"}, not: []string{"build"}},
		{line: []string{"made --file=my"}, want: []string{"my tasks.md"}, not: []string{"build"}},
		{line: []string{"made completion "}, want: []string{"bash", "zsh", "fish", "powershell"}, not: []string{"build"}},
		{line: []string{"made completion powershell "}, not: []string{"build", "powershell"}},
		{line: []string{"made init "}, want: []string{"--force"}, not: []string{"build"}},
		{line: []string{"made init --force "}, not: []string{"--force", "build"}},
		{line: []string{"made self-update v1.2.3 "}, want: []string{"--dry-run"}, not: []string{"build"}},
		{line: []string{"made self-update --dry-run "}, not: []string{"--dry-run", "build"}},
		{line: []string{"made --check "}, not: []string{"build", "init", "completion"}},
		{line: []string{"made --check --"}, want: []string{"--strict", "--host", "--file"}, not: []string{"--list", "--version"}},
		{line: []string{"made --"}, want: []string{"--file", "--check", "--list", "--version"}, not: []string{"--list-names"}},
		{line: []string{"made build "}, not: []string{"build", "--file"}},
	} {
		t.Run(tc.line[0], func(t *testing.T) {
			var matches []struct{ Name, Text, Description string }
			if err := json.Unmarshal([]byte(runPowerShell(t, dir, script, tc.line[0])), &matches); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, match := range matches {
				got = append(got, match.Name)
				if match.Name == "build" && match.Description != "Compiles all packages." {
					t.Errorf("build description = %q", match.Description)
				}
				if match.Name == "ns:task" && match.Description != "Has a colon: here." {
					t.Errorf("ns:task description = %q", match.Description)
				}
				if strings.HasPrefix(tc.line[0], "made --file=my") && !strings.HasPrefix(match.Text, "--file=") {
					t.Errorf("file completion lost its option: %q", match.Text)
				}
			}
			checkCompletions(t, got, tc)
			checkNoRun(t, dir)
		})
	}
}

func TestCompletionPowerShellQuoting(t *testing.T) {
	dir := completionDir(t)
	const script = `Invoke-Expression (& made completion powershell | Out-String)
function __made_test_word { ConvertTo-Json -Compress -InputObject @($args) }
$line = 'made -f quote.md '
$matches = [System.Management.Automation.CommandCompletion]::CompleteInput($line, $line.Length, $null).CompletionMatches
ConvertTo-Json -Compress -InputObject @(foreach ($match in $matches) {
    $words = & ([scriptblock]::Create('__made_test_word ' + $match.CompletionText)) | ConvertFrom-Json
    @{name=$match.ListItemText; words=@($words)}
})`
	var matches []struct {
		Name  string
		Words []string
	}
	if err := json.Unmarshal([]byte(runPowerShell(t, dir, script)), &matches); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, match := range matches {
		if !slices.Contains(quoteNames, match.Name) {
			continue
		}
		got = append(got, match.Name)
		if !slices.Equal(match.Words, []string{match.Name}) {
			t.Errorf("completion for %q became arguments %q", match.Name, match.Words)
		}
	}
	for _, name := range quoteNames {
		if !slices.Contains(got, name) {
			t.Errorf("missing completion for %q", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "MARK")); err == nil {
		t.Error("an inserted completion ran a command substitution")
	}
}

func TestCheckAdditionalLanguages(t *testing.T) {
	var md strings.Builder
	for _, lang := range []string{"pwsh", "powershell", "deno", "bun", "ts"} {
		md.WriteString("### " + lang + "\n```" + lang + "\nexit 0\n```\n")
	}
	dir := writeTasks(t, "Madefile.md", md.String())
	stdout, stderr, code := runMade(t, dir, "--check")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("--check: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
