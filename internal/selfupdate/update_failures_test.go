//go:build !windows

package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateOversizedChecksums(t *testing.T) {
	sums := strings.Repeat("x", maxChecksumsBytes+1)
	rel := release("v0.7.0")
	rel.sums = &sums
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": rel})
	exe := installed(t, "v0.6.0")
	res := runUpdate(t, g, exe, "v0.6.0", "v0.7.0", false)
	if code := exitCode(t, res.err); code != ExitFailure {
		t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitFailure)
	}
	if want := fmt.Sprintf("larger than %d bytes", maxChecksumsBytes); !strings.Contains(res.err.Error(), want) {
		t.Errorf("error = %q, want %q", res.err, want)
	}
	if n := g.downloadCount(); n != 1 {
		t.Errorf("%d downloads, want only checksums.txt", n)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want nothing", res.stdout)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateInvalidTarHeader(t *testing.T) {
	// A valid gzip stream gets as far as tar.Next, whose header error must
	// survive the update's error wrapping.
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	if _, err := gz.Write(bytes.Repeat([]byte{'x'}, 512)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": {raw: archive.Bytes()}})
	exe := installed(t, "v0.6.0")
	res := runUpdate(t, g, exe, "v0.6.0", "v0.7.0", false)
	if code := exitCode(t, res.err); code != ExitFailure || !errors.Is(res.err, tar.ErrHeader) {
		t.Fatalf("Run = %v (code %d), want tar.ErrHeader and exit %d", res.err, code, ExitFailure)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want nothing", res.stdout)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateDirectoryDisappearsDuringDownload(t *testing.T) {
	exe := installed(t, "v0.6.0")
	dir := filepath.Dir(exe)
	moved := filepath.Join(t.TempDir(), "moved")
	archive := release("v0.7.0").archive(t)
	asset, _ := AssetName("v0.7.0", testOS, testArch)
	const base = "https://releases.invalid"
	calls := 0
	client := Client{BaseURL: base, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var body []byte
		switch r.URL.String() {
		case ReleaseURL(base, "v0.7.0", ChecksumsName):
			body = []byte(checksumsFor("v0.7.0", archive))
		case ReleaseURL(base, "v0.7.0", asset):
			// The initial directory check passed, but creating the temporary
			// binary must still report a filesystem error after the download.
			if err := os.Rename(dir, moved); err != nil {
				t.Errorf("move test directory: %v", err)
				return nil, err
			}
			body = archive
		default:
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}}
	var stdout bytes.Buffer
	u := Updater{Client: client, Exe: exe, Current: "v0.6.0", Target: "v0.7.0", GOOS: testOS, GOARCH: testArch, Stdout: &stdout}
	err := u.Run(context.Background())
	if code := exitCode(t, err); code != ExitFailure || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run = %v (code %d), want a missing directory error and exit %d", err, code, ExitFailure)
	}
	if !strings.Contains(err.Error(), "cannot write the new binary") {
		t.Errorf("error = %q, want a temporary binary creation failure", err)
	}
	if calls != 2 || stdout.Len() != 0 {
		t.Errorf("requests = %d, stdout = %q, want two downloads and no output", calls, stdout.String())
	}
	assertBinary(t, filepath.Join(moved, "made"), "v0.6.0")
}

func TestUpdateRenameFailure(t *testing.T) {
	// A directory cannot be replaced by the downloaded regular file. This
	// exercises the rename error and cleanup without depending on user IDs.
	dir := t.TempDir()
	exe := filepath.Join(dir, "made")
	if err := os.Mkdir(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(exe, "keep")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	res := runUpdate(t, g, exe, "v0.6.0", "v0.7.0", false)
	if code := exitCode(t, res.err); code != ExitFailure {
		t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitFailure)
	}
	if !strings.Contains(res.err.Error(), "cannot replace") || res.stdout != "" {
		t.Errorf("Run = (%q, %v), want no output and a rename failure", res.stdout, res.err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "unchanged" {
		t.Errorf("destination changed: (%q, %v)", got, err)
	}
	assertNoLeftovers(t, dir)
}

func TestUpdateVersionOutputFailures(t *testing.T) {
	for _, c := range []struct {
		name, script, wantErr string
		exit                  int
	}{
		{name: "silent failure", script: "exit 7", wantErr: "exit status 7", exit: 7},
		{name: "empty version", script: "printf '   \\n'", wantErr: `it reports "", want "made v0.7.0"`},
		{name: "extra output", script: "echo made v0.7.0; echo unexpected", wantErr: `it reports "made v0.7.0\nunexpected"`},
		{name: "long failure", script: "printf '" + strings.Repeat("x", 250) + "'; exit 3", wantErr: strings.Repeat("x", 200) + "...", exit: 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			rel := &fakeRelease{bin: []byte("#!/bin/sh\n" + c.script + "\n")}
			g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": rel})
			exe := installed(t, "v0.6.0")
			res := runUpdate(t, g, exe, "v0.6.0", "v0.7.0", false)
			if code := exitCode(t, res.err); code != ExitFailure {
				t.Fatalf("Run = %v (code %d), want exit %d", res.err, code, ExitFailure)
			}
			if !strings.Contains(res.err.Error(), c.wantErr) || strings.Contains(res.err.Error(), strings.Repeat("x", 201)) {
				t.Errorf("error = %q, want %q with output capped at 200 bytes", res.err, c.wantErr)
			}
			if c.exit != 0 {
				var exitErr *exec.ExitError
				if !errors.As(res.err, &exitErr) || exitErr.ExitCode() != c.exit {
					t.Errorf("error = %v, want the original process error with exit %d", res.err, c.exit)
				}
			}
			if res.stdout != "" {
				t.Errorf("stdout = %q, want nothing", res.stdout)
			}
			assertBinary(t, exe, "v0.6.0")
		})
	}
}

func TestCheckVersionCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkVersion(ctx, installed(t, "v0.7.0"), "v0.7.0"); !errors.Is(err, context.Canceled) {
		t.Errorf("checkVersion = %v, want context.Canceled", err)
	}
}

func TestUpdateInvalidInputs(t *testing.T) {
	for _, c := range []struct{ name, current, target, latest, goos, wantErr string }{
		{name: "development build", current: "dev", target: "v0.7.0", goos: testOS, wantErr: "cannot update from dev"},
		{name: "invalid target", current: "v0.6.0", target: "v0.7.0/other", goos: testOS, wantErr: "invalid release tag"},
		{name: "latest prerelease", current: "v0.6.0", latest: "v0.7.0-rc.1", goos: testOS, wantErr: "is not a release version"},
		{name: "unsupported platform", current: "v0.6.0", target: "v0.7.0", goos: "plan9", wantErr: "no release archive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newFakeGitHub(t, c.latest, nil)
			exe := installed(t, "v0.6.0")
			var stdout bytes.Buffer
			u := Updater{Client: Client{BaseURL: g.srv.URL}, Exe: exe, Current: c.current, Target: c.target, GOOS: c.goos, GOARCH: testArch, Stdout: &stdout}
			err := u.Run(context.Background())
			if code := exitCode(t, err); code != ExitFailure {
				t.Fatalf("Run = %v (code %d), want exit %d", err, code, ExitFailure)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want %q", err, c.wantErr)
			}
			if n := g.downloadCount(); n != 0 || stdout.Len() != 0 {
				t.Errorf("downloads = %d, stdout = %q, want no downloads or output", n, stdout.String())
			}
			assertBinary(t, exe, "v0.6.0")
		})
	}
}
