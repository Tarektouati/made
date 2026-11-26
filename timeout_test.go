package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestTimeoutUsage(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--timeout", ""}, "invalid timeout"},
		{[]string{"--timeout", "tomorrow", "build"}, "invalid timeout"},
		{[]string{"--timeout=5", "build"}, "invalid timeout"},
		{[]string{"--timeout=-1s", "build"}, "timeout must not be negative"},
		{[]string{"--timeout", "1s"}, "--timeout requires a task"},
		{[]string{"--timeout", "1s", "--list"}, "--timeout requires a task"},
		{[]string{"--timeout", "1s", "--list-names"}, "--timeout requires a task"},
		{[]string{"--timeout", "1s", "--version"}, "--timeout requires a task"},
		{[]string{"--timeout", "1s", "--check"}, "--check does not take --timeout"},
		{[]string{"--timeout", "1s", "init"}, "init does not take --timeout"},
		{[]string{"--timeout", "1s", "completion", "bash"}, "completion does not take --timeout"},
		{[]string{"--timeout", "1s", "self-update"}, "self-update does not take --timeout"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != 2 {
				t.Errorf("run(%q) = %d, want 2", tc.args, code)
			}
			if !strings.Contains(stderr.String(), tc.want) || stdout.Len() != 0 {
				t.Errorf("stdout = %q, stderr = %q, want no stdout and %q", &stdout, &stderr, tc.want)
			}
		})
	}
}
