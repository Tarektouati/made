// Package selfupdate finds, verifies and installs made releases. It resolves the
// latest release tag and names release assets the way install.sh does (the drift
// tests check both against install.sh and .goreleaser.yaml), compares versions, and
// detects how the running binary was installed, so that only a binary that no
// package manager owns is replaced.
package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases come from.
const Repo = "Tarektouati/made"

// DefaultBaseURL is the GitHub host releases are fetched from.
const DefaultBaseURL = "https://github.com"

// ChecksumsName is the release asset listing the SHA-256 of every archive.
const ChecksumsName = "checksums.txt"

// defaultTimeout bounds a whole request, body included, when Client.HTTP is nil.
const defaultTimeout = 5 * time.Minute

// Client fetches release information and assets. The zero value talks to github.com
// through the proxy named in the environment, like curl.
type Client struct {
	// BaseURL replaces DefaultBaseURL, for tests.
	BaseURL string
	// HTTP is the client to use; nil means one with a 5 minute timeout. Its
	// CheckRedirect is replaced.
	HTTP *http.Client
}

func (c Client) base() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

// httpClient returns a copy of c.HTTP that refuses redirects from https to http, so
// a release fetched over TLS is never downgraded to plain text.
func (c Client) httpClient() *http.Client {
	hc := http.Client{Timeout: defaultTimeout}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing redirect from https to %s", req.URL.Redacted())
		}
		return nil
	}
	return &hc
}

// get fetches url, failing on an HTTP error status (like curl -f) or a body larger
// than limit bytes.
func (c Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, errors.New(resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return body, nil
}

// LatestTag resolves the latest release tag in one request, without following the
// redirect of <base>/Tarektouati/made/releases/latest: it takes the last path
// segment of the redirect's Location, normalized by NormalizeTag. install.sh
// follows that redirect and takes the same segment from the final URL, so both
// resolve the same tag. It needs no API token and is not rate-limited like the
// GitHub API.
func (c Client) LatestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	hc := c.httpClient()
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 || resp.StatusCode == http.StatusNotModified {
		return "", fmt.Errorf("%s: %s, want a redirect to the latest release", req.URL.Redacted(), resp.Status)
	}
	loc, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("%s: %s: %w", req.URL.Redacted(), resp.Status, err)
	}
	if req.URL.Scheme == "https" && loc.Scheme != "https" {
		return "", fmt.Errorf("refusing redirect from https to %s", loc.Redacted())
	}
	p := loc.EscapedPath()
	tag := p[strings.LastIndex(p, "/")+1:]
	if tag == "" || loc.RawQuery != "" || loc.Fragment != "" {
		return "", fmt.Errorf("%s: no release tag in the redirect to %s", req.URL.Redacted(), loc.Redacted())
	}
	return NormalizeTag(tag)
}

// NormalizeTag applies install.sh's rules to a release tag: a leading "V" becomes
// "v", a missing "v" is added, a digit must follow it, and only ASCII letters,
// digits, ".", "+" and "-" may come after. The caller handles "" and "latest",
// which install.sh treats as "resolve the latest release".
func NormalizeTag(s string) (string, error) {
	tag := s
	switch {
	case strings.HasPrefix(tag, "v"):
	case strings.HasPrefix(tag, "V"):
		tag = "v" + tag[1:]
	default:
		tag = "v" + tag
	}
	if len(tag) < 2 || tag[1] < '0' || tag[1] > '9' {
		return "", fmt.Errorf("could not determine a release tag (got %q)", s)
	}
	for _, r := range tag[1:] {
		if !isAlnum(r) && r != '.' && r != '+' && r != '-' {
			return "", fmt.Errorf("invalid release tag %q", s)
		}
	}
	return tag, nil
}

func isAlnum(r rune) bool {
	return ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
}

// parseRelease splits a release version, vMAJOR.MINOR.PATCH optionally followed by
// build metadata ("+..."), into its numbers. Pre-releases (and so Go pseudo-versions), "dev",
// "(devel)" and numbers with leading zeros are not releases.
func parseRelease(v string) (nums [3]uint64, ok bool) {
	rest, found := strings.CutPrefix(v, "v")
	if !found {
		return nums, false
	}
	rest, _, _ = strings.Cut(rest, "+")
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return nums, false
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') || strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return nums, false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nums, false
		}
		nums[i] = n
	}
	return nums, true
}

// IsRelease reports whether v is a release version: vMAJOR.MINOR.PATCH, optionally
// with build metadata, and no pre-release. Pseudo-versions such as
// v0.6.1-0.20260101000000-abcdef123456, "(devel)" and "dev" are not.
func IsRelease(v string) bool {
	_, ok := parseRelease(v)
	return ok
}

// Compare returns -1, 0 or +1 as a is older than, the same as, or newer than b,
// ignoring build metadata. Versions that are not releases (see IsRelease) sort before
// every release and equal to each other.
func Compare(a, b string) int {
	na, oka := parseRelease(a)
	nb, okb := parseRelease(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := range na {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Newer reports whether latest is a newer release than current. It is false when
// either is not a release, so pre-releases and development builds never count.
func Newer(current, latest string) bool {
	return IsRelease(current) && IsRelease(latest) && Compare(latest, current) > 0
}

// Method is how the running binary was installed, which decides whether made
// self-update may replace it.
type Method int

const (
	// Manual is install.sh or a copied release binary: made self-update replaces it.
	Manual Method = iota
	// Homebrew is a binary in a Homebrew Cellar or Caskroom.
	Homebrew
	// GoInstall is a binary in GOBIN or GOPATH/bin, or a release built from the module
	// without release ldflags.
	GoInstall
	// Dev is a development build: "dev", "(devel)", a pseudo-version or a pre-release.
	Dev
	// NoArchive is a platform with no release archive, such as Windows.
	NoArchive
)

func (m Method) String() string {
	switch m {
	case Manual:
		return "manual"
	case Homebrew:
		return "homebrew"
	case GoInstall:
		return "go-install"
	case Dev:
		return "dev"
	case NoArchive:
		return "no-archive"
	}
	return "Method(" + strconv.Itoa(int(m)) + ")"
}

// Install describes the running binary for Detect.
type Install struct {
	// Exe is the binary's path, with symlinks resolved.
	Exe string
	// Version is the version made reports (made --version, without "made ").
	Version string
	// Stamped reports that the version was set at link time (-X main.version), as
	// release builds do; go install leaves it to the module build info.
	Stamped bool
	// GOOS and GOARCH are the platform the binary runs on.
	GOOS, GOARCH string
	// GOBIN and GOPATH are as go install would use them; GOPATH may list several
	// directories.
	GOBIN, GOPATH string
}

// Detect returns how in was installed. In order: a Cellar or Caskroom directory in
// the path is Homebrew; a binary directly in GOBIN or a GOPATH bin directory is go
// install; a version that is not a release is a development build; an unstamped
// release is go install (built from the module, for example with GOBIN elsewhere);
// a platform without release archives is NoArchive; anything else is Manual.
func Detect(in Install) Method {
	exe := filepath.Clean(in.Exe)
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Dir(exe)), "/") {
		if seg == "Cellar" || seg == "Caskroom" {
			return Homebrew
		}
	}
	dir := filepath.Dir(exe)
	if in.GOBIN != "" && filepath.Clean(in.GOBIN) == dir {
		return GoInstall
	}
	for _, p := range filepath.SplitList(in.GOPATH) {
		if p != "" && filepath.Join(p, "bin") == dir {
			return GoInstall
		}
	}
	if !IsRelease(in.Version) {
		return Dev
	}
	if !in.Stamped {
		return GoInstall
	}
	if _, ok := AssetName(in.Version, in.GOOS, in.GOARCH); !ok {
		return NoArchive
	}
	return Manual
}

// Current describes the running binary: its path with symlinks resolved, the
// platform, and GOBIN and GOPATH from the environment (GOPATH defaults to ~/go, as
// in the go command; settings made with go env -w are not read). version and stamped
// are as for Install.
func Current(version string, stamped bool) Install {
	in := Install{Version: version, Stamped: stamped, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if exe, err := os.Executable(); err == nil {
		in.Exe = resolve(exe)
	}
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		in.GOBIN = resolve(gobin)
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	var paths []string
	for _, p := range filepath.SplitList(gopath) {
		if p != "" {
			paths = append(paths, resolve(p))
		}
	}
	in.GOPATH = strings.Join(paths, string(filepath.ListSeparator))
	return in
}

// resolve returns p with symlinks resolved, or p itself if that fails.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

const (
	brewUpgrade = "brew upgrade Tarektouati/tap/made"
	goInstall   = "go install github.com/" + Repo + "@"
	// installScript reinstalls the latest release with install.sh.
	installScript = "curl -fsSL https://raw.githubusercontent.com/" + Repo + "/main/install.sh | sh"
)

// Hint returns the command that upgrades an install of method m. latest, the newest
// release, pins the go install command for a development build, which is likely
// newer than @latest would suggest; it may be empty.
func Hint(m Method, latest string) string {
	switch m {
	case Homebrew:
		return brewUpgrade
	case GoInstall, NoArchive:
		return goInstall + "latest"
	case Dev:
		if latest == "" {
			latest = "latest"
		}
		return goInstall + latest
	}
	return "made self-update"
}

// Refusal is made self-update's message for an install it does not replace (any
// method but Manual), naming the command to use instead.
func Refusal(m Method, in Install) string {
	switch m {
	case Homebrew:
		return "made was installed with Homebrew; update it with: " + Hint(m, "")
	case GoInstall:
		return "made was installed with go install; update it with: " + Hint(m, "")
	case Dev:
		return fmt.Sprintf("this is a development build (%s), which made self-update does not replace; install a release with: %s", in.Version, Hint(m, ""))
	case NoArchive:
		return fmt.Sprintf("no release archive is published for %s/%s; update made with: %s", in.GOOS, in.GOARCH, Hint(m, ""))
	}
	return ""
}

// platforms are the GOOS/GOARCH pairs with a release archive (.goreleaser.yaml
// builds, install.sh installs).
var platforms = map[string]bool{
	"darwin/amd64": true,
	"darwin/arm64": true,
	"linux/amd64":  true,
	"linux/arm64":  true,
}

// AssetName returns the name of the release archive of tag for goos/goarch,
// made_<tag without "v">_<goos>_<goarch>.tar.gz, and false when no archive is
// published for that platform.
func AssetName(tag, goos, goarch string) (string, bool) {
	if !platforms[goos+"/"+goarch] {
		return "", false
	}
	return "made_" + strings.TrimPrefix(tag, "v") + "_" + goos + "_" + goarch + ".tar.gz", true
}

// ReleaseURL returns the download URL of the asset name in release tag.
func ReleaseURL(base, tag, name string) string {
	return strings.TrimRight(base, "/") + "/" + Repo + "/releases/download/" + tag + "/" + name
}
