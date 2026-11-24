package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCheckTrust(t *testing.T) {
	const me, other = 501, 65534
	trusted := []uint32{0, me}
	tests := []struct {
		name   string
		uid    uint32
		mode   fs.FileMode
		isDir  bool
		isLink bool
		want   trustProblem
	}{
		{"own file 0644", me, 0o644, false, false, trustOK},
		{"own file 0600", me, 0o600, false, false, trustOK},
		{"root file 0644", 0, 0o644, false, false, trustOK},
		{"other user's file", other, 0o644, false, false, untrustedOwner},
		{"group-writable file", me, 0o664, false, false, groupWritable},
		{"world-writable file", me, 0o646, false, false, worldWritable},
		{"group- and world-writable file", me, 0o666, false, false, worldWritable},
		{"other user's writable file", other, 0o666, false, false, untrustedOwner},
		{"sticky bit on a file does not help", me, 0o666 | fs.ModeSticky, false, false, worldWritable},
		{"own dir 0755", me, fs.ModeDir | 0o755, true, false, trustOK},
		{"group-writable dir", me, fs.ModeDir | 0o775, true, false, groupWritable},
		{"world-writable dir", 0, fs.ModeDir | 0o777, true, false, worldWritable},
		{"sticky world-writable root dir (/tmp)", 0, fs.ModeDir | fs.ModeSticky | 0o777, true, false, trustOK},
		{"sticky world-writable own dir", me, fs.ModeDir | fs.ModeSticky | 0o777, true, false, trustOK},
		{"sticky dir of another user", other, fs.ModeDir | fs.ModeSticky | 0o777, true, false, untrustedOwner},
		{"other user's dir 0755", other, fs.ModeDir | 0o755, true, false, untrustedOwner},
		{"own symlink (mode 0777 ignored)", me, fs.ModeSymlink | 0o777, false, true, trustOK},
		{"other user's symlink", other, fs.ModeSymlink | 0o777, false, true, untrustedOwner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkTrust(tt.uid, tt.mode, tt.isDir, tt.isLink, trusted); got != tt.want {
				t.Errorf("checkTrust(%d, %v, dir=%v, link=%v) = %d, want %d", tt.uid, tt.mode, tt.isDir, tt.isLink, got, tt.want)
			}
		})
	}
}

// TestCheckTrustSudoUID checks that the sudo user's files are trusted only when their
// uid is in the trusted set, which trustedUIDs adds only when running as root.
func TestCheckTrustSudoUID(t *testing.T) {
	const sudoUser = 1000
	asRootUnderSudo := []uint32{0, sudoUser}
	asRootAlone := []uint32{0}
	if got := checkTrust(sudoUser, 0o644, false, false, asRootUnderSudo); got != trustOK {
		t.Errorf("sudo user's file under sudo = %d, want trustOK", got)
	}
	if got := checkTrust(sudoUser, 0o644, false, false, asRootAlone); got != untrustedOwner {
		t.Errorf("sudo user's file without SUDO_UID = %d, want untrustedOwner", got)
	}
}

func TestParseSudoUID(t *testing.T) {
	tests := []struct {
		in   string
		want uint32
		ok   bool
	}{
		{"1000", 1000, true},
		{"0", 0, true},
		{"4294967294", 4294967294, true},
		{"", 0, false},
		{"abc", 0, false},
		{"-1", 0, false},
		{"+1000", 0, false},
		{" 1000", 0, false},
		{"0x3e8", 0, false},
		{"4294967295", 0, false}, // (uid_t)-1
		{"4294967296", 0, false}, // overflows uint32
	}
	for _, tt := range tests {
		if got, ok := parseSudoUID(tt.in); got != tt.want || ok != tt.ok {
			t.Errorf("parseSudoUID(%q) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestTrustedUIDsFor(t *testing.T) {
	tests := []struct {
		name    string
		euid    int
		sudoUID string
		want    []uint32
	}{
		{"user", 501, "", []uint32{0, 501}},
		{"user ignores SUDO_UID", 501, "65534", []uint32{0, 501}},
		{"root", 0, "", []uint32{0}},
		{"root under sudo", 0, "1000", []uint32{0, 1000}},
		{"root under sudo from root", 0, "0", []uint32{0}},
		{"root with a bad SUDO_UID", 0, "abc", []uint32{0}},
		{"root with SUDO_UID -1", 0, "-1", []uint32{0}},
		{"root with an overflowing SUDO_UID", 0, "4294967296", []uint32{0}},
		{"no euid (windows)", -1, "1000", []uint32{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trustedUIDsFor(tt.euid, tt.sudoUID); !slices.Equal(got, tt.want) {
				t.Errorf("trustedUIDsFor(%d, %q) = %v, want %v", tt.euid, tt.sudoUID, got, tt.want)
			}
		})
	}
}

func TestUntrustedErrorMessage(t *testing.T) {
	tests := []struct {
		err  untrustedError
		want string
	}{
		{
			untrustedError{path: "/p/Madefile.md", subject: "it", problem: untrustedOwner, owner: "nobody (uid 65534)"},
			"refusing /p/Madefile.md found in a parent directory: it is owned by nobody (uid 65534), not by you or root",
		},
		{
			untrustedError{path: "/p/Madefile.md", subject: "it", problem: groupWritable, mode: 0o664},
			"refusing /p/Madefile.md found in a parent directory: it is group-writable (mode 0664)",
		},
		{
			untrustedError{path: "/p/Madefile.md", subject: "directory /p", problem: worldWritable, mode: fs.ModeDir | 0o777},
			"refusing /p/Madefile.md found in a parent directory: directory /p is world-writable (mode 0777)",
		},
		{
			// A problem without a message of its own must not read as another one.
			untrustedError{path: "/p/Madefile.md", subject: "it", problem: trustOK, mode: 0o664},
			"refusing /p/Madefile.md found in a parent directory: it failed the trust check (problem 0)",
		},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

// TestReadMadefileRefusesAnotherFile checks that readMadefile reads a parent Madefile
// only if it is still the file checkParentMadefile checked.
func TestReadMadefileRefusesAnotherFile(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, "Madefile.md", "other.md")
	path := filepath.Join(dir, "Madefile.md")
	self, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.Stat(filepath.Join(dir, "other.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = readMadefile(path, false, other); !errors.Is(err, errChangedAfterCheck) {
		t.Errorf("readMadefile with another file's FileInfo: err = %v, want %v", err, errChangedAfterCheck)
	}
	for _, trusted := range []fs.FileInfo{self, nil} {
		if _, _, err = readMadefile(path, false, trusted); err != nil {
			t.Errorf("readMadefile(trusted %v) = %v, want nil", trusted, err)
		}
	}
}
