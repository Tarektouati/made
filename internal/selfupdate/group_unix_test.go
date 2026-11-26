//go:build unix

package selfupdate

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

func TestUpdatePreservesGroup(t *testing.T) {
	for _, name := range []string{"same group", "different group"} {
		t.Run(name, func(t *testing.T) {
			g := newFakeGitHub(t, "v0.7.0", map[string]*fakeRelease{"v0.7.0": release("v0.7.0")})
			exe := installed(t, "v0.6.0")
			info, err := os.Stat(exe)
			if err != nil {
				t.Fatal(err)
			}
			gid := info.Sys().(*syscall.Stat_t).Gid
			if name == "different group" {
				groups, groupsErr := os.Getgroups()
				if groupsErr != nil {
					t.Fatal(groupsErr)
				}
				found := false
				for _, group := range groups {
					if uint32(group) != gid {
						gid = uint32(group)
						found = true
						break
					}
				}
				if !found {
					t.Skip("no second permitted group")
				}
			}
			if err = os.Chown(exe, -1, int(gid)); err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(exe, 0o750); err != nil {
				t.Fatal(err)
			}
			if res := runUpdate(t, g, exe, "v0.6.0", "", false); res.err != nil {
				t.Fatalf("Run = %v, want success", res.err)
			}
			assertBinary(t, exe, "v0.7.0")
			assertPermissions(t, exe, 0o750)
			info, err = os.Stat(exe)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Sys().(*syscall.Stat_t).Gid; got != gid {
				t.Errorf("updated binary group = %d, want %d", got, gid)
			}
		})
	}
}

func TestWriteTempIgnoresGroupChangeFailure(t *testing.T) {
	exe := installed(t, "v0.6.0")
	if err := os.Chmod(exe, 0o750); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	groups = append(groups, os.Getegid())
	gid := 0
	for slices.Contains(groups, gid) {
		gid++
	}
	if err = os.Chown(exe, -1, gid); err == nil {
		t.Skip("process can change to a group outside its memberships")
	}
	// Model an installed binary whose group this user cannot switch to.
	info.Sys().(*syscall.Stat_t).Gid = uint32(gid)
	bin := versionScript("v0.7.0")
	tmp, err := writeTemp(filepath.Dir(exe), []byte(bin), info)
	if err != nil {
		t.Fatalf("writeTemp = %v, want success despite the denied group change", err)
	}
	got, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != bin {
		t.Errorf("temporary binary = %q, want %q", got, bin)
	}
	assertPermissions(t, tmp, 0o750)
	info, err = os.Stat(tmp)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Gid == uint32(gid) {
		t.Errorf("temporary binary unexpectedly changed to group %d", gid)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		t.Errorf("temporary binary owner = %d, want %d", stat.Uid, os.Geteuid())
	}
}
