package discover

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkTree creates root/<path> for each path: a trailing "/" makes a directory,
// anything else an empty file (parent directories are created as needed).
func mkTree(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// endlessReader returns zero bytes forever, like a /proc file that reports size 0.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestReadLimited(t *testing.T) {
	if _, err := readLimited(endlessReader{}, "x.md"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("readLimited(endless) error = %v, want errTooLarge", err)
	}
	atCap := io.LimitReader(endlessReader{}, MaxMadefileBytes)
	if b, err := readLimited(atCap, "x.md"); err != nil || len(b) != MaxMadefileBytes {
		t.Errorf("readLimited(at cap) = %d bytes, %v; want %d bytes, nil", len(b), err, MaxMadefileBytes)
	}
}
