//go:build unix

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRefusesWritableParentMadefile checks that made refuses a world-writable
// Madefile found in a parent directory for every command, says why and how to run
// it anyway, and that -f still runs it.
func TestRefusesWritableParentMadefile(t *testing.T) {
	requireBash(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mf := filepath.Join(dir, "Madefile.md")
	writeFile(t, mf, catMarkerMD)
	writeFile(t, filepath.Join(dir, "marker.txt"), "in-root\n")
	// #nosec G302 -- A world-writable fixture is required to test the parent-file trust check.
	if err := os.Chmod(mf, 0o666); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "a", "b")
	// #nosec G301 -- The fixture tree is inside a private temporary directory.
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"where"}, {"--list"}, {"--check"}, {"--list-names"}} {
		stdout, stderr, code := runMade(t, nested, args...)
		if code != 1 || stdout != "" {
			t.Errorf("made %v: exit %d, stdout %q; want exit 1 and no output (stderr: %s)", args, code, stdout, stderr)
		}
		for _, want := range []string{
			"made: refusing " + mf + " found in a parent directory: it is world-writable (mode 0666)\n",
			"made -f " + mf,
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("made %v: stderr = %q, want %q", args, stderr, want)
			}
		}
	}

	stdout, stderr, code := runMade(t, nested, "-f", mf, "where")
	if code != 0 || stdout != "in-root\n" {
		t.Errorf("made -f %s where: exit %d, stdout %q (stderr: %s); want the task run", mf, code, stdout, stderr)
	}
}
