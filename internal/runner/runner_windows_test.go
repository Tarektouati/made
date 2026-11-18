//go:build windows

package runner

import "testing"

// terminalCancel skips t: Windows has no terminal foreground group.
func terminalCancel(t *testing.T) error {
	t.Skip("no terminal signals on windows")
	return nil
}
