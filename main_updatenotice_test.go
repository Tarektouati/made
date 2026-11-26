package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// noticeLine is the notice for a v0.6.0 manual install when v0.7.0 is out.
const noticeLine = "made: v0.7.0 is available (you have v0.6.0); run made self-update\n"

// noticeTasksMD has a passing and a failing task.
const noticeTasksMD = "### ok\n```sh\nexit 0\n```\n\n### fail\n```sh\nexit 3\n```\n"

// noticeEnv is an update check in a test: made sees itself as a v0.6.0 release
// installed by install.sh, on a terminal, outside CI and with no opt-out, checking
// srv for releases with its cache in cacheDir.
type noticeEnv struct {
	app      *app
	srv      *fakeReleases
	cacheDir string
}

// cachePath is the update check's cache file.
func (e noticeEnv) cachePath() string { return filepath.Join(e.cacheDir, "update-check.json") }

// writeCache stores latest as checked age ago.
func (e noticeEnv) writeCache(t *testing.T, latest string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(e.cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	checked := time.Now().Add(-age).UTC().Format(time.RFC3339Nano)
	content := `{"checked_at":"` + checked + `","latest":"` + latest + `"}`
	if err := os.WriteFile(e.cachePath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readCache returns the cache file's content, or "" if there is none.
func (e noticeEnv) readCache(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.cachePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

// useUpdateNotice returns an app with releases served by the fake GitHub.
func useUpdateNotice(t *testing.T, latest string) noticeEnv {
	t.Helper()
	e := noticeEnv{app: newTestApp(), srv: newFakeReleases(t, latest), cacheDir: filepath.Join(t.TempDir(), "made")}
	e.app.version = "0.6.0"
	e.app.selfUpdateBaseURL = e.srv.srv.URL
	e.app.detectInstall = func(v string, stamped bool) selfupdate.Install {
		return selfupdate.Install{Exe: "/usr/local/bin/made", Version: v, Stamped: stamped, GOOS: "linux", GOARCH: "amd64"}
	}
	e.app.updateCacheDir = func() (string, error) { return e.cacheDir, nil }
	e.app.isTerminal = func(io.Writer) bool { return true }
	for _, k := range []string{"MADE_NO_UPDATE_CHECK", "NO_UPDATE_NOTIFIER", "CI"} {
		unsetenv(t, k)
	}
	return e
}

// unsetenv unsets key for the rest of the test.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // restores the old value at the end of the test
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func writeNoticeTasks(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(noticeTasksMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestUpdateNoticeShown checks that a list or a task run prints the cached notice
// as one line on stderr, with stdout and the exit code the same as without it.
func TestUpdateNoticeShown(t *testing.T) {
	f := writeNoticeTasks(t)
	for _, tt := range []struct {
		name     string
		args     []string
		wantCode int
		needSh   bool
	}{
		{name: "list", args: []string{"-f", f}},
		{name: "list flag", args: []string{"-f", f, "-l"}},
		{name: "passing task", args: []string{"-f", f, "ok"}, needSh: true},
		{name: "failing task", args: []string{"-f", f, "fail"}, wantCode: 3, needSh: true},
		{name: "unknown task", args: []string{"-f", f, "nope"}, wantCode: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needSh {
				requireCmd(t, "sh")
			}
			e := useUpdateNotice(t, "v0.7.0")
			e.writeCache(t, "v0.7.0", time.Hour)

			t.Setenv("MADE_NO_UPDATE_CHECK", "1")
			var baseOut, baseErr bytes.Buffer
			baseCode := e.app.run(tt.args, &baseOut, &baseErr)
			unsetenv(t, "MADE_NO_UPDATE_CHECK")

			var stdout, stderr bytes.Buffer
			code := e.app.run(tt.args, &stdout, &stderr)
			if code != tt.wantCode || code != baseCode {
				t.Errorf("exit code = %d, want %d (opted out: %d)", code, tt.wantCode, baseCode)
			}
			if stdout.String() != baseOut.String() {
				t.Errorf("stdout = %q, want %q as without the notice", &stdout, &baseOut)
			}
			if got, want := stderr.String(), baseErr.String()+noticeLine; got != want {
				t.Errorf("stderr = %q, want %q", got, want)
			}
			if n := e.srv.requests.Load(); n != 0 {
				t.Errorf("%d requests with a fresh cache, want none", n)
			}
		})
	}
}

// TestUpdateNoticeOnceADay checks that a second run within 24h shows no notice.
func TestUpdateNoticeOnceADay(t *testing.T) {
	f := writeNoticeTasks(t)
	e := useUpdateNotice(t, "v0.7.0")
	e.writeCache(t, "v0.7.0", time.Hour)
	for i, want := range []string{noticeLine, ""} {
		var stdout, stderr bytes.Buffer
		e.app.run([]string{"-f", f}, &stdout, &stderr)
		if stderr.String() != want {
			t.Errorf("run %d: stderr = %q, want %q", i+1, &stderr, want)
		}
	}
}

// TestUpdateNoticeDisabled checks the opt-outs and the cases where printing would
// be unsafe or useless: no notice, no request, and the cache left alone.
func TestUpdateNoticeDisabled(t *testing.T) {
	f := writeNoticeTasks(t)
	for _, tt := range []struct {
		name      string
		env       map[string]string
		ttyStdout bool
		ttyStderr bool
		version   string
	}{
		{name: "MADE_NO_UPDATE_CHECK", env: map[string]string{"MADE_NO_UPDATE_CHECK": "1"}, ttyStdout: true, ttyStderr: true},
		{name: "NO_UPDATE_NOTIFIER", env: map[string]string{"NO_UPDATE_NOTIFIER": "1"}, ttyStdout: true, ttyStderr: true},
		{name: "CI", env: map[string]string{"CI": "true"}, ttyStdout: true, ttyStderr: true},
		{name: "CI set but empty", env: map[string]string{"CI": ""}, ttyStdout: true, ttyStderr: true},
		{name: "stdout redirected", ttyStderr: true},
		{name: "stderr redirected", ttyStdout: true},
		{name: "dev build", ttyStdout: true, ttyStderr: true, version: "dev"},
		{name: "pseudo-version", ttyStdout: true, ttyStderr: true, version: "v0.6.1-0.20260101000000-abcdef123456"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := useUpdateNotice(t, "v0.7.0")
			e.writeCache(t, "v0.7.0", 48*time.Hour) // a notice and a refresh are due
			before := e.readCache(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if tt.version != "" {
				e.app.version = tt.version
			}
			var stdout, stderr bytes.Buffer
			e.app.isTerminal = func(w io.Writer) bool {
				if w == io.Writer(&stdout) {
					return tt.ttyStdout
				}
				return w == io.Writer(&stderr) && tt.ttyStderr
			}
			// The background refresh could still be on its way when run returns, so
			// check that none starts rather than counting requests alone.
			if c := e.app.startUpdateCheck(&stdout, &stderr); c != nil {
				t.Error("startUpdateCheck started a check, want none")
				<-c.Done()
			}
			if code := e.app.run([]string{"-f", f}, &stdout, &stderr); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			if strings.Contains(stderr.String(), "is available") {
				t.Errorf("stderr = %q, want no notice", &stderr)
			}
			if n := e.srv.requests.Load(); n != 0 {
				t.Errorf("%d requests, want none", n)
			}
			if after := e.readCache(t); after != before {
				t.Errorf("cache = %q, want it unchanged (%q)", after, before)
			}
		})
	}
}

// TestUpdateNoticeExcludedCommands checks that commands used by scripts and tab
// completion, and the commands that need no task file, never check for updates.
func TestUpdateNoticeExcludedCommands(t *testing.T) {
	f := writeNoticeTasks(t)
	for _, tt := range []struct {
		name string
		args []string
		// requests is made self-update's own request count.
		requests bool
	}{
		{name: "list-names", args: []string{"-f", f, "--list-names"}},
		{name: "completion", args: []string{"completion", "bash"}},
		{name: "init", args: []string{"init"}},
		{name: "check", args: []string{"-f", f, "--check"}},
		{name: "-V", args: []string{"-V"}},
		{name: "--version", args: []string{"--version"}},
		{name: "help", args: []string{"-h"}},
		{name: "bad flag", args: []string{"-bogus"}},
		{name: "self-update", args: []string{"self-update", "--dry-run"}, requests: true},
		{name: "self-update help", args: []string{"self-update", "--help"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir()) // for init
			e := useUpdateNotice(t, "v0.7.0")
			e.writeCache(t, "v0.7.0", 48*time.Hour) // a notice and a refresh are due
			before := e.readCache(t)
			var stdout, stderr bytes.Buffer
			e.app.run(tt.args, &stdout, &stderr)
			if strings.Contains(stderr.String(), "is available") {
				t.Errorf("stderr = %q, want no notice", &stderr)
			}
			if n := e.srv.requests.Load(); n != 0 && !tt.requests {
				t.Errorf("%d requests, want none", n)
			}
			if after := e.readCache(t); after != before {
				t.Errorf("cache = %q, want it unchanged (%q)", after, before)
			}
		})
	}
}

// TestUpdateNoticeNoLatency checks that a release server that never answers does
// not delay made, even with a long check timeout.
func TestUpdateNoticeNoLatency(t *testing.T) {
	f := writeNoticeTasks(t)
	e := useUpdateNotice(t, "v0.7.0")
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
			w.WriteHeader(http.StatusServiceUnavailable)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { unblock(); hang.Close() })
	e.app.selfUpdateBaseURL = hang.URL
	e.app.updateTimeout = time.Minute

	begin := time.Now()
	var stdout, stderr bytes.Buffer
	code := e.app.run([]string{"-f", f}, &stdout, &stderr)
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Errorf("run took %v with a hanging release server, want no wait", elapsed)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %s)", code, &stderr)
	}

	// Let the background refresh record its failed check before the cache directory
	// is removed. The attempt itself is recorded before the request.
	unblock()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(e.readCache(t), `"checked_at"`) {
		if time.Now().After(deadline) {
			t.Fatal("the background refresh never wrote the cache")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
