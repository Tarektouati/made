//go:build unix

package discover

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// parentTree makes a repository root with a Madefile.md and a subdirectory a, changes
// to a, and returns the physical root. The .git entry keeps the walk inside it.
func parentTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkTree(t, root, ".git/", "Madefile.md", "a/")
	t.Chdir(filepath.Join(root, "a"))
	return root
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// wantUntrusted checks that err is an *UntrustedError for path with problem, whose
// message names subject.
func wantUntrusted(t *testing.T, err error, path string, problem trustProblem, subject string) {
	t.Helper()
	var ue *UntrustedError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want an *untrustedError", err)
	}
	if ue.Path != path || ue.problem != problem {
		t.Errorf("untrustedError = {path %q, problem %d}, want {path %q, problem %d}", ue.Path, ue.problem, path, problem)
	}
	if !strings.Contains(err.Error(), subject) {
		t.Errorf("err = %q, want it to name %q", err, subject)
	}
}

func TestResolveFileRefusesWritableParentMadefile(t *testing.T) {
	tests := []struct {
		name    string
		file    os.FileMode
		dir     os.FileMode
		problem trustProblem
		subject string // a part of the message
	}{
		{"group-writable file", 0o664, 0o755, groupWritable, "it is group-writable (mode 0664)"},
		{"world-writable file", 0o646, 0o755, worldWritable, "it is world-writable (mode 0646)"},
		{"group-writable directory", 0o644, 0o775, groupWritable, "is group-writable (mode 0775)"},
		{"world-writable directory", 0o644, 0o777, worldWritable, "is world-writable (mode 0777)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parentTree(t)
			mf := filepath.Join(root, "Madefile.md")
			chmod(t, mf, tt.file)
			chmod(t, root, tt.dir)
			t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
			res, err := ResolveFile("")
			wantUntrusted(t, err, mf, tt.problem, tt.subject)
			if res.Path != mf || !res.FromParent {
				t.Errorf("resolveFile = %+v, want the refused path %q from a parent", res, mf)
			}
		})
	}
}

func TestResolveFileAcceptsTrustedParentMadefile(t *testing.T) {
	tests := []struct {
		name string
		file os.FileMode
		dir  os.FileMode
	}{
		{"own 0644 file in own 0755 directory", 0o644, 0o755},
		{"read-only file", 0o444, 0o700},
		{"sticky world-writable directory", 0o644, 0o777 | os.ModeSticky},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parentTree(t)
			mf := filepath.Join(root, "Madefile.md")
			chmod(t, mf, tt.file)
			chmod(t, root, tt.dir)
			t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
			res, err := ResolveFile("")
			if err != nil {
				t.Fatalf("resolveFile: %v", err)
			}
			if res.Path != mf || !res.FromParent {
				t.Errorf("resolveFile = %+v, want path %q from a parent", res, mf)
			}
		})
	}
}

// TestResolveFileDoesNotCheckCwdOrExplicit checks that only discovery in a parent
// directory is checked: a Madefile in the current directory and a -f path are used
// as before, however writable.
func TestResolveFileDoesNotCheckCwdOrExplicit(t *testing.T) {
	root := parentTree(t)
	mf := filepath.Join(root, "Madefile.md")
	chmod(t, mf, 0o666)
	chmod(t, root, 0o777)
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	if res, err := ResolveFile(mf); err != nil || !res.Explicit {
		t.Errorf("resolveFile(-f %q) = %+v, %v; want it used as given", mf, res, err)
	}
	t.Chdir(root)
	if res, err := ResolveFile(""); err != nil || res.Path != "Madefile.md" || res.FromParent {
		t.Errorf("resolveFile in the Madefile's directory = %+v, %v; want Madefile.md", res, err)
	}
}

func TestResolveFileChecksBothEndsOfASymlink(t *testing.T) {
	tests := []struct {
		name      string
		targetDir os.FileMode
		target    os.FileMode
		problem   trustProblem // trustOK: accepted
		subject   string
	}{
		{"trusted target", 0o755, 0o644, trustOK, ""},
		{"target in a world-writable directory", 0o777, 0o644, worldWritable, "directory "},
		{"target in a sticky world-writable directory", 0o777 | os.ModeSticky, 0o644, trustOK, ""},
		{"group-writable target", 0o755, 0o664, groupWritable, "its target "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "proj")
			shared := filepath.Join(base, "shared")
			mkTree(t, base, "proj/.git/", "proj/a/", "shared/tasks.md")
			mf := filepath.Join(root, "Madefile.md")
			if err = os.Symlink(filepath.Join("..", "shared", "tasks.md"), mf); err != nil {
				t.Fatal(err)
			}
			chmod(t, filepath.Join(shared, "tasks.md"), tt.target)
			chmod(t, shared, tt.targetDir)
			t.Cleanup(func() { _ = os.Chmod(shared, 0o700) })
			t.Chdir(filepath.Join(root, "a"))

			res, err := ResolveFile("")
			if tt.problem == trustOK {
				if err != nil || res.Path != mf {
					t.Errorf("resolveFile = %+v, %v; want %q", res, err, mf)
				}
				return
			}
			wantUntrusted(t, err, mf, tt.problem, tt.subject+shared)
		})
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestResolveFileChecksEveryHopOfASymlinkChain checks the middle link of a two-hop
// chain, proj/Madefile.md -> ../mid/link2 -> ../shared/tasks.md: its directory, and
// a ".." that follows a symlinked directory the way the kernel does.
func TestResolveFileChecksEveryHopOfASymlinkChain(t *testing.T) {
	tests := []struct {
		name    string
		mid     os.FileMode // mode of the middle link's directory
		target  os.FileMode
		problem trustProblem // trustOK: accepted
		subject string       // followed by the path below base in the message
		path    string
	}{
		{"trusted chain", 0o755, 0o644, trustOK, "", ""},
		{"middle link in a world-writable directory", 0o777, 0o644, worldWritable, "directory ", "mid"},
		{"middle link in a group-writable directory", 0o775, 0o644, groupWritable, "directory ", "mid"},
		{"middle link in a sticky directory", 0o777 | os.ModeSticky, 0o644, trustOK, "", ""},
		{"group-writable file after two hops", 0o755, 0o664, groupWritable, "its target ", "shared/tasks.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			mkTree(t, base, "proj/.git/", "proj/a/", "mid/", "shared/tasks.md")
			mf := filepath.Join(base, "proj", "Madefile.md")
			symlink(t, filepath.Join("..", "mid", "link2"), mf)
			symlink(t, filepath.Join("..", "shared", "tasks.md"), filepath.Join(base, "mid", "link2"))
			chmod(t, filepath.Join(base, "shared", "tasks.md"), tt.target)
			chmod(t, filepath.Join(base, "mid"), tt.mid)
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(base, "mid"), 0o700) })
			t.Chdir(filepath.Join(base, "proj", "a"))

			res, err := ResolveFile("")
			if tt.problem == trustOK {
				if err != nil || res.Path != mf {
					t.Errorf("resolveFile = %+v, %v; want %q", res, err, mf)
				}
				return
			}
			wantUntrusted(t, err, mf, tt.problem, tt.subject+filepath.Join(base, filepath.FromSlash(tt.path)))
		})
	}

	t.Run("dot-dot after a symlinked directory", func(t *testing.T) {
		// proj/Madefile.md -> sub/../tasks.md with proj/sub -> ../elsewhere/deep: the
		// kernel opens elsewhere/tasks.md, not proj/tasks.md, so that is what is checked.
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		mkTree(t, base, "proj/.git/", "proj/a/", "proj/tasks.md", "elsewhere/deep/", "elsewhere/tasks.md")
		symlink(t, filepath.Join("..", "elsewhere", "deep"), filepath.Join(base, "proj", "sub"))
		mf := filepath.Join(base, "proj", "Madefile.md")
		symlink(t, "sub/../tasks.md", mf) // not filepath.Join, which would clean away the ".."
		chmod(t, filepath.Join(base, "elsewhere", "tasks.md"), 0o666)
		t.Chdir(filepath.Join(base, "proj", "a"))
		_, err = ResolveFile("")
		wantUntrusted(t, err, mf, worldWritable, "its target "+filepath.Join(base, "elsewhere", "tasks.md"))
	})
}

// TestReadMadefileRefusesSwappedParentMadefile reproduces the swap between the check
// and the read: the owner of a directory above the checked one moves the project away
// and puts a new tree with a Madefile of their choosing in its place.
func TestReadMadefileRefusesSwappedParentMadefile(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(base, "proj")
	mkTree(t, proj, ".git/", "Madefile.md", "a/")
	t.Chdir(filepath.Join(proj, "a"))
	res, err := ResolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if res.Trusted == nil {
		t.Fatal("resolveFile returned no trusted FileInfo for a parent Madefile")
	}

	if err = os.Rename(proj, proj+".old"); err != nil {
		t.Fatal(err)
	}
	mkTree(t, proj, ".git/", "Madefile.md", "a/")
	if _, _, err = ReadMadefile(res.Path, false, res.Trusted); !errors.Is(err, ErrChangedAfterCheck) {
		t.Errorf("readMadefile after the swap: err = %v, want %v", err, ErrChangedAfterCheck)
	}
}

// TestCheckParentMadefileSymlinkLimits checks that a symlink loop and a chain longer
// than maxSymlinkHops fail with an error, without hanging. Discovery already skips
// both, as a stat of them fails, so checkParentMadefile is called directly.
func TestCheckParentMadefileSymlinkLimits(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	symlink(t, "loop2", filepath.Join(dir, "loop1"))
	symlink(t, "loop1", filepath.Join(dir, "loop2"))
	if _, err = checkParentMadefile(filepath.Join(dir, "loop1")); err == nil || !strings.Contains(err.Error(), "too many levels of symbolic links") {
		t.Errorf("checkParentMadefile(loop) = %v, want too many levels of symbolic links", err)
	}

	mkTree(t, dir, "tasks.md")
	prev := "tasks.md"
	for i := range maxSymlinkHops + 1 {
		name := "hop" + strconv.Itoa(i)
		symlink(t, prev, filepath.Join(dir, name))
		prev = name
	}
	info, err := checkParentMadefile(filepath.Join(dir, "hop"+strconv.Itoa(maxSymlinkHops-1)))
	if err != nil {
		t.Errorf("checkParentMadefile(%d hops) = %v, want nil", maxSymlinkHops, err)
	} else if want, _ := os.Stat(filepath.Join(dir, "tasks.md")); !os.SameFile(info, want) {
		t.Errorf("checkParentMadefile(%d hops) returned %v, want the FileInfo of tasks.md", maxSymlinkHops, info)
	}
	if _, err = checkParentMadefile(filepath.Join(dir, prev)); err == nil || !strings.Contains(err.Error(), "too many levels") {
		t.Errorf("checkParentMadefile(%d hops) = %v, want too many levels of symbolic links", maxSymlinkHops+1, err)
	}
}

func TestFindMadefileStopsAfterHome(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkTree(t, base, "Madefile.md", "home/a/")
	home := filepath.Join(base, "home")
	cwd := filepath.Join(home, "a")

	t.Setenv("HOME", home)
	t.Chdir(cwd)
	if res, err := ResolveFile(""); err == nil {
		t.Errorf("resolveFile with HOME=%q = %+v, want not found above HOME", home, res)
	}

	// HOME itself is still searched.
	mkTree(t, base, "home/Madefile.md")
	if res, err := ResolveFile(""); err != nil || res.Path != filepath.Join(home, "Madefile.md") {
		t.Errorf("resolveFile = %+v, %v; want the Madefile in HOME", res, err)
	}
	if err := os.Remove(filepath.Join(home, "Madefile.md")); err != nil {
		t.Fatal(err)
	}

	// HOME given through a symlink stops at its physical directory.
	link := filepath.Join(base, "home-link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	if res, err := ResolveFile(""); err == nil {
		t.Errorf("resolveFile with HOME=%q = %+v, want not found above HOME", link, res)
	}

	// No stop for an empty or relative HOME, or one outside the walk.
	for _, h := range []string{"", "home", filepath.Join(base, "elsewhere")} {
		t.Setenv("HOME", h)
		if res, err := ResolveFile(""); err != nil || res.Path != filepath.Join(base, "Madefile.md") {
			t.Errorf("resolveFile with HOME=%q = %+v, %v; want %q", h, res, err, filepath.Join(base, "Madefile.md"))
		}
	}
}

// TestTrustedUIDsIgnoresSudoUIDWhenNotRoot checks that SUDO_UID adds nobody to the
// trusted owners unless made runs as root.
func TestTrustedUIDsIgnoresSudoUIDWhenNotRoot(t *testing.T) {
	euid := os.Geteuid()
	if euid == 0 {
		t.Skip("running as root, where SUDO_UID counts")
	}
	t.Setenv("SUDO_UID", "65534")
	if got, want := trustedUIDs(), []uint32{0, uint32(euid)}; !slices.Equal(got, want) {
		t.Errorf("trustedUIDs() with SUDO_UID=65534 = %v, want %v", got, want)
	}
}

// TestResolveFileRefusesOtherOwner chowns the parent Madefile, then its directory, to
// another user. Only root can chown, so it is skipped otherwise.
func TestResolveFileRefusesOtherOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown to another user needs root")
	}
	t.Setenv("SUDO_UID", "")
	const nobody = 65534
	for _, target := range []string{"file", "directory"} {
		t.Run(target, func(t *testing.T) {
			root := parentTree(t)
			mf := filepath.Join(root, "Madefile.md")
			chowned, subject := mf, "it is owned by "
			if target == "directory" {
				chowned, subject = root, "directory "+root+" is owned by "
			}
			if err := os.Chown(chowned, nobody, -1); err != nil {
				t.Fatal(err)
			}
			_, err := ResolveFile("")
			wantUntrusted(t, err, mf, untrustedOwner, subject)
			if !strings.Contains(err.Error(), "uid 65534") {
				t.Errorf("err = %q, want it to name uid 65534", err)
			}
		})
	}
}
