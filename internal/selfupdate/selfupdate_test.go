package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// normalizeTagCases follow the case statements in install.sh.
// TestInstallTagMatchesSelfUpdate in e2e runs install.sh against NormalizeTag.
var normalizeTagCases = []struct {
	in, want string // want "" means rejected
}{
	{"v1.2.3", "v1.2.3"},
	{"1.2.3", "v1.2.3"},
	{"V1.2.3", "v1.2.3"},
	{"v1.2.3-rc.1+b", "v1.2.3-rc.1+b"},
	{"v0.10.0", "v0.10.0"},
	{"v1", "v1"},
	{"", ""},
	{"v", ""},
	{"vx", ""},
	{"x1.2.3", ""},
	{"vv1.2.3", ""},
	{"v1.2;rm", ""},
	{"v1.2.3/x", ""},
	{"v1.2 3", ""},
	{"v1.2.3_4", ""},
}

func TestNormalizeTag(t *testing.T) {
	for _, c := range normalizeTagCases {
		got, err := NormalizeTag(c.in)
		if c.want == "" {
			if err == nil {
				t.Errorf("NormalizeTag(%q) = %q, want an error", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("NormalizeTag(%q) = (%q, %v), want %q", c.in, got, err, c.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.6.0":                               true,
		"v0.10.0":                              true,
		"v1.2.3+meta":                          true,
		"v0.6.1-0.20260101000000-abcdef123456": false,
		"v0.7.0-rc.1":                          false,
		"(devel)":                              false,
		"dev":                                  false,
		"":                                     false,
		"0.6.0":                                false,
		"v1.2":                                 false,
		"v01.2.3":                              false,
		"v1.2.3.4":                             false,
		"v99999999999999999999.0.0":            false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v0.6.0", "v0.6.0", 0},
		{"v0.6.0", "v0.7.0", -1},
		{"v0.10.0", "v0.9.0", 1},
		{"v1.0.0", "v0.99.99", 1},
		{"v0.6.1", "v0.6.0", 1},
		{"v1.2.3+a", "v1.2.3+b", 0},
		{"dev", "v0.1.0", -1},
		{"v0.1.0", "v0.7.0-rc.1", 1},
		{"dev", "(devel)", 0},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		current, latest string
		want            bool
	}{
		{"v0.6.0", "v0.7.0", true},
		{"v0.9.0", "v0.10.0", true},
		{"v0.7.0", "v0.7.0", false},
		{"v0.8.0", "v0.7.0", false},
		{"v0.6.0", "v0.7.0-rc.1", false},
		{"v0.6.1-0.20260101000000-abcdef123456", "v0.7.0", false},
		{"dev", "v0.7.0", false},
		{"(devel)", "v0.7.0", false},
		{"v0.6.0", "", false},
	} {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestDetect(t *testing.T) {
	release := func(exe string) Install {
		return Install{Exe: exe, Version: "v0.6.0", Stamped: true, GOOS: "linux", GOARCH: "amd64"}
	}
	gopaths := strings.Join([]string{filepath.FromSlash("/home/u/go"), filepath.FromSlash("/opt/go")}, string(filepath.ListSeparator))
	for _, c := range []struct {
		name string
		in   Install
		want Method
	}{
		{"manual", release("/usr/local/bin/made"), Manual},
		{"manual in home", release("/home/u/.local/bin/made"), Manual},
		{"cask", release("/opt/homebrew/Caskroom/made/0.6.0/made"), Homebrew},
		{"cellar", release("/home/linuxbrew/.linuxbrew/Cellar/made/0.6.0/bin/made"), Homebrew},
		{"cellar-like name is not brew", release("/opt/MyCellar/made"), Manual},
		{"gobin", func() Install { in := release("/home/u/bin/made"); in.GOBIN = "/home/u/bin"; return in }(), GoInstall},
		{"gopath bin", func() Install { in := release("/opt/go/bin/made"); in.GOPATH = gopaths; return in }(), GoInstall},
		{"gopath not bin", func() Install { in := release("/opt/go/made"); in.GOPATH = gopaths; return in }(), Manual},
		{"unstamped release", Install{Exe: "/usr/local/bin/made", Version: "v0.6.0", GOOS: "linux", GOARCH: "amd64"}, GoInstall},
		{"pseudo-version", Install{Exe: "/tmp/made", Version: "v0.6.1-0.20260101000000-abcdef123456", GOOS: "linux", GOARCH: "amd64"}, Dev},
		{"pseudo-version in gobin", Install{Exe: "/home/u/bin/made", Version: "v0.6.1-0.20260101000000-abcdef123456", GOOS: "linux", GOARCH: "amd64", GOBIN: "/home/u/bin"}, GoInstall},
		{"devel", Install{Exe: "/tmp/made", Version: "dev", GOOS: "linux", GOARCH: "amd64"}, Dev},
		{"stamped snapshot", Install{Exe: "/tmp/made", Version: "v0.6.1-SNAPSHOT-abc", Stamped: true, GOOS: "linux", GOARCH: "amd64"}, Dev},
		{"windows", Install{Exe: `C:\bin\made.exe`, Version: "v0.6.0", Stamped: true, GOOS: "windows", GOARCH: "amd64"}, NoArchive},
		{"linux 386", Install{Exe: "/usr/local/bin/made", Version: "v0.6.0", Stamped: true, GOOS: "linux", GOARCH: "386"}, NoArchive},
		{"freebsd", Install{Exe: "/usr/local/bin/made", Version: "v0.6.0", Stamped: true, GOOS: "freebsd", GOARCH: "amd64"}, NoArchive},
	} {
		c.in.Exe = filepath.FromSlash(c.in.Exe)
		c.in.GOBIN = filepath.FromSlash(c.in.GOBIN)
		if got := Detect(c.in); got != c.want {
			t.Errorf("%s: Detect(%+v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestHint(t *testing.T) {
	for _, c := range []struct {
		m      Method
		latest string
		want   string
	}{
		{Manual, "v0.7.0", "made self-update"},
		{Homebrew, "v0.7.0", "brew upgrade Tarektouati/tap/made"},
		{GoInstall, "v0.7.0", "go install github.com/Tarektouati/made@latest"},
		{NoArchive, "v0.7.0", "go install github.com/Tarektouati/made@latest"},
		{Dev, "v0.7.0", "go install github.com/Tarektouati/made@v0.7.0"},
		{Dev, "", "go install github.com/Tarektouati/made@latest"},
	} {
		if got := Hint(c.m, c.latest); got != c.want {
			t.Errorf("Hint(%v, %q) = %q, want %q", c.m, c.latest, got, c.want)
		}
	}
}

func TestRefusal(t *testing.T) {
	in := Install{Exe: "/opt/homebrew/Caskroom/made/0.6.0/made", Version: "v0.6.1-0.20260101000000-abcdef123456", GOOS: "windows", GOARCH: "amd64"}
	for m, want := range map[Method][]string{
		Homebrew:  {"Homebrew", "brew upgrade Tarektouati/tap/made"},
		GoInstall: {"go install", "go install github.com/Tarektouati/made@latest"},
		Dev:       {"development build", in.Version, "go install github.com/Tarektouati/made@latest"},
		NoArchive: {"windows/amd64", "go install github.com/Tarektouati/made@latest"},
	} {
		got := Refusal(m, in)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("Refusal(%v) = %q, want it to contain %q", m, got, w)
			}
		}
	}
}

func TestAssetName(t *testing.T) {
	for _, c := range []struct {
		tag, goos, goarch, want string
	}{
		{"v1.2.3", "linux", "amd64", "made_1.2.3_linux_amd64.tar.gz"},
		{"v1.2.3", "linux", "arm64", "made_1.2.3_linux_arm64.tar.gz"},
		{"v0.7.0", "darwin", "amd64", "made_0.7.0_darwin_amd64.tar.gz"},
		{"v0.7.0", "darwin", "arm64", "made_0.7.0_darwin_arm64.tar.gz"},
		{"v1.2.3-rc.1", "darwin", "arm64", "made_1.2.3-rc.1_darwin_arm64.tar.gz"},
		{"v1.2.3", "windows", "amd64", ""},
		{"v1.2.3", "linux", "386", ""},
		{"v1.2.3", "freebsd", "amd64", ""},
	} {
		got, ok := AssetName(c.tag, c.goos, c.goarch)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("AssetName(%q, %q, %q) = (%q, %v), want %q", c.tag, c.goos, c.goarch, got, ok, c.want)
		}
	}
}

func TestReleaseURL(t *testing.T) {
	got := ReleaseURL("https://github.com/", "v1.2.3", "checksums.txt")
	if want := "https://github.com/Tarektouati/made/releases/download/v1.2.3/checksums.txt"; got != want {
		t.Errorf("ReleaseURL = %q, want %q", got, want)
	}
}

// latestServer answers releases/latest with h and counts every request; any other
// path is an error, since LatestTag must not follow the redirect.
func latestServer(t *testing.T, h http.HandlerFunc) (srv *httptest.Server, requests *atomic.Int32) {
	t.Helper()
	requests = new(atomic.Int32)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/Tarektouati/made/releases/latest" {
			t.Errorf("unexpected request %s: LatestTag must stop at the redirect", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

// redirectTo answers with status and a raw Location header.
func redirectTo(status int, location string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
	}
}

func TestLatestTag(t *testing.T) {
	for _, c := range []struct {
		name     string
		status   int
		location func(base string) string
	}{
		{"relative", http.StatusFound, func(string) string { return "/Tarektouati/made/releases/tag/v0.7.0" }},
		{"absolute", http.StatusFound, func(base string) string { return base + "/Tarektouati/made/releases/tag/v0.7.0" }},
		{"other host", http.StatusFound, func(string) string { return "https://127.0.0.1:1/Tarektouati/made/releases/tag/v0.7.0" }},
		{"moved permanently", http.StatusMovedPermanently, func(string) string { return "/Tarektouati/made/releases/tag/v0.7.0" }},
		{"temporary redirect", http.StatusTemporaryRedirect, func(string) string { return "tag/v0.7.0" }},
		{"unnormalized tag", http.StatusFound, func(string) string { return "/Tarektouati/made/releases/tag/0.7.0" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var base string
			srv, requests := latestServer(t, func(w http.ResponseWriter, r *http.Request) {
				redirectTo(c.status, c.location(base))(w, r)
			})
			base = srv.URL
			got, err := Client{BaseURL: srv.URL}.LatestTag(context.Background())
			if err != nil || got != "v0.7.0" {
				t.Fatalf("LatestTag = (%q, %v), want v0.7.0", got, err)
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("%d requests, want exactly 1", n)
			}
		})
	}
}

func TestLatestTagFailures(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"not found":    http.NotFound,
		"server error": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"no redirect":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("v0.7.0")) },
		"no location":  redirectTo(http.StatusFound, ""),
		"not modified": redirectTo(http.StatusNotModified, "/Tarektouati/made/releases/tag/v0.7.0"),
		// GitHub sends a repository without releases to its releases page.
		"no releases":    redirectTo(http.StatusFound, "/Tarektouati/made/releases"),
		"bad tag":        redirectTo(http.StatusFound, "/Tarektouati/made/releases/tag/v1;rm"),
		"escaped tag":    redirectTo(http.StatusFound, "/Tarektouati/made/releases/tag/v1%2Frm"),
		"trailing slash": redirectTo(http.StatusFound, "/Tarektouati/made/releases/tag/v0.7.0/"),
		"query":          redirectTo(http.StatusFound, "/Tarektouati/made/releases/tag/v0.7.0?x=1"),
		"fragment":       redirectTo(http.StatusFound, "/Tarektouati/made/releases/tag/v0.7.0#x"),
		"bad location":   redirectTo(http.StatusFound, "http://[::1"),
		"no path":        redirectTo(http.StatusFound, "https://127.0.0.1:1"),
	} {
		t.Run(name, func(t *testing.T) {
			srv, requests := latestServer(t, h)
			if got, err := (Client{BaseURL: srv.URL}).LatestTag(context.Background()); err == nil {
				t.Errorf("LatestTag = %q, want an error", got)
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("%d requests, want exactly 1", n)
			}
		})
	}
}

// TestLatestTagRefusesHTTPSDowngrade checks a redirect from https to plain http is
// refused, even though the tag is read from it without following it.
func TestLatestTagRefusesHTTPSDowngrade(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the plain http server was contacted")
	}))
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(redirectTo(http.StatusFound, plain.URL+"/Tarektouati/made/releases/tag/v0.7.0"))
	defer tlsSrv.Close()
	if got, err := (Client{BaseURL: tlsSrv.URL, HTTP: tlsSrv.Client()}).LatestTag(context.Background()); err == nil {
		t.Errorf("LatestTag = %q, want an error for the https to http redirect", got)
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "AAAA  made_1.2.3_linux_arm64.tar.gz\r\n" +
		"BBBB  made_1.2.3_linux_amd64.tar.gz\r\n" +
		"\n" +
		"cccc\n"
	if got, err := checksumFor([]byte(sums), "made_1.2.3_linux_amd64.tar.gz"); err != nil || got != "bbbb" {
		t.Errorf("checksumFor = (%q, %v), want bbbb", got, err)
	}
	if got, err := checksumFor([]byte(sums), "made_1.2.3_darwin_amd64.tar.gz"); err == nil {
		t.Errorf("checksumFor(missing) = %q, want an error", got)
	}
	if got, err := checksumFor([]byte(sums), "made_1.2.3_linux"); err == nil {
		t.Errorf("checksumFor(prefix) = %q, want an error", got)
	}
}
