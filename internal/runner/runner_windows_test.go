//go:build windows

package runner

import (
	"strings"
	"testing"
)

// terminalCancel skips t: Windows has no terminal foreground group.
func terminalCancel(t *testing.T) error {
	t.Skip("no terminal signals on windows")
	return nil
}

// TestScriptTooLargeCountsUTF16 checks that the Windows limit, which CreateProcess
// counts in UTF-16 code units, is not applied to the script's UTF-8 bytes.
func TestScriptTooLargeCountsUTF16(t *testing.T) {
	script := strings.Repeat("漢", 12000) // 36,000 bytes, 12,000 UTF-16 units
	if len(script) <= ArgLimit {
		t.Fatalf("script is %d bytes, want more than %d", len(script), ArgLimit)
	}
	if got := ScriptLen("bash", script); got != 12000 {
		t.Errorf("ScriptLen = %d, want 12000", got)
	}
	if ScriptTooLarge("bash", script) {
		t.Errorf("ScriptTooLarge(%d bytes, 12000 UTF-16 units) = true, want false", len(script))
	}
}
