package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// newTestApp never contacts a real release server or writes the user's cache.
func newTestApp() *app {
	a := newApp("")
	a.selfUpdateBaseURL = "http://127.0.0.1:0"
	a.updateCacheDir = func() (string, error) { return "", errors.New("no cache directory in tests") }
	a.isTerminal = func(io.Writer) bool { return false }
	return a
}

// Each invocation must keep its version, install detection and release server even
// when another app is configured and running at the same time.
func TestAppDependenciesAreIndependent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version, current, latest string
	}{
		{"first", "1.2.3", "v1.2.3", "v1.3.0"},
		{"second", "v2.3.4", "v2.3.4", "v2.4.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeReleases(t, tc.latest)
			in := manualInstall(t)
			in.Version = tc.current
			a := newTestApp()
			a.version = tc.version
			a.selfUpdateBaseURL = f.srv.URL
			calls := 0
			a.detectInstall = func(v string, stamped bool) selfupdate.Install {
				calls++
				if v != tc.current || !stamped {
					t.Errorf("detectInstall(%q, %v), want (%q, true)", v, stamped, tc.current)
				}
				return in
			}
			var stdout, stderr bytes.Buffer
			if code := a.run([]string{"--version"}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(--version) = %d, stderr: %s", code, &stderr)
			}
			if got, want := stdout.String(), "made "+tc.current+"\n"; got != want {
				t.Errorf("version output = %q, want %q", got, want)
			}
			stdout.Reset()
			if code := a.run([]string{"self-update", "--dry-run"}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(self-update --dry-run) = %d, stderr: %s", code, &stderr)
			}
			if want := "made: would update " + tc.current + " -> " + tc.latest + "\n"; !strings.HasPrefix(stdout.String(), want) {
				t.Errorf("dry-run output = %q, want prefix %q", &stdout, want)
			}
			if calls != 1 || f.requests.Load() == 0 {
				t.Errorf("install calls = %d, release requests = %d; want one detection and a request to this app's server", calls, f.requests.Load())
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", &stderr)
			}
		})
	}
}
