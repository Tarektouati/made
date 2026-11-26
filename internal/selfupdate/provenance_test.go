package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type verifierFunc func(context.Context, string, []byte, []byte) error

func (f verifierFunc) Verify(ctx context.Context, tag string, checksums, bundle []byte) error {
	return f(ctx, tag, checksums, bundle)
}

func TestCosignVerifierPinsReleaseIdentity(t *testing.T) {
	sums := []byte("exact checksum bytes\r\n")
	bundle := []byte("exact signature bytes\n")
	var calls [][]string
	var dir string
	v := cosignVerifier{run: func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > cosignTimeout {
			t.Errorf("command context deadline = %v, %v, want bounded verification", deadline, ok)
		}
		if len(calls) == 1 {
			if !reflect.DeepEqual(args, []string{"version", "--json"}) {
				t.Errorf("version arguments = %q", args)
			}
			return []byte(`{"gitVersion":"v3.1.6"}`), nil
		}
		if len(args) != 8 {
			t.Fatalf("verification arguments = %q", args)
		}
		dir = filepath.Dir(args[7])
		want := []string{
			"verify-blob", "--certificate-identity",
			"https://github.com/Tarektouati/made/.github/workflows/release.yml@refs/tags/v1.2.3",
			"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
			"--bundle", filepath.Join(dir, "checksums.txt.sigstore.json"), filepath.Join(dir, "checksums.txt"),
		}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("verification arguments = %q, want %q", args, want)
		}
		for path, want := range map[string][]byte{args[6]: bundle, args[7]: sums} {
			// #nosec G304 -- Inspect only the temporary verification files passed to this test runner.
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("verification file %s = %q, %v, want exact bytes %q", path, got, err, want)
			}
			if info, err := os.Stat(path); err != nil {
				t.Fatal(err)
			} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("verification file mode = %v, want 0600", info.Mode())
			}
		}
		if info, err := os.Stat(dir); err != nil {
			t.Fatal(err)
		} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("verification directory mode = %v, want 0700", info.Mode())
		}
		return nil, nil
	}}
	if err := v.Verify(t.Context(), "v1.2.3", sums, bundle); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Errorf("%d commands, want version then verification", len(calls))
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("verification directory remains: %v", err)
	}
}

func TestCosignVerifierVersionFloor(t *testing.T) {
	for _, c := range []struct {
		version string
		ok      bool
	}{
		{"v3.1.6", true}, {"v3.1.7", true}, {"v3.2.0", true}, {"v4.0.0", true},
		{"v3.1.5", false}, {"v3.0.0", false}, {"v2.6.0", false},
		{"v3.2.0-rc.1", false}, {"v3.1.6+dirty", false}, {"v03.1.6", false},
		{"3.1.6", false}, {"dev", false}, {"", false},
	} {
		t.Run(c.version, func(t *testing.T) {
			calls := 0
			v := cosignVerifier{run: func(context.Context, ...string) ([]byte, error) {
				calls++
				return fmt.Appendf(nil, `{"gitVersion":%q}`, c.version), nil
			}}
			err := v.Verify(t.Context(), "v1.2.3", nil, nil)
			if (err == nil) != c.ok {
				t.Errorf("Verify with %q = %v, want success %v", c.version, err, c.ok)
			}
			wantCalls := 1
			if c.ok {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Errorf("%d commands, want %d", calls, wantCalls)
			}
		})
	}
}

func TestCosignVerifierRejectsMalformedVersion(t *testing.T) {
	for _, out := range []string{"not JSON", `{}`, `{"gitVersion":3}`, `{"gitVersion":"v3.1.6"} trailing`} {
		t.Run(out, func(t *testing.T) {
			calls := 0
			v := cosignVerifier{run: func(context.Context, ...string) ([]byte, error) {
				calls++
				return []byte(out), nil
			}}
			if err := v.Verify(t.Context(), "v1.2.3", nil, nil); err == nil {
				t.Error("malformed version was accepted")
			}
			if calls != 1 {
				t.Errorf("%d commands, want only the version check", calls)
			}
		})
	}
}

func TestCosignVerifierFailuresCleanUp(t *testing.T) {
	for _, step := range []string{"version", "verify-blob"} {
		t.Run(step, func(t *testing.T) {
			wantErr := errors.New("cosign command failed")
			var dir string
			v := cosignVerifier{run: func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == "verify-blob" {
					dir = filepath.Dir(args[len(args)-1])
				}
				if args[0] == step {
					return nil, wantErr
				}
				return []byte(`{"gitVersion":"v3.1.6"}`), nil
			}}
			if err := v.Verify(t.Context(), "v1.2.3", nil, nil); !errors.Is(err, wantErr) {
				t.Errorf("Verify = %v, want %v", err, wantErr)
			}
			if dir != "" {
				if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("verification directory remains after failure: %v", err)
				}
			}
		})
	}
}

func TestCosignVerifierMissingExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := (cosignVerifier{}).Verify(t.Context(), "v1.2.3", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "cosign v3.1.6 or later is required") {
		t.Errorf("Verify = %v, want an actionable missing Cosign error", err)
	}
}

func TestCosignVerifierCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	v := cosignVerifier{run: func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "version" {
			return []byte(`{"gitVersion":"v3.1.6"}`), nil
		}
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	defer cancel()
	if err := v.Verify(ctx, "v1.2.3", nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Verify = %v, want cancellation", err)
	}
}

// The helper is the test binary itself, not a substitute Cosign installation.
// It exercises the real subprocess cancellation and output bounds without a network.
func TestCosignCommandHelper(_ *testing.T) {
	for i, arg := range os.Args {
		if arg != "--" || i+1 >= len(os.Args) {
			continue
		}
		switch os.Args[i+1] {
		case "output":
			fmt.Print(strings.Repeat("x", maxCosignOutput+1))
		case "failure":
			fmt.Print("signature rejected")
			os.Exit(7)
		case "wait":
			time.Sleep(time.Minute)
		}
		return
	}
}

func TestCosignCommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		mode, want string
	}{
		{"output", "cosign output exceeds"},
		{"failure", "signature rejected"},
		{"wait", "context deadline exceeded"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			timeout := 5 * time.Second
			if c.mode == "wait" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			out, err := cosignOutput(ctx, exe, "-test.run=^TestCosignCommandHelper$", "--", c.mode)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("cosignOutput = %q, %v, want %s", out, err, c.want)
			}
			if len(out) > maxCosignOutput {
				t.Errorf("captured %d output bytes, limit %d", len(out), maxCosignOutput)
			}
			if c.mode == "wait" && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("cosignOutput = %v, want wrapped deadline", err)
			}
		})
	}
}
