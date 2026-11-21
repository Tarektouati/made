package runner

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/task"
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
		err := Run(context.Background(), nil, task.Task{Name: "big", Script: script}, "", nil)
		tooLarge := ScriptTooLarge("", script)
		if (tooLarge && !argListTooLong(err)) || (!tooLarge && err != nil) {
			t.Errorf("%d bytes: ScriptTooLarge = %v, Run error = %v", n, tooLarge, err)
		}
	}
}

// TestArgLimitBlamesArgumentAtBoundary checks that a script just under the limit, which
// Linux accepts, is not blamed when an argument at the limit makes exec fail.
func TestArgLimitBlamesArgumentAtBoundary(t *testing.T) {
	if os.Getpagesize() != 4096 {
		t.Skipf("page size %d: MAX_ARG_STRLEN is not 128 KiB", os.Getpagesize())
	}
	requireCmd(t, "bash")
	script := ":" + strings.Repeat(" ", ArgLimit-2)
	err := Run(context.Background(), nil, task.Task{Name: "s", Script: script}, "", []string{strings.Repeat("a", ArgLimit)})
	wantArgLimit(t, err,
		[]string{"arguments plus environment are too large", "script " + strconv.Itoa(ArgLimit-1) + " bytes", "1 argument of " + strconv.Itoa(ArgLimit) + " bytes"},
		[]string{"script is"})
}
