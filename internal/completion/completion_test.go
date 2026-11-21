package completion

import (
	"regexp"
	"strings"
	"testing"
)

func TestScript(t *testing.T) {
	for _, shell := range Shells {
		if b, ok := Script(shell); !ok || len(b) == 0 {
			t.Errorf("Script(%q) = (%d bytes, %v), want a non-empty script", shell, len(b), ok)
		}
	}
	for _, shell := range []string{"", "pwsh", "made", "../made.bash"} {
		if _, ok := Script(shell); ok {
			t.Errorf("Script(%q) ok, want unsupported", shell)
		}
	}
}

// TestBashScriptNoFailingArithmetic guards the bash script against a bare (( ... )) or
// let statement, such as ((i++)): from bash 4.1, it returns status 1 when its value is
// 0, which exits a shell running with set -e. bash 3.2 ignores it, so the e2e tests
// only catch it on bash 4.1+. Conditions (if, while, for ...) and forms guarded with
// "|| :" or "|| true" may fail.
func TestBashScriptNoFailingArithmetic(t *testing.T) {
	b, ok := Script("bash")
	if !ok {
		t.Fatal(`Script("bash") is missing`)
	}
	re := regexp.MustCompile(`^\s*(\(\(|let\s)`)
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		m := re.FindStringSubmatch(line)
		if m == nil || strings.HasSuffix(line, "|| :") || strings.HasSuffix(line, "|| true") {
			continue
		}
		if m[1] == "((" {
			t.Errorf("made.bash:%d: %q returns 1 when its value is 0, which exits a set -e shell; use x=$((...)) or add || :", i+1, line)
		} else {
			t.Errorf("made.bash:%d: %q returns 1 when its last value is 0, which exits a set -e shell; use x=$((...)) or add || :", i+1, line)
		}
	}
}
