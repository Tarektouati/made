//go:build !windows

package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fake-server update tests exercise verification ordering and error handling. The
// verifier is in-process; these fixtures are not real Sigstore signatures.
const testSignature = "test-only signature fixture\n"

func TestCosignVerifierRefusesRelativePath(t *testing.T) {
	for _, path := range []string{".", "bin"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if path != "." {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			// A successful verifier fixture would create this marker if executed.
			program := "#!/bin/sh\nprintf invoked > invoked\nprintf '{\"gitVersion\":\"v3.1.6\"}'\n"
			// #nosec G304 G306 -- A private executable fixture must never be invoked by the verifier.
			if err := os.WriteFile(filepath.Join(path, "cosign"), []byte(program), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", path)
			// Our policy must still hold when Go's optional ErrDot check is disabled.
			t.Setenv("GODEBUG", os.Getenv("GODEBUG")+",execerrdot=0")
			err := (cosignVerifier{}).Verify(t.Context(), "v1.2.3", nil, nil)
			if err == nil || !strings.Contains(err.Error(), "absolute") {
				t.Errorf("Verify = %v, want relative verifier rejection", err)
			}
			if _, err := os.Stat("invoked"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("relative Cosign was executed: marker stat = %v", err)
			}
		})
	}
}

// A release token can replace both an archive and its matching checksums. A
// checksum by itself must no longer authorize running that replacement.
func TestUpdateRejectsUnsignedRelease(t *testing.T) {
	rel := release("v0.7.0")
	rel.failBundle = true
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": rel})
	exe := installed(t, "v0.6.0")
	var stdout bytes.Buffer
	u := Updater{
		Client: Client{BaseURL: g.srv.URL},
		Exe:    exe, Current: "v0.6.0", Target: "v0.7.0",
		GOOS: testOS, GOARCH: testArch, Stdout: &stdout,
	}
	err := u.Run(context.Background())
	if err == nil {
		t.Error("an unsigned archive with matching checksums was installed")
	} else {
		var ue *Error
		if !errors.As(err, &ue) || !strings.Contains(ue.Hint, "unsigned releases cannot be installed") {
			t.Errorf("error = %v, want an explanation that unsigned releases are refused", err)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no success message", &stdout)
	}
	assertBinary(t, exe, "v0.6.0")
	if n := g.downloadCount(); n != 2 {
		t.Errorf("%d downloads, want only checksums and signature", n)
	}
}

func TestUpdateUsesCosignByDefault(t *testing.T) {
	// No executable is installed: a default verifier must fail rather than trust
	// the mere presence of a bundle supplied by the release server.
	t.Setenv("PATH", t.TempDir())
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
	exe := installed(t, "v0.6.0")
	var stdout bytes.Buffer
	u := Updater{
		Client: Client{BaseURL: g.srv.URL},
		Exe:    exe, Current: "v0.6.0", Target: "v0.7.0",
		GOOS: testOS, GOARCH: testArch, Stdout: &stdout,
	}
	if err := u.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "cosign v3.1.6 or later is required") {
		t.Errorf("Run = %v, want the required Cosign check", err)
	}
	if n := g.downloadCount(); n != 2 {
		t.Errorf("%d downloads, want no archive download", n)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateRejectsTamperedChecksumsBeforeRunningBinary(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	rel := release("v0.7.0")
	rel.bin = []byte("#!/bin/sh\ntouch '" + marker + "'\necho made v0.7.0\n")
	g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": rel})
	exe := installed(t, "v0.6.0")
	var stdout bytes.Buffer
	badSignature := errors.New("checksum signature does not match")
	calls := 0
	u := Updater{
		Client: Client{BaseURL: g.srv.URL},
		Verifier: verifierFunc(func(_ context.Context, tag string, sums, bundle []byte) error {
			calls++
			if tag != "v0.7.0" || string(sums) != checksumsFor(tag, rel.archive(t)) || string(bundle) != testSignature {
				t.Errorf("verifier received tag %q, checksums %q, bundle %q", tag, sums, bundle)
			}
			if n := g.downloadCount(); n != 2 {
				t.Errorf("%d downloads at verification, want no archive download yet", n)
			}
			return badSignature
		}),
		Exe: exe, Current: "v0.6.0", Target: "v0.7.0",
		GOOS: testOS, GOARCH: testArch, Stdout: &stdout,
	}
	if err := u.Run(t.Context()); !errors.Is(err, badSignature) {
		t.Errorf("Run = %v, want the signature error", err)
	}
	if calls != 1 || g.downloadCount() != 2 {
		t.Errorf("%d verifications, %d downloads, want 1 and 2", calls, g.downloadCount())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unverified binary ran: marker stat = %v", err)
	}
	assertBinary(t, exe, "v0.6.0")
}

func TestUpdateSignatureFailures(t *testing.T) {
	for _, c := range []struct {
		name, bundle, want string
	}{
		{name: "empty", want: "invalid test signature"},
		{name: "malformed", bundle: "not a signature", want: "invalid test signature"},
		{name: "oversized", bundle: strings.Repeat("x", maxSignatureBytes+1), want: "larger than"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rel := release("v0.7.0")
			rel.bundle = &c.bundle
			g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": rel})
			exe := installed(t, "v0.6.0")
			res := runUpdate(t, g, exe, "v0.6.0", "v0.7.0", false)
			if res.err == nil || !strings.Contains(res.err.Error(), c.want) {
				t.Errorf("Run = %v, want %s", res.err, c.want)
			}
			if n := g.downloadCount(); n != 2 {
				t.Errorf("%d downloads, want no archive download", n)
			}
			assertBinary(t, exe, "v0.6.0")
		})
	}
}

func TestUpdateNoOpDoesNotVerify(t *testing.T) {
	for _, c := range []struct {
		name, target string
		dryRun       bool
	}{
		{name: "already installed", target: "v0.6.0"},
		{name: "dry run", target: "v0.7.0", dryRun: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
			exe := installed(t, "v0.6.0")
			var stdout bytes.Buffer
			u := Updater{
				Client: Client{BaseURL: g.srv.URL},
				Verifier: verifierFunc(func(context.Context, string, []byte, []byte) error {
					t.Error("no-op update invoked the verifier")
					return errors.New("verifier must not run")
				}),
				Exe: exe, Current: "v0.6.0", Target: c.target, DryRun: c.dryRun,
				GOOS: testOS, GOARCH: testArch, Stdout: &stdout,
			}
			if err := u.Run(t.Context()); err != nil {
				t.Errorf("Run = %v", err)
			}
			if n := g.downloadCount(); n != 0 {
				t.Errorf("%d downloads, want none", n)
			}
			assertBinary(t, exe, "v0.6.0")
		})
	}
}
