//go:build !windows

package selfupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This tests the release wrapper's exit status and arguments, not cryptography.
// The shell function stands in for sign-blob inside the same shell process.
func TestReleaseSignerRequiresBundle(t *testing.T) {
	script, err := filepath.Abs("../../scripts/sign-checksums.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, sign string
		ok         bool
	}{
		{"signed", `printf 'test-only bundle\n' > "$bundle"`, true},
		{"missing", `:`, false},
		{"empty", `: > "$bundle"`, false},
		{"failed", `printf 'partial bundle\n' > "$bundle"; return 7`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			artifact, bundle := filepath.Join(dir, "checksums with spaces.txt"), filepath.Join(dir, "bundle with spaces.json")
			// #nosec G304 -- Both fixtures are in the private directory owned by this test.
			if err := os.WriteFile(artifact, []byte("checksum fixture\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// #nosec G304 -- A stale bundle fixture proves a no-output signer cannot pass the gate.
			if err := os.WriteFile(bundle, []byte("stale bundle\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			shell := `
cosign() {
    [ "$#" -eq 4 ] && [ "$1" = sign-blob ] && [ "$2" = "--bundle=$bundle" ] &&
        [ "$3" = "$artifact" ] && [ "$4" = --yes ] || return 9
    ` + c.sign + `
}
script=$1
shift
. "$script"
`
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			// #nosec G204 -- The shell program is a fixed test fixture; paths are positional arguments.
			cmd := exec.CommandContext(ctx, "sh", "-c", shell, "sign-checksums-test", script, artifact, bundle)
			cmd.WaitDelay = 2 * time.Second
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.ok {
				t.Errorf("signing wrapper = %v, %s; want success %v", err, out, c.ok)
			}
			if (c.name == "missing" || c.name == "empty") && !strings.Contains(string(out), "did not write a nonempty signature bundle") {
				t.Errorf("missing bundle error = %q", out)
			}
		})
	}
}
