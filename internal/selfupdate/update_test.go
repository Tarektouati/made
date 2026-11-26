//go:build !windows

package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The fake release binaries are shell scripts, so the tests run on any unix host
// whatever the asset's GOOS/GOARCH says.
const (
	testOS   = "linux"
	testArch = "amd64"
)

func versionScript(v string) string { return "#!/bin/sh\necho made " + v + "\n" }

// fakeRelease is one release served by fakeGitHub.
type fakeRelease struct {
	bin []byte
	// raw replaces the whole archive (checksums.txt still matches it).
	raw []byte
	// sums replaces checksums.txt when set.
	sums *string
	// failArchive and failSums make the downloads fail with 404.
	failArchive, failSums bool
	// blockArchive makes the archive download hang until the request is cancelled;
	// started is closed when it begins.
	blockArchive bool
	started      chan struct{}
}

// fakeGitHub serves the URLs made self-update requests: the releases/latest redirect
// (to latest, or 404 when latest is "") and release assets. Any other request, such
// as for the tag page the redirect points to, is an error.
type fakeGitHub struct {
	t        *testing.T
	srv      *httptest.Server
	latest   string
	releases map[string]*fakeRelease

	mu        sync.Mutex
	downloads []string
}

func newFakeGitHub(t *testing.T, latest string, releases map[string]*fakeRelease) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{t: t, latest: latest, releases: releases}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	const prefix = "/" + Repo + "/releases/"
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	switch {
	case ok && rest == "latest":
		if g.latest == "" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, prefix+"tag/"+g.latest, http.StatusFound)
		return
	case ok && strings.HasPrefix(rest, "download/"):
		g.mu.Lock()
		g.downloads = append(g.downloads, r.URL.Path)
		g.mu.Unlock()
		tag, name, _ := strings.Cut(strings.TrimPrefix(rest, "download/"), "/")
		rel := g.releases[tag]
		asset, _ := AssetName(tag, testOS, testArch)
		switch {
		case rel == nil:
			http.NotFound(w, r)
		case name == ChecksumsName && rel.failSums, name == asset && rel.failArchive:
			http.NotFound(w, r)
		case name == ChecksumsName:
			sums := checksumsFor(tag, rel.archive(g.t))
			if rel.sums != nil {
				sums = *rel.sums
			}
			_, _ = w.Write([]byte(sums))
		case name == asset && rel.blockArchive:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0x1f})
			w.(http.Flusher).Flush()
			close(rel.started)
			<-r.Context().Done()
		case name == asset:
			_, _ = w.Write(rel.archive(g.t))
		default:
			g.t.Errorf("unexpected asset requested: %s", r.URL.Path)
			http.NotFound(w, r)
		}
		return
	}
	g.t.Errorf("unexpected request: %s", r.URL.Path)
	http.NotFound(w, r)
}

func (g *fakeGitHub) downloadCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.downloads)
}

// archive returns the release tarball: README.md, made and LICENSE, or raw.
func (r *fakeRelease) archive(t *testing.T) []byte {
	t.Helper()
	if r.raw != nil {
		return r.raw
	}
	return tarGz(t,
		tarEntry{hdr: tar.Header{Name: "README.md", Mode: 0o644}, body: []byte("# made\n")},
		tarEntry{hdr: tar.Header{Name: "made", Mode: 0o755}, body: r.bin},
		tarEntry{hdr: tar.Header{Name: "LICENSE", Mode: 0o644}, body: []byte("MIT\n")},
	)
}

// tarEntry is one file of a test archive. A regular file gets its Typeflag and Size
// from body.
type tarEntry struct {
	hdr  tar.Header
	body []byte
}

// tarGz returns a .tar.gz archive holding entries.
func tarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checksumsFor is checksums.txt as goreleaser writes it, with entries for other
// platforms around the one under test.
func checksumsFor(tag string, archive []byte) string {
	sum := sha256.Sum256(archive)
	v := strings.TrimPrefix(tag, "v")
	return fmt.Sprintf("%s  made_%s_darwin_arm64.tar.gz\n%s  made_%s_%s_%s.tar.gz\n%s  made_%s_linux_arm64.tar.gz\n",
		strings.Repeat("0", 64), v, hex.EncodeToString(sum[:]), v, testOS, testArch, strings.Repeat("1", 64), v)
}

// release returns a release whose binary reports version tag.
func release(tag string) *fakeRelease {
	return &fakeRelease{bin: []byte(versionScript(tag))}
}

// installed writes the made binary of version v into a new directory and returns
// its path.
func installed(t *testing.T, v string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "made")
	// #nosec G306 -- Executable fixture inside a private temporary directory.
	if err := os.WriteFile(exe, []byte(versionScript(v)), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

type updateResult struct {
	err    error
	stdout string
}

func runUpdate(t *testing.T, g *fakeGitHub, exe, current, target string, dryRun bool) updateResult {
	t.Helper()
	return runUpdateCtx(context.Background(), t, g, exe, current, target, dryRun)
}

func runUpdateCtx(ctx context.Context, t *testing.T, g *fakeGitHub, exe, current, target string, dryRun bool) updateResult {
	t.Helper()
	var stdout bytes.Buffer
	u := Updater{
		Client:  Client{BaseURL: g.srv.URL},
		Exe:     exe,
		Current: current,
		Target:  target,
		GOOS:    testOS,
		GOARCH:  testArch,
		DryRun:  dryRun,
		Stdout:  &stdout,
	}
	err := u.Run(ctx)
	return updateResult{err: err, stdout: stdout.String()}
}

// exitCode returns 0 for a nil error and the code of an *Error.
func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ue *Error
	if !errors.As(err, &ue) {
		t.Fatalf("Run returned %T (%v), want *Error", err, err)
	}
	return ue.Code
}

// assertBinary checks exe still holds the made binary of version v and that no
// temporary file was left next to it.
func assertBinary(t *testing.T, exe, v string) {
	t.Helper()
	// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != versionScript(v) {
		t.Errorf("%s holds %q, want the %s binary", exe, b, v)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("%s mode %v is not executable", exe, info.Mode())
	}
	assertNoLeftovers(t, filepath.Dir(exe))
}

func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".made-update-") {
			t.Errorf("temporary file %s left in %s", e.Name(), dir)
		}
	}
}

func TestUpdateHappyPath(t *testing.T) {
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.6.0")
	res := runUpdate(t, g, exe, "v0.6.0", "", false)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v (code %d), want success", res.err, code)
	}
	if want := "made: updated v0.6.0 -> v0.7.0\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	assertBinary(t, exe, "v0.7.0")
}

// TestUpdateRunsNewBinaryNextToOld checks the new binary is written, and run, in the
// directory of the one it replaces, so the final rename stays on one filesystem.
func TestUpdateRunsNewBinaryNextToOld(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "dir")
	bin := "#!/bin/sh\ndirname \"$0\" >'" + marker + "'\necho made v0.7.0\n"
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": {bin: []byte(bin)}})
	exe := installed(t, "v0.6.0")
	if res := runUpdate(t, g, exe, "v0.6.0", "", false); res.err != nil {
		t.Fatalf("Run = %v, want success", res.err)
	}
	// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("the new binary ran from %s, want %s", strings.TrimSpace(string(got)), want)
	}
}

func TestUpdateAlreadyLatest(t *testing.T) {
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.7.0")
	res := runUpdate(t, g, exe, "v0.7.0", "", false)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v, want success", res.err)
	}
	if want := "made: v0.7.0 is the latest version\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	if n := g.downloadCount(); n != 0 {
		t.Errorf("%d downloads, want none", n)
	}
	assertBinary(t, exe, "v0.7.0")
}

func TestUpdateRefusesDowngrade(t *testing.T) {
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.8.0")
	res := runUpdate(t, g, exe, "v0.8.0", "", false)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v, want success", res.err)
	}
	for _, want := range []string{"v0.8.0 is newer than the latest release v0.7.0", "made self-update v0.7.0"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", res.stdout, want)
		}
	}
	if n := g.downloadCount(); n != 0 {
		t.Errorf("%d downloads, want none", n)
	}
	assertBinary(t, exe, "v0.8.0")
}

func TestUpdateExplicitVersion(t *testing.T) {
	g := newFakeGitHub(t, "v0.8.0", map[string]*fakeRelease{"v0.6.0": release("v0.6.0"), "v0.8.0": release("v0.8.0")})
	exe := installed(t, "v0.7.0")
	res := runUpdate(t, g, exe, "v0.7.0", "0.6.0", false)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v, want success", res.err)
	}
	if want := "made: updated v0.7.0 -> v0.6.0\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateExplicitSameVersion(t *testing.T) {
	g := newFakeGitHub(t, "v0.8.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.7.0")
	res := runUpdate(t, g, exe, "v0.7.0", "v0.7.0", false)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v, want success", res.err)
	}
	if want := "made: v0.7.0 is already installed\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	if n := g.downloadCount(); n != 0 {
		t.Errorf("%d downloads, want none", n)
	}
}

// TestUpdateRefusesNamedPreRelease checks a named target must be a release: an rc
// would leave a binary Detect sees as a dev build, which self-update then refuses
// to replace.
func TestUpdateRefusesNamedPreRelease(t *testing.T) {
	for _, target := range []string{"v0.7.0-rc.1", "v0.7.0-0.20260101000000-abcdef123456", "v1"} {
		t.Run(target, func(t *testing.T) {
			g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{target: release(target)})
			exe := installed(t, "v0.6.0")
			res := runUpdate(t, g, exe, "v0.6.0", target, false)
			if code := exitCode(t, res.err); code != ExitFailure {
				t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitFailure)
			}
			if !strings.Contains(res.err.Error(), "not a release") {
				t.Errorf("error = %q, want it to say %s is not a release", res.err, target)
			}
			if n := g.downloadCount(); n != 0 {
				t.Errorf("%d downloads, want none", n)
			}
			assertBinary(t, exe, "v0.6.0")
		})
	}
}

func TestUpdateFailuresReplaceNothing(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, c := range []struct {
		name    string
		latest  string
		rel     *fakeRelease
		wantErr string
	}{
		{name: "checksum mismatch", latest: "v0.7.0", rel: &fakeRelease{bin: []byte(versionScript("v0.7.0")),
			sums: str(strings.Repeat("ab", 32) + "  made_0.7.0_linux_amd64.tar.gz\n")}, wantErr: "checksum mismatch for made_0.7.0_linux_amd64.tar.gz"},
		{name: "missing entry", latest: "v0.7.0", rel: &fakeRelease{bin: []byte(versionScript("v0.7.0")),
			sums: str(strings.Repeat("ab", 32) + "  made_0.7.0_linux_arm64.tar.gz\n")}, wantErr: "no checksum found for made_0.7.0_linux_amd64.tar.gz"},
		{name: "archive download fails", latest: "v0.7.0", rel: &fakeRelease{bin: []byte(versionScript("v0.7.0")), failArchive: true}, wantErr: "download failed"},
		{name: "checksums download fails", latest: "v0.7.0", rel: &fakeRelease{bin: []byte(versionScript("v0.7.0")), failSums: true}, wantErr: "download failed"},
		{name: "latest unresolved", latest: "", rel: release("v0.7.0"), wantErr: "cannot resolve the latest release"},
		{name: "binary does not run", latest: "v0.7.0", rel: &fakeRelease{bin: []byte("#!/bin/sh\necho boom >&2\nexit 3\n")}, wantErr: "boom"},
		{name: "binary is not executable code", latest: "v0.7.0", rel: &fakeRelease{bin: []byte("\x7fELF garbage")}, wantErr: "failed to run"},
		{name: "binary reports another version", latest: "v0.7.0", rel: release("v9.9.9"), wantErr: "made v9.9.9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newFakeGitHub(t, c.latest, map[string]*fakeRelease{"v0.7.0": c.rel})
			exe := installed(t, "v0.6.0")
			res := runUpdate(t, g, exe, "v0.6.0", "", false)
			if code := exitCode(t, res.err); code != ExitFailure {
				t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitFailure)
			}
			if !strings.Contains(res.err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to contain %q", res.err, c.wantErr)
			}
			if res.stdout != "" {
				t.Errorf("stdout = %q, want nothing", res.stdout)
			}
			assertBinary(t, exe, "v0.6.0")
		})
	}
}

// TestUpdateBadArchive covers the checks on the archive's contents. The checksum
// matches each archive, so only extraction can reject it.
func TestUpdateBadArchive(t *testing.T) {
	newBin := []byte(versionScript("v0.7.0"))
	readme := tarEntry{hdr: tar.Header{Name: "README.md", Mode: 0o644}, body: []byte("# made\n")}
	for _, c := range []struct {
		name    string
		archive func(t *testing.T) []byte
		// limit, when set, lowers maxBinaryBytes for the case.
		limit   int64
		wantErr string
	}{
		{name: "no made entry", archive: func(t *testing.T) []byte {
			return tarGz(t, readme, tarEntry{hdr: tar.Header{Name: "bin/made", Mode: 0o755}, body: newBin})
		}, wantErr: "the archive has no made binary"},
		{name: "made is a symlink", archive: func(t *testing.T) []byte {
			return tarGz(t, readme, tarEntry{hdr: tar.Header{Name: "made", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh", Mode: 0o777}})
		}, wantErr: "made in the archive is not a regular file"},
		{name: "made is a hardlink", archive: func(t *testing.T) []byte {
			return tarGz(t, readme, tarEntry{hdr: tar.Header{Name: "made", Typeflag: tar.TypeLink, Linkname: "README.md", Mode: 0o755}})
		}, wantErr: "made in the archive is not a regular file"},
		{name: "made is a directory", archive: func(t *testing.T) []byte {
			return tarGz(t, readme, tarEntry{hdr: tar.Header{Name: "made", Typeflag: tar.TypeDir, Mode: 0o755}})
		}, wantErr: "made in the archive is not a regular file"},
		{name: "made is too large", archive: func(t *testing.T) []byte {
			return tarGz(t, readme, tarEntry{hdr: tar.Header{Name: "made", Mode: 0o755}, body: newBin})
		}, limit: int64(len(newBin)) - 1, wantErr: "made in the archive is larger than"},
		{name: "not gzip", archive: func(*testing.T) []byte {
			return []byte("this is not a gzip stream")
		}, wantErr: "gzip"},
		{name: "truncated tar", archive: func(t *testing.T) []byte {
			full := tarGz(t, readme, tarEntry{hdr: tar.Header{Name: "made", Mode: 0o755}, body: bytes.Repeat([]byte("x"), 4096)})
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			r, err := gzip.NewReader(bytes.NewReader(full))
			if err != nil {
				t.Fatal(err)
			}
			plain, _ := io.ReadAll(r)
			_, _ = gz.Write(plain[:1024+512+100])
			_ = gz.Close()
			return buf.Bytes()
		}, wantErr: "cannot extract made"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.limit > 0 {
				old := maxBinaryBytes
				maxBinaryBytes = c.limit
				t.Cleanup(func() { maxBinaryBytes = old })
			}
			g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": {raw: c.archive(t)}})
			exe := installed(t, "v0.6.0")
			res := runUpdate(t, g, exe, "v0.6.0", "", false)
			if code := exitCode(t, res.err); code != ExitFailure {
				t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitFailure)
			}
			if !strings.Contains(res.err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to contain %q", res.err, c.wantErr)
			}
			assertBinary(t, exe, "v0.6.0")
		})
	}
}

// TestUpdateArchiveDotSlashName checks the binary is also found as ./made, the
// spelling tar uses for an archive made with "tar -C dir .".
func TestUpdateArchiveDotSlashName(t *testing.T) {
	raw := tarGz(t, tarEntry{hdr: tar.Header{Name: "./made", Mode: 0o755}, body: []byte(versionScript("v0.7.0"))})
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": {raw: raw}})
	exe := installed(t, "v0.6.0")
	if res := runUpdate(t, g, exe, "v0.6.0", "", false); res.err != nil {
		t.Fatalf("Run = %v, want success", res.err)
	}
	assertBinary(t, exe, "v0.7.0")
}

// TestUpdateVersionCheckDoesNotHang checks a new binary that exits but leaves a
// background process holding its output open cannot stall the update: the check
// gives up and nothing is replaced.
func TestUpdateVersionCheckDoesNotHang(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	bin := "#!/bin/sh\nsleep 20 &\necho $! >'" + pidFile + "'\necho made v0.7.0\n"
	t.Cleanup(func() {
		// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	})
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": {bin: []byte(bin)}})
	exe := installed(t, "v0.6.0")
	start := time.Now()
	res := runUpdate(t, g, exe, "v0.6.0", "", false)
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("the update took %v, want the version check to give up within a few seconds", elapsed)
	}
	if code := exitCode(t, res.err); code != ExitFailure {
		t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitFailure)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateMissingEntrySkipsArchive(t *testing.T) {
	sums := strings.Repeat("ab", 32) + "  made_0.7.0_linux_arm64.tar.gz\n"
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": {bin: []byte(versionScript("v0.7.0")), sums: &sums}})
	exe := installed(t, "v0.6.0")
	_ = runUpdate(t, g, exe, "v0.6.0", "", false)
	if n := g.downloadCount(); n != 1 {
		t.Errorf("%d downloads (%v), want only checksums.txt", n, g.downloads)
	}
}

func TestUpdateUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to any directory")
	}
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.6.0")
	dir := filepath.Dir(exe)
	// #nosec G302 -- The read-only directory is required to test an unwritable installation.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- Restore directory search/write access for temporary-file cleanup.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	res := runUpdate(t, g, exe, "v0.6.0", "", false)
	if code := exitCode(t, res.err); code != ExitNotWritable {
		t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitNotWritable)
	}
	var ue *Error
	errors.As(res.err, &ue)
	if !strings.Contains(ue.Error(), dir) {
		t.Errorf("error = %q, want it to name %s", ue, dir)
	}
	for _, want := range []string{"sudo made self-update", "install.sh"} {
		if !strings.Contains(ue.Hint, want) {
			t.Errorf("hint = %q, want it to mention %q", ue.Hint, want)
		}
	}
	if n := g.downloadCount(); n != 0 {
		t.Errorf("%d downloads (%v), want none: an unwritable directory is reported before downloading", n, g.downloads)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateFollowsSymlink(t *testing.T) {
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	dest := installed(t, "v0.6.0")
	link := filepath.Join(t.TempDir(), "made")
	if err := os.Symlink(dest, link); err != nil {
		t.Fatal(err)
	}
	res := runUpdate(t, g, link, "v0.6.0", "", false)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v, want success", res.err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is no longer a symlink (%v, %v)", link, info, err)
	}
	assertBinary(t, dest, "v0.7.0")
}

func TestUpdateDryRun(t *testing.T) {
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.6.0")
	res := runUpdate(t, g, exe, "v0.6.0", "", true)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v, want success", res.err)
	}
	for _, want := range []string{
		"made: would update v0.6.0 -> v0.7.0\n",
		g.srv.URL + "/Tarektouati/made/releases/download/v0.7.0/made_0.7.0_linux_amd64.tar.gz",
		exe,
		"writable",
	} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", res.stdout, want)
		}
	}
	if n := g.downloadCount(); n != 0 {
		t.Errorf("%d downloads, want none", n)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateDryRunUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to any directory")
	}
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.6.0")
	dir := filepath.Dir(exe)
	// #nosec G302 -- The read-only directory is required to test an unwritable installation.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- Restore directory search/write access for temporary-file cleanup.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	res := runUpdate(t, g, exe, "v0.6.0", "", true)
	if code := exitCode(t, res.err); code != 0 {
		t.Fatalf("Run = %v, want success", res.err)
	}
	if !strings.Contains(res.stdout, "not writable") {
		t.Errorf("stdout = %q, want it to say the directory is not writable", res.stdout)
	}
}

func TestUpdateCancelled(t *testing.T) {
	rel := release("v0.7.0")
	rel.blockArchive = true
	rel.started = make(chan struct{})
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": rel})
	exe := installed(t, "v0.6.0")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-rel.started
		cancel()
	}()
	res := runUpdateCtx(ctx, t, g, exe, "v0.6.0", "", false)
	if code := exitCode(t, res.err); code != ExitFailure || !errors.Is(res.err, context.Canceled) {
		t.Fatalf("Run = %v (code %d), want a cancelled download, exit %d", res.err, code, ExitFailure)
	}
	assertBinary(t, exe, "v0.6.0")
}
