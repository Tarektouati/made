package updatecheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// fakeGitHub stands in for github.com: releases/latest redirects to the tag page of
// latest, or answers status when it is set. With hold set, every request waits for
// release (called at the latest when the test ends) before it is answered. No test
// reaches the real network.
type fakeGitHub struct {
	srv      *httptest.Server
	latest   string
	status   int
	hold     atomic.Bool
	released chan struct{}
	release  func()
	requests atomic.Int32
}

func newFakeGitHub(t *testing.T, latest string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{latest: latest, released: make(chan struct{})}
	f.release = sync.OnceFunc(func() { close(f.released) })
	const prefix = "/" + selfupdate.Repo + "/releases/"
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if f.hold.Load() {
			select {
			case <-f.released:
			case <-r.Context().Done():
				return
			}
		}
		switch {
		case f.status != 0:
			w.WriteHeader(f.status)
		case r.URL.Path == prefix+"latest":
			http.Redirect(w, r, prefix+"tag/"+f.latest, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, prefix+"tag/"):
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { f.release(); f.srv.Close() })
	return f
}

// t0 is the fixed "now" of the tests.
var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// config returns a Config for a v0.6.0 manual install checking f, with a cache in a
// new directory and the clock at now.
func config(t *testing.T, f *fakeGitHub, now time.Time) Config {
	t.Helper()
	return Config{
		Current:  "v0.6.0",
		Method:   selfupdate.Manual,
		Client:   selfupdate.Client{BaseURL: f.srv.URL},
		CacheDir: filepath.Join(t.TempDir(), "made"),
		Now:      func() time.Time { return now },
	}
}

func writeCache(t *testing.T, cfg Config, s state) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	writeRawCache(t, cfg, b)
}

func writeRawCache(t *testing.T, cfg Config, b []byte) {
	t.Helper()
	if err := os.MkdirAll(cfg.CacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.CacheDir, cacheName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readCache(t *testing.T, cfg Config) state {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cfg.CacheDir, cacheName))
	if err != nil {
		t.Fatal(err)
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("cache %q: %v", b, err)
	}
	return s
}

// finish starts a check with cfg, waits for its refresh, and returns its notice.
func finish(t *testing.T, cfg Config) string {
	t.Helper()
	c := Start(cfg)
	select {
	case <-c.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("refresh did not finish")
	}
	return c.Notice()
}

func TestFreshCacheMakesNoRequest(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	cfg := config(t, f, t0)
	writeCache(t, cfg, state{CheckedAt: t0.Add(-time.Hour), Latest: "v0.6.0"})
	if got := finish(t, cfg); got != "" {
		t.Errorf("notice = %q, want none", got)
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("%d requests, want none within the interval", n)
	}
}

func TestStaleCacheRefreshes(t *testing.T) {
	for name, cached := range map[string]*state{
		"no cache":  nil,
		"stale":     {CheckedAt: t0.Add(-25 * time.Hour), Latest: "v0.6.0"},
		"in future": {CheckedAt: t0.Add(time.Hour), Latest: "v0.6.0"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGitHub(t, "v0.7.0")
			cfg := config(t, f, t0)
			if cached != nil {
				writeCache(t, cfg, *cached)
			}
			finish(t, cfg)
			if n := f.requests.Load(); n == 0 {
				t.Fatal("no request, want a refresh")
			}
			got := readCache(t, cfg)
			if got.Latest != "v0.7.0" || !got.CheckedAt.Equal(t0) {
				t.Errorf("cache = %+v, want latest v0.7.0 checked at %v", got, t0)
			}
		})
	}
}

// TestNoticeFromCache checks that the notice comes from what an earlier run cached,
// not from the refresh started by this run.
func TestNoticeFromCache(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	cfg := config(t, f, t0)
	if got := finish(t, cfg); got != "" {
		t.Errorf("first run notice = %q, want none (nothing cached yet)", got)
	}
	want := "made: v0.7.0 is available (you have v0.6.0); run made self-update"
	if got := finish(t, cfg); got != want {
		t.Errorf("second run notice = %q, want %q", got, want)
	}
}

func TestNoticeRateLimited(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	cfg := config(t, f, t0)
	writeCache(t, cfg, state{CheckedAt: t0.Add(-time.Hour), Latest: "v0.7.0"})
	if got := finish(t, cfg); got == "" {
		t.Fatal("first notice missing")
	}
	if got := finish(t, cfg); got != "" {
		t.Errorf("second notice within 24h = %q, want none", got)
	}
	later := cfg
	later.Now = func() time.Time { return t0.Add(25 * time.Hour) }
	if got := finish(t, later); got == "" {
		t.Error("notice after 24h missing")
	}
}

func TestOfflineIsSilent(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		closed bool
	}{
		{name: "connection refused", closed: true},
		{name: "rate limited", status: http.StatusTooManyRequests},
		{name: "server error", status: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGitHub(t, "v0.7.0")
			f.status = tt.status
			cfg := config(t, f, t0)
			if tt.closed {
				f.srv.Close()
			}
			writeCache(t, cfg, state{CheckedAt: t0.Add(-48 * time.Hour), Latest: "v0.6.0"})
			if got := finish(t, cfg); got != "" {
				t.Errorf("notice = %q, want none", got)
			}
			got := readCache(t, cfg)
			if got.Latest != "v0.6.0" || !got.CheckedAt.Equal(t0) {
				t.Errorf("cache = %+v, want latest kept and checked at %v (no retry on every run)", got, t0)
			}
		})
	}
}

func TestHangingServerDoesNotBlock(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	f.hold.Store(true)
	cfg := config(t, f, t0)
	cfg.Timeout = 10 * time.Second
	writeCache(t, cfg, state{CheckedAt: t0.Add(-48 * time.Hour), Latest: "v0.7.0"})
	begin := time.Now()
	c := Start(cfg)
	got := c.Notice()
	if elapsed := time.Since(begin); elapsed > 500*time.Millisecond {
		t.Errorf("Start and Notice took %v with a hanging server, want no wait", elapsed)
	}
	if got == "" {
		t.Error("notice from cache missing while the refresh hangs")
	}
	select {
	case <-c.Done():
		t.Error("refresh finished, want it still waiting on the server")
	default:
	}
	// Let the refresh end before the cache directory is removed.
	f.release()
	<-c.Done()
}

func TestRefreshTimesOut(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	f.hold.Store(true)
	cfg := config(t, f, t0)
	cfg.Timeout = 50 * time.Millisecond
	finish(t, cfg)
	if got := readCache(t, cfg); !got.CheckedAt.Equal(t0) {
		t.Errorf("cache = %+v, want the timed-out check recorded", got)
	}
}

// TestUnfinishedRefreshBacksOff checks a run that exits (made exits without waiting
// for the refresh) before the release server answers: the attempt is recorded before
// the request, so later runs within the backoff send none, and the next attempt
// comes once the backoff is over.
func TestUnfinishedRefreshBacksOff(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	f.hold.Store(true)
	cfg := config(t, f, t0)
	cfg.Timeout = time.Minute
	at := func(d time.Duration) Config {
		c := cfg
		c.Now = func() time.Time { return t0.Add(d) }
		return c
	}

	var started []*Check
	t.Cleanup(func() {
		f.release()
		for _, c := range started {
			<-c.Done()
		}
	})
	started = append(started, Start(cfg)) // never waited on, like a run that exits
	if got := load(filepath.Join(cfg.CacheDir, cacheName)); !got.AttemptedAt.Equal(t0) || !got.CheckedAt.IsZero() {
		t.Errorf("cache right after Start = %+v, want the attempt at %v recorded and no check yet", got, t0)
	}
	for _, d := range []time.Duration{0, 59 * time.Minute} {
		c := Start(at(d))
		started = append(started, c)
		select {
		case <-c.Done():
		default:
			t.Errorf("Start %v after an unfinished attempt started a refresh, want none within the backoff", d)
		}
	}
	retry := Start(at(61 * time.Minute))
	started = append(started, retry)
	select {
	case <-retry.Done():
		t.Error("Start after the backoff started no refresh")
	default:
	}

	f.release()
	for _, c := range started {
		<-c.Done()
	}
	if n := f.requests.Load(); n != 2 {
		t.Errorf("%d requests, want 2 (the first attempt and the one after the backoff)", n)
	}
}

// TestRefreshKeepsOtherRunsFields checks that a refresh finishing after another run
// wrote the cache changes only what the refresh owns, so it does not bring back a
// notice that run already showed.
func TestRefreshKeepsOtherRunsFields(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	f.hold.Store(true)
	cfg := config(t, f, t0)
	cfg.Timeout = time.Minute
	writeCache(t, cfg, state{CheckedAt: t0.Add(-48 * time.Hour), Latest: "v0.6.0"})
	c := Start(cfg)

	other := state{CheckedAt: t0.Add(-48 * time.Hour), AttemptedAt: t0, Latest: "v0.6.0", NotifiedAt: t0.Add(time.Minute)}
	writeCache(t, cfg, other)
	f.release()
	<-c.Done()

	got := readCache(t, cfg)
	if !got.NotifiedAt.Equal(other.NotifiedAt) || !got.AttemptedAt.Equal(other.AttemptedAt) {
		t.Errorf("cache = %+v, want notified_at %v and attempted_at %v kept from the other run", got, other.NotifiedAt, other.AttemptedAt)
	}
	if got.Latest != "v0.7.0" || !got.CheckedAt.Equal(t0) {
		t.Errorf("cache = %+v, want latest v0.7.0 checked at %v from the refresh", got, t0)
	}
}

// TestFinishedCheckWaitsADay checks that once a check has finished, even with an
// error, the next one waits for the full interval, not just the attempt backoff.
func TestFinishedCheckWaitsADay(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0")
	f.status = http.StatusInternalServerError
	cfg := config(t, f, t0)
	finish(t, cfg)
	later := cfg
	later.Now = func() time.Time { return t0.Add(2 * time.Hour) }
	c := Start(later)
	select {
	case <-c.Done():
	default:
		<-c.Done()
		t.Error("Start 2h after a finished check started a refresh, want none for 24h")
	}
	if n := f.requests.Load(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestCorruptCache(t *testing.T) {
	for name, raw := range map[string]string{
		"garbage":       "\x00not json{",
		"escape latest": `{"checked_at":"2026-11-21T11:00:00Z","latest":"\u001b[31mv9.9.9"}`,
		"escape build":  `{"checked_at":"2026-11-21T11:00:00Z","latest":"v9.9.9+\u001b[31m"}`,
		"wrong types":   `{"checked_at":42,"latest":["v9.9.9"]}`,
		"empty":         ``,
		"not a release": `{"checked_at":"2026-11-21T11:00:00Z","latest":"latest"}`,
		"pre-release":   `{"checked_at":"2026-11-21T11:00:00Z","latest":"v9.9.9-rc.1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGitHub(t, "v0.6.0")
			cfg := config(t, f, t0)
			writeRawCache(t, cfg, []byte(raw))
			if got := finish(t, cfg); got != "" {
				t.Errorf("notice = %q, want none", got)
			}
			if got := readCache(t, cfg); got.Latest != "v0.6.0" {
				t.Errorf("cache = %+v, want it rewritten with latest v0.6.0", got)
			}
		})
	}
}

func TestNoNoticeWhenNotApplicable(t *testing.T) {
	for _, tt := range []struct {
		name    string
		current string
		method  selfupdate.Method
		latest  string
	}{
		{name: "same version", current: "v0.7.0", latest: "v0.7.0"},
		{name: "older latest", current: "v0.8.0", latest: "v0.7.0"},
		{name: "dev build", current: "dev", method: selfupdate.Dev, latest: "v0.7.0"},
		{name: "devel build", current: "(devel)", method: selfupdate.Dev, latest: "v0.7.0"},
		{name: "pseudo-version", current: "v0.6.1-0.20260101000000-abcdef123456", method: selfupdate.Dev, latest: "v0.7.0"},
		{name: "dev method with release version", current: "v0.6.0", method: selfupdate.Dev, latest: "v0.7.0"},
		{name: "pre-release current", current: "v0.7.0-rc.1", latest: "v0.7.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGitHub(t, tt.latest)
			cfg := config(t, f, t0)
			cfg.Current, cfg.Method = tt.current, tt.method
			writeCache(t, cfg, state{CheckedAt: t0.Add(-time.Hour), Latest: tt.latest})
			if got := finish(t, cfg); got != "" {
				t.Errorf("notice = %q, want none", got)
			}
		})
	}
}

func TestPreReleaseLatestIsNotCached(t *testing.T) {
	f := newFakeGitHub(t, "v0.7.0-rc.1")
	cfg := config(t, f, t0)
	writeCache(t, cfg, state{CheckedAt: t0.Add(-48 * time.Hour), Latest: "v0.6.0"})
	finish(t, cfg)
	if got := readCache(t, cfg); got.Latest != "v0.6.0" {
		t.Errorf("cache latest = %q, want the pre-release ignored", got.Latest)
	}
	if got := finish(t, cfg); got != "" {
		t.Errorf("notice = %q, want none for a pre-release", got)
	}
}

func TestNoticeNamesUpgradeCommand(t *testing.T) {
	for _, tt := range []struct {
		method selfupdate.Method
		want   string
	}{
		{selfupdate.Manual, "run made self-update"},
		{selfupdate.Homebrew, "run brew upgrade Tarektouati/tap/made"},
		{selfupdate.GoInstall, "run go install github.com/Tarektouati/made@latest"},
		{selfupdate.NoArchive, "run go install github.com/Tarektouati/made@latest"},
	} {
		t.Run(tt.method.String(), func(t *testing.T) {
			f := newFakeGitHub(t, "v0.7.0")
			cfg := config(t, f, t0)
			cfg.Method = tt.method
			writeCache(t, cfg, state{CheckedAt: t0.Add(-time.Hour), Latest: "v0.7.0"})
			if got := finish(t, cfg); !strings.HasSuffix(got, tt.want) {
				t.Errorf("notice = %q, want it to end with %q", got, tt.want)
			}
		})
	}
}

func TestCacheFileIsPrivate(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("no unix permissions on windows")
	}
	f := newFakeGitHub(t, "v0.7.0")
	cfg := config(t, f, t0)
	finish(t, cfg)
	info, err := os.Stat(filepath.Join(cfg.CacheDir, cacheName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache mode = %v, want 0600", perm)
	}
	entries, err := os.ReadDir(cfg.CacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("cache dir has %d entries, want only %s (no leftover temp files)", len(entries), cacheName)
	}
}

func TestNilCheck(t *testing.T) {
	var c *Check
	if got := c.Notice(); got != "" {
		t.Errorf("nil Check notice = %q, want none", got)
	}
}
