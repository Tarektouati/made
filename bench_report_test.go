package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBenchReportGoSignificance runs scripts/bench-report.py with a fake benchstat
// that prints a canned CSV row, and checks the verdict of the Go benchmark row.
func TestBenchReportGoSignificance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake benchstat is a shell script")
	}
	requireCmd(t, "python3")

	// Columns: name, base, ci, head, ci, delta ("~" when not significant), p.
	cases := []struct {
		name string
		row  string
		want string
	}{
		{"significant but below floor", "Parse-4,1000000,±1%,1005000,±1%,+0.50%,p=0.000 n=10", "no"},
		{"regression above floor", "Parse-4,1000000,±1%,1025000,±1%,+2.50%,p=0.000 n=10", "⚠️ **regression**"},
		{"improvement above floor", "Parse-4,1000000,±1%,970000,±1%,-3.00%,p=0.000 n=10", "✅ improvement"},
		{"exactly at floor", "Parse-4,1000000,±1%,1020000,±1%,+2.00%,p=0.000 n=10", "⚠️ **regression**"},
		{"large but not significant", "Parse-4,1000000,±1%,1050000,±1%,~,p=0.200 n=10", "no"},
		{"missing on head", "Parse-4,1000000,±1%,,,,", "⚠️ **missing on head**"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"base.bench.txt": "BenchmarkParse-4 1000 1000000 ns/op\n",
				"head.bench.txt": "BenchmarkParse-4 1000 1000000 ns/op\n",
				"made-base":      "binary",
				"made-head":      "binary",
				"stat.csv":       ",sec/op,CI,sec/op,CI,vs base,P\n" + tc.row + "\n",
			}
			for name, content := range files {
				// #nosec G306 -- Non-sensitive fixture inside a private temporary directory.
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			bin := filepath.Join(dir, "bin")
			// #nosec G301 -- The fixture tree is inside a private temporary directory.
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\ncat '" + filepath.Join(dir, "stat.csv") + "'\n"
			// #nosec G306 -- Executable fixture inside a private temporary directory.
			if err := os.WriteFile(filepath.Join(bin, "benchstat"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			// #nosec G204 -- Runs the repository report script against the temporary benchmark fixtures.
			out, err := exec.CommandContext(t.Context(), "python3", "scripts/bench-report.py", dir).CombinedOutput()
			if err != nil {
				t.Fatalf("bench-report.py: %v\n%s", err, out)
			}
			var row string
			for line := range strings.Lines(string(out)) {
				if strings.HasPrefix(line, "| go bench `Parse-4`") {
					row = line
				}
			}
			cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(row), "| "), " |"), " | ")
			if got := cells[len(cells)-1]; len(cells) != 5 || got != tc.want {
				t.Errorf("go bench row = %q, want 5 cells with verdict %q\nfull output:\n%s", row, tc.want, out)
			}
			if !strings.Contains(string(out), "2% or more") {
				t.Errorf("footnote does not mention the 2%% floor:\n%s", out)
			}
		})
	}
}
