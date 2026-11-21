//go:build windows

package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/parser"
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

// TestRunArgsCountUTF16 checks that the size of the task arguments in an ArgLimitError
// is counted in UTF-16 code units, as CreateProcess counts the command line: an emoji
// outside the Basic Multilingual Plane is 4 bytes but 2 units.
func TestRunArgsCountUTF16(t *testing.T) {
	requireCmd(t, "bash")
	arg := strings.Repeat("🙂", 20000) // 80,000 bytes, 40,000 UTF-16 units
	err := Run(context.Background(), nil, parser.Task{Name: "s", Script: "echo ok"}, "", []string{arg})
	var argErr *ArgLimitError
	if !errors.As(err, &argErr) {
		t.Fatalf("Run: error = %v (%T), want an *ArgLimitError", err, err)
	}
	if argErr.ArgsLen != 40000 {
		t.Errorf("ArgsLen = %d, want 40000 (UTF-16 units, not %d bytes)", argErr.ArgsLen, len(arg))
	}
	checkMessage(t, err.Error(), []string{"1 argument of 40000 UTF-16 characters"}, []string{"80000", "script is"})
}
