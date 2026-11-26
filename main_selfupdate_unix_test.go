//go:build unix

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// addRelease adds release tag to f, for linux/amd64, with a shell script as the made
// binary that reports tag.
func (f *fakeReleases) addRelease(t *testing.T, tag string) {
	t.Helper()
	bin := []byte("#!/bin/sh\necho made " + tag + "\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "made", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(bin); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	asset, _ := selfupdate.AssetName(tag, "linux", "amd64")
	f.files[tag+"/"+asset] = buf.Bytes()
	f.files[tag+"/"+selfupdate.ChecksumsName] = fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(buf.Bytes()), asset)
}

func TestSelfUpdateReplaces(t *testing.T) {
	f := newFakeReleases(t, "v0.7.0")
	f.addRelease(t, "v0.7.0")
	in := manualInstall(t)
	a := useFakeInstall(t, f, in)
	var stdout, stderr bytes.Buffer
	if code := a.run([]string{"self-update"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(self-update) = %d, want 0 (stderr: %s)", code, &stderr)
	}
	if want := "made: updated v0.6.0 -> v0.7.0\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", &stdout, want)
	}
	if b, _ := os.ReadFile(in.Exe); !bytes.Contains(b, []byte("made v0.7.0")) {
		t.Errorf("%s = %q, want the v0.7.0 binary", in.Exe, b)
	}
}

func TestSelfUpdateUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to any directory")
	}
	f := newFakeReleases(t, "v0.7.0")
	f.addRelease(t, "v0.7.0")
	in := manualInstall(t)
	dir, err := filepath.EvalSymlinks(filepath.Dir(in.Exe))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	a := useFakeInstall(t, f, in)
	var stdout, stderr bytes.Buffer
	if code := a.run([]string{"self-update"}, &stdout, &stderr); code != selfupdate.ExitNotWritable {
		t.Fatalf("run(self-update) = %d, want %d (stderr: %s)", code, selfupdate.ExitNotWritable, &stderr)
	}
	checkContains(t, "stderr", stderr.String(), []string{
		"made: cannot write to " + dir + ": permission denied\n",
		"made: hint: run sudo made self-update, or reinstall with: curl -fsSL https://raw.githubusercontent.com/Tarektouati/made/main/install.sh | sh\n",
	}, true)
	if b, _ := os.ReadFile(in.Exe); string(b) != oldBinary {
		t.Errorf("%s = %q, want the old binary", in.Exe, b)
	}
}
