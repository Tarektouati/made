package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SignatureName is the Sigstore bundle authenticating the release's checksums.
const SignatureName = ChecksumsName + ".sigstore.json"

const (
	minimumCosignVersion = "v3.1.6"
	githubOIDCIssuer     = "https://token.actions.githubusercontent.com"
	releaseWorkflow      = DefaultBaseURL + "/" + Repo + "/.github/workflows/release.yml"
	maxSignatureBytes    = 1 << 20
	maxCosignOutput      = 16 << 10
	cosignTimeout        = 2 * time.Minute
)

// Verifier authenticates the exact checksums bytes for a release tag. Implementations
// must reject signatures from any other publisher, workflow or tag.
type Verifier interface {
	Verify(ctx context.Context, tag string, checksums, bundle []byte) error
}

// cosignVerifier uses the installed Cosign. Tests replace run with an in-process
// command runner; this is never selected through a CLI flag or environment setting.
type cosignVerifier struct {
	run func(context.Context, ...string) ([]byte, error)
}

func (v cosignVerifier) Verify(ctx context.Context, tag string, checksums, bundle []byte) error {
	run := v.run
	if run == nil {
		path, err := exec.LookPath("cosign")
		if err != nil {
			return fmt.Errorf("cosign %s or later is required: %w", minimumCosignVersion, err)
		}
		// LookPath may allow relative results when GODEBUG disables ErrDot.
		// Never execute a verifier selected from the current or a relative directory.
		if !filepath.IsAbs(path) {
			return fmt.Errorf("cosign must be found in an absolute PATH directory (got %q)", path)
		}
		// Use the same executable for the version check and signature verification.
		run = func(ctx context.Context, args ...string) ([]byte, error) {
			return cosignOutput(ctx, path, args...)
		}
	}
	versionCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	out, err := run(versionCtx, "version", "--json")
	cancel()
	if err != nil {
		return fmt.Errorf("cannot check cosign version: %w", err)
	}
	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if err = json.Unmarshal(out, &version); err != nil {
		return fmt.Errorf("cannot read cosign version: %w", err)
	}
	// Development and pre-release builds do not establish the security floor.
	if !IsRelease(version.GitVersion) || strings.Contains(version.GitVersion, "+") || Compare(version.GitVersion, minimumCosignVersion) < 0 {
		return fmt.Errorf("cosign %s or later is required (found %q)", minimumCosignVersion, version.GitVersion)
	}

	dir, err := os.MkdirTemp("", ".made-verify-*")
	if err != nil {
		return fmt.Errorf("cannot create signature verification directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sumsPath, bundlePath := filepath.Join(dir, ChecksumsName), filepath.Join(dir, SignatureName)
	// #nosec G304 -- Both filenames are fixed inside a newly created private directory.
	if err = os.WriteFile(sumsPath, checksums, 0o600); err != nil {
		return fmt.Errorf("cannot write checksums for verification: %w", err)
	}
	// #nosec G304 -- The bundle is verification input in the same private directory.
	if err = os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		return fmt.Errorf("cannot write signature for verification: %w", err)
	}
	verifyCtx, cancel := context.WithTimeout(ctx, cosignTimeout)
	defer cancel()
	_, err = run(verifyCtx, "verify-blob",
		"--certificate-identity", releaseWorkflow+"@refs/tags/"+tag,
		"--certificate-oidc-issuer", githubOIDCIssuer,
		"--bundle", bundlePath, sumsPath)
	return err
}

func signatureFailure(err error) *Error {
	return &Error{
		Code: ExitFailure,
		Err:  fmt.Errorf("release signature verification failed: %w", err),
		Hint: "self-update requires a signed release and cosign " + minimumCosignVersion + " or later (https://docs.sigstore.dev/cosign/system_config/installation/); unsigned releases cannot be installed",
	}
}

// cosignOutput bounds both the subprocess and its output. A verifier failure must
// never leave self-update waiting on inherited output pipes or consuming unbounded
// diagnostics from a malformed bundle.
func cosignOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	// #nosec G204 -- Run the resolved Cosign executable with fixed flags and no shell.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = versionWaitDelay
	var out cosignBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.bytes, ctx.Err()
	}
	if err != nil {
		detail := strings.TrimSpace(string(out.bytes))
		if len(detail) > 200 {
			detail = detail[:200] + "..."
		}
		return out.bytes, fmt.Errorf("%w: %s", err, detail)
	}
	if out.overflow {
		return out.bytes, fmt.Errorf("cosign output exceeds %d bytes", maxCosignOutput)
	}
	return out.bytes, nil
}

type cosignBuffer struct {
	bytes    []byte
	overflow bool
}

func (b *cosignBuffer) Write(p []byte) (int, error) {
	n := min(len(p), maxCosignOutput-len(b.bytes))
	b.bytes = append(b.bytes, p[:n]...)
	b.overflow = b.overflow || n < len(p)
	return len(p), nil
}
