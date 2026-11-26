package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// fakeReleases stands in for github.com in the made self-update tests: it redirects
// releases/latest to latest (404 when latest is "") and serves files, keyed by
// "<tag>/<asset>".
type fakeReleases struct {
	srv      *httptest.Server
	latest   string
	files    map[string][]byte
	requests atomic.Int32
}

func newFakeReleases(t *testing.T, latest string) *fakeReleases {
	t.Helper()
	f := &fakeReleases{latest: latest, files: map[string][]byte{}}
	const prefix = "/" + selfupdate.Repo + "/releases/"
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		switch {
		case ok && rest == "latest" && f.latest != "":
			http.Redirect(w, r, prefix+"tag/"+f.latest, http.StatusFound)
		case ok && f.files[strings.TrimPrefix(rest, "download/")] != nil:
			_, _ = w.Write(f.files[strings.TrimPrefix(rest, "download/")])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// useFakeInstall returns an app that sees install in and reads releases from f.
func useFakeInstall(t *testing.T, f *fakeReleases, in selfupdate.Install) *app {
	t.Helper()
	a := newTestApp()
	a.selfUpdateBaseURL = f.srv.URL
	a.detectInstall = func(string, bool) selfupdate.Install { return in }
	return a
}

// oldBinary is the content of the made binary manualInstall writes.
const oldBinary = "#!/bin/sh\necho made v0.6.0\n"

// manualInstall is a release binary installed by install.sh, written to a new
// directory.
func manualInstall(t *testing.T) selfupdate.Install {
	t.Helper()
	in := selfupdate.Install{Exe: filepath.Join(t.TempDir(), "made"), Version: "v0.6.0", Stamped: true, GOOS: "linux", GOARCH: "amd64"}
	if err := os.WriteFile(in.Exe, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestSelfUpdateUsage(t *testing.T) {
	f := newFakeReleases(t, "v0.7.0")
	a := useFakeInstall(t, f, manualInstall(t))
	defer func() {
		if n := f.requests.Load(); n != 0 {
			t.Errorf("%d requests to the release server, want none", n)
		}
	}()
	a.checkRunCases(t, []runCase{
		{name: "with -f", args: []string{"-f", "tasks.md", "self-update"}, wantCode: 2, wantStderr: []string{"made: self-update does not take -f"}},
		{name: "with empty -f", args: []string{"-f", "", "self-update"}, wantCode: 2, wantStderr: []string{"-f requires a file path"}},
		{name: "-l before", args: []string{"-l", "self-update"}, wantCode: 2, wantStderr: []string{"made: self-update does not take -l"}},
		{name: "--no-chdir before", args: []string{"--no-chdir", "self-update"}, wantCode: 2, wantStderr: []string{"made: self-update does not take --no-chdir"}},
		{name: "two versions", args: []string{"self-update", "v1.0.0", "v2.0.0"}, wantCode: 2, wantStderr: []string{"made: self-update takes at most one version"}},
		{name: "unknown flag", args: []string{"self-update", "--bogus"}, wantCode: 2, wantStderr: []string{"made: unknown flag --bogus (see made self-update --help)"}, notStderr: []string{"Usage"}},
		{name: "unknown flag after version", args: []string{"self-update", "v1.0.0", "-x"}, wantCode: 2, wantStderr: []string{"made: unknown flag -x (see made self-update --help)"}},
		{name: "bad version", args: []string{"self-update", "vx"}, wantCode: 2, wantStderr: []string{`made: invalid version "vx"`}},
		// A pre-release would install as a dev build that self-update then refuses
		// to replace, so only releases can be named.
		{name: "pre-release", args: []string{"self-update", "v0.7.0-rc.1"}, wantCode: 2, wantStderr: []string{`made: invalid version "v0.7.0-rc.1": give a release such as v1.2.3`}},
		{name: "pre-release dry run", args: []string{"self-update", "--dry-run", "0.7.0-rc.1"}, wantCode: 2, wantStderr: []string{`made: invalid version "0.7.0-rc.1"`}},
		{name: "major only", args: []string{"self-update", "v1"}, wantCode: 2, wantStderr: []string{`made: invalid version "v1"`}},
		{name: "major and minor", args: []string{"self-update", "v1.2"}, wantCode: 2, wantStderr: []string{`made: invalid version "v1.2"`}},
		{name: "help", args: []string{"self-update", "-h"}, wantStdout: []string{"Usage: made self-update [vX.Y.Z] [--dry-run]", "    --dry-run", "-h, --help"}},
		{name: "main help", args: []string{"-h"}, wantStdout: []string{"made self-update [vX.Y.Z] [--dry-run]"}},
		{name: "check", args: []string{"--check", "self-update"}, wantCode: 2, wantStderr: []string{"made: --check takes no task name"}},
	})
}

// TestSelfUpdateIsReserved checks a user task named self-update never runs, and that
// made self-update needs no task file.
func TestSelfUpdateIsReserved(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	content := "### self-update\n```sh\ntouch " + marker + "\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "Madefile.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	f := newFakeReleases(t, "v0.7.0")
	a := useFakeInstall(t, f, manualInstall(t))
	var stdout, stderr bytes.Buffer
	if code := a.run([]string{"self-update", "--dry-run"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(self-update --dry-run) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if !strings.Contains(stdout.String(), "made: would update v0.6.0 -> v0.7.0") {
		t.Errorf("stdout = %q, want the dry-run report", &stdout)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the user's self-update task ran")
	}
}

// TestUpdateTaskStillRuns checks only self-update is reserved: a task named update,
// a common name for dependency updates, still runs.
func TestUpdateTaskStillRuns(t *testing.T) {
	requireCmd(t, "sh")
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	content := "### update\n```sh\ntouch '" + marker + "'\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "Madefile.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	if code := newTestApp().run([]string{"update"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(update) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the user's update task did not run: %v", err)
	}
}

func TestSelfUpdateDryRunArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"latest", []string{"self-update", "--dry-run"}, "made: would update v0.6.0 -> v0.8.0\n"},
		{"latest by name", []string{"self-update", "latest", "--dry-run"}, "made: would update v0.6.0 -> v0.8.0\n"},
		{"version first", []string{"self-update", "v0.7.0", "--dry-run"}, "made: would update v0.6.0 -> v0.7.0\n"},
		{"flag first", []string{"self-update", "--dry-run", "0.7.0"}, "made: would update v0.6.0 -> v0.7.0\n"},
		{"after --", []string{"self-update", "--dry-run", "--", "V0.7.0"}, "made: would update v0.6.0 -> v0.7.0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeReleases(t, "v0.8.0")
			in := manualInstall(t)
			a := useFakeInstall(t, f, in)
			var stdout, stderr bytes.Buffer
			if code := a.run(tc.args, &stdout, &stderr); code != 0 {
				t.Fatalf("run(%q) = %d, want 0 (stderr: %s)", tc.args, code, &stderr)
			}
			if !strings.HasPrefix(stdout.String(), tc.want) {
				t.Errorf("stdout = %q, want it to start with %q", &stdout, tc.want)
			}
			if b, err := os.ReadFile(in.Exe); err != nil || string(b) != oldBinary {
				t.Errorf("dry run changed %s: %q, %v", in.Exe, b, err)
			}
		})
	}
}

func TestSelfUpdateInstallMethods(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   selfupdate.Install
		want []string
	}{
		{"homebrew", selfupdate.Install{Exe: "/opt/homebrew/Caskroom/made/0.6.0/made", Version: "v0.6.0", Stamped: true, GOOS: "darwin", GOARCH: "arm64"},
			[]string{"made: made was installed with Homebrew; update it with: brew upgrade Tarektouati/tap/made\n"}},
		{"go install", selfupdate.Install{Exe: "/home/u/go/bin/made", Version: "v0.6.0", GOOS: "linux", GOARCH: "amd64", GOPATH: "/home/u/go"},
			[]string{"made: made was installed with go install; update it with: go install github.com/Tarektouati/made@latest\n"}},
		{"dev", selfupdate.Install{Exe: "/src/made/made", Version: "v0.6.1-0.20260101000000-abcdef123456", GOOS: "linux", GOARCH: "amd64"},
			[]string{"development build (v0.6.1-0.20260101000000-abcdef123456)", "go install github.com/Tarektouati/made@latest"}},
		{"windows", selfupdate.Install{Exe: `C:\bin\made.exe`, Version: "v0.6.0", Stamped: true, GOOS: "windows", GOARCH: "amd64"},
			[]string{"no release archive is published for windows/amd64", "go install github.com/Tarektouati/made@latest"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Exe = filepath.FromSlash(tc.in.Exe)
			tc.in.GOPATH = filepath.FromSlash(tc.in.GOPATH)
			f := newFakeReleases(t, "v0.7.0")
			a := useFakeInstall(t, f, tc.in)
			for _, args := range [][]string{{"self-update"}, {"self-update", "--dry-run"}, {"self-update", "v0.7.0"}} {
				var stdout, stderr bytes.Buffer
				if code := a.run(args, &stdout, &stderr); code != selfupdate.ExitNotSelfUpdatable {
					t.Errorf("run(%q) = %d, want %d (stderr: %s)", args, code, selfupdate.ExitNotSelfUpdatable, &stderr)
				}
				checkContains(t, "stderr", stderr.String(), tc.want, true)
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want nothing", &stdout)
				}
			}
			if n := f.requests.Load(); n != 0 {
				t.Errorf("%d requests to the release server, want none", n)
			}
		})
	}
}

func TestSelfUpdateLatestUnresolved(t *testing.T) {
	f := newFakeReleases(t, "")
	a := useFakeInstall(t, f, manualInstall(t))
	var stdout, stderr bytes.Buffer
	if code := a.run([]string{"self-update"}, &stdout, &stderr); code != selfupdate.ExitFailure {
		t.Errorf("run(self-update) = %d, want %d", code, selfupdate.ExitFailure)
	}
	if !strings.HasPrefix(stderr.String(), "made: cannot resolve the latest release: ") {
		t.Errorf("stderr = %q, want a cannot-resolve error", &stderr)
	}
}
