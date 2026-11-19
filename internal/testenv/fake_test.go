package testenv

import (
	"fmt"
	"testing"
)

// fakeTB records how a helper ended a test without stopping the caller.
type fakeTB struct {
	testing.TB
	skipped, fatal string
}

func (f *fakeTB) Helper()                           {}
func (f *fakeTB) Skipf(format string, args ...any)  { f.skipped = fmt.Sprintf(format, args...) }
func (f *fakeTB) Fatalf(format string, args ...any) { f.fatal = fmt.Sprintf(format, args...) }
