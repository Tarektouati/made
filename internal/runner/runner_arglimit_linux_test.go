package runner

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/parser"
)

// TestArgLimitMatchesKernel checks that ScriptTooLarge's boundary is where Linux starts
// rejecting a single argument (MAX_ARG_STRLEN, 32 pages including the NUL).
func TestArgLimitMatchesKernel(t *testing.T) {
	if os.Getpagesize() != 4096 {
		t.Skipf("page size %d: MAX_ARG_STRLEN is not 128 KiB", os.Getpagesize())
	}
	requireCmd(t, "bash")
	for _, n := range []int{ArgLimit - 1, ArgLimit} {
		script := ":" + strings.Repeat(" ", n-1)
		err := Run(context.Background(), nil, parser.Task{Name: "big", Script: script}, "", nil)
		tooLarge := ScriptTooLarge("", script)
		if (tooLarge && !argListTooLong(err)) || (!tooLarge && err != nil) {
			t.Errorf("%d bytes: ScriptTooLarge = %v, Run error = %v", n, tooLarge, err)
		}
	}
}
