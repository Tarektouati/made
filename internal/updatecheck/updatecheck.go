// Package updatecheck tells the user, at most once a day, that a newer made release
// exists. The notice comes from a small cache written by an earlier run; a stale cache
// is refreshed in the background with one GET to the releases/latest URL, which the
// caller never waits for, so a check adds no latency to starting a task or exiting.
// Every failure (offline, DNS, proxy, rate limit, an unreadable cache) is silent.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// cacheName is the cache file in Config.CacheDir.
const cacheName = "update-check.json"

// maxCacheBytes bounds how much of the cache file is read.
const maxCacheBytes = 4 << 10

const (
	defaultTimeout        = time.Second
	defaultInterval       = 24 * time.Hour
	defaultAttemptBackoff = time.Hour
)

// Config describes the running made and where to look for releases.
type Config struct {
	// Current is the running version, as made --version reports it.
	Current string
	// Method is how the running binary was installed; it picks the upgrade command.
	Method selfupdate.Method
	// Client resolves the latest release.
	Client selfupdate.Client
	// CacheDir holds the cache file; it is created if missing.
	CacheDir string
	// Timeout bounds the background refresh; zero means 1s.
	Timeout time.Duration
	// Interval is the least time between two checks, and between two notices; zero
	// means 24h.
	Interval time.Duration
	// AttemptBackoff is the least time between two attempts at a check, for an
	// attempt that never finished; zero means 1h.
	AttemptBackoff time.Duration
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

// state is the cache file's content.
type state struct {
	CheckedAt   time.Time `json:"checked_at,omitzero"`
	AttemptedAt time.Time `json:"attempted_at,omitzero"`
	Latest      string    `json:"latest,omitempty"`
	NotifiedAt  time.Time `json:"notified_at,omitzero"`
}

// Check is a started update check.
type Check struct {
	notice string
	done   chan struct{}
}

// Start reads the cache, picks the notice to show from it, and, when the last
// finished check is older than the interval and the last attempt older than the
// attempt backoff, starts refreshing the cache in the background. It does not wait
// for the network. The attempt, and the notice when one is due, are recorded right
// away, so a run that exits before the refresh finishes still counts, and
// concurrent runs rarely check or notify twice.
func Start(cfg Config) *Check {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.AttemptBackoff <= 0 {
		cfg.AttemptBackoff = defaultAttemptBackoff
	}
	c := &Check{done: make(chan struct{})}
	now := cfg.Now()
	path := filepath.Join(cfg.CacheDir, cacheName)
	st := load(path)

	changed := false
	if cfg.Method != selfupdate.Dev && selfupdate.Newer(cfg.Current, st.Latest) && due(st.NotifiedAt, now, cfg.Interval) {
		c.notice = fmt.Sprintf("made: %s is available (you have %s); run %s", st.Latest, cfg.Current, selfupdate.Hint(cfg.Method, st.Latest))
		st.NotifiedAt = now
		changed = true
	}
	refresh := due(st.CheckedAt, now, cfg.Interval) && due(st.AttemptedAt, now, cfg.AttemptBackoff)
	if refresh {
		// Recorded before the request: made exits without waiting for it, which
		// abandons a refresh that has not finished, and the next runs must not retry
		// until the backoff is over.
		st.AttemptedAt = now
		changed = true
	}
	if changed {
		_ = save(path, st)
	}
	if !refresh {
		close(c.done)
		return c
	}
	go func() {
		defer close(c.done)
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()
		tag, err := cfg.Client.LatestTag(ctx)
		// Reloaded so that what other runs wrote meanwhile (a notice shown, an
		// attempt) survives; the refresh owns only these fields.
		cur := load(path)
		if err == nil && valid(tag) {
			cur.Latest = tag
		}
		cur.CheckedAt = cfg.Now()
		_ = save(path, cur)
	}()
	return c
}

// Notice returns the line to print on stderr, without a newline, or "" when there is
// nothing to say. It never waits for the refresh.
func (c *Check) Notice() string {
	if c == nil {
		return ""
	}
	return c.notice
}

// Done is closed once the background refresh, if any, has finished. made never waits
// on it; tests do.
func (c *Check) Done() <-chan struct{} {
	return c.done
}

// due reports whether at least interval has passed since t. A zero t, or one in the
// future (a clock set back), is due.
func due(t, now time.Time, interval time.Duration) bool {
	return t.IsZero() || t.After(now) || now.Sub(t) >= interval
}

// valid reports whether tag is a release version made can name in a notice: one
// that NormalizeTag leaves unchanged (so it holds no control characters) and that
// is not a pre-release.
func valid(tag string) bool {
	norm, err := selfupdate.NormalizeTag(tag)
	return err == nil && norm == tag && selfupdate.IsRelease(tag)
}

// load reads the cache at path. A missing, unreadable or malformed cache, or one
// whose latest version is not valid, reads as empty, so the next check overwrites it.
func load(path string) state {
	f, err := os.Open(path)
	if err != nil {
		return state{}
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxCacheBytes+1))
	if err != nil || len(b) > maxCacheBytes {
		return state{}
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil || (st.Latest != "" && !valid(st.Latest)) {
		return state{}
	}
	return st
}

// save writes st to path atomically: to a temporary file in the same directory, then
// renamed over path, so a concurrent run never reads a partial cache.
func save(path string, st state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+cacheName+"-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
