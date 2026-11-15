package completion

import "testing"

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
