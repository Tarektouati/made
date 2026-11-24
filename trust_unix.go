//go:build unix

package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// maxSymlinkHops is how many symlinks checkParentMadefile follows from a parent
// Madefile to its file, as many as macOS resolves in one path.
const maxSymlinkHops = 32

// checkParentMadefile returns an *untrustedError if the Madefile at path, found in a
// parent directory, fails checkTrust: the file and the directory it was found in,
// and, when the name is a symlink, every link of the chain to the final file, the
// directory each link is in, and the final file and its directory. Each hop is
// followed with os.Readlink, its directory resolved to the physical one. The
// directories walked through on the way up, and the ancestors of the checked
// directories, are not checked: a file planted in a walked-through directory would
// be found first and fail the owner check itself.
//
// It returns the FileInfo of the final file it checked. The check works on paths,
// and so does the later open, so the owner of an unchecked ancestor directory (or a
// link's) could swap the file between the two; readMadefile therefore refuses to
// read anything but that same file (os.SameFile).
func checkParentMadefile(path string) (fs.FileInfo, error) {
	trusted := trustedUIDs()
	check := func(subject, p string, info fs.FileInfo) error {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot check the owner of %s", p)
		}
		isLink := info.Mode()&fs.ModeSymlink != 0
		if problem := checkTrust(st.Uid, info.Mode(), info.IsDir(), isLink, trusted); problem != trustOK {
			return &untrustedError{path: path, subject: subject, problem: problem, owner: ownerName(st.Uid), mode: info.Mode()}
		}
		return nil
	}
	checkedDirs := map[string]bool{}
	checkDir := func(dir string) error {
		if checkedDirs[dir] {
			return nil
		}
		checkedDirs[dir] = true
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		return check("directory "+dir, dir, info)
	}

	// cur is the current hop, in a physical directory: path's is, since discovery
	// walks up from the physical current directory.
	cur := path
	for hop := 0; ; hop++ {
		if err := checkDir(filepath.Dir(cur)); err != nil {
			return nil, err
		}
		info, err := os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			subject := "it"
			if hop > 0 {
				subject = "its target " + cur
			}
			if err = check(subject, cur, info); err != nil {
				return nil, err
			}
			return info, nil
		}
		subject := "the symlink"
		if hop > 0 {
			subject = "symlink " + cur
		}
		if err = check(subject, cur, info); err != nil {
			return nil, err
		}
		if hop == maxSymlinkHops {
			return nil, fmt.Errorf("cannot check %s: too many levels of symbolic links", path)
		}
		if cur, err = nextHop(cur); err != nil {
			return nil, err
		}
	}
}

// nextHop returns the path the symlink link points to, with its directory resolved
// to the physical one. The target's directory part is resolved by
// filepath.EvalSymlinks, not cleaned lexically, so ".." after a symlinked directory
// goes where the kernel would go.
func nextHop(link string) (string, error) {
	target, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Dir(link) + string(filepath.Separator) + target
	}
	i := strings.LastIndexByte(target, filepath.Separator)
	dir, base := target[:i], target[i+1:]
	if base == "" || base == "." || base == ".." {
		return "", fmt.Errorf("%s: %w", link, errNotRegular)
	}
	if dir == "" {
		dir = string(filepath.Separator)
	}
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(physical, base), nil
}

// trustedUIDs returns the owners a parent Madefile may have; see trustedUIDsFor.
func trustedUIDs() []uint32 {
	return trustedUIDsFor(os.Geteuid(), os.Getenv("SUDO_UID"))
}

// ownerName describes uid for an error message: "name (uid N)", or "uid N" when
// the user database has no entry for it.
func ownerName(uid uint32) string {
	id := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(id); err == nil && u.Username != "" {
		return fmt.Sprintf("%s (uid %s)", u.Username, id)
	}
	return "uid " + id
}

// discoveryHome returns the physical home directory, after which Madefile discovery
// stops (the home directory itself is still searched), or "" when HOME is unset,
// relative or cannot be resolved, and there is no such stop.
func discoveryHome() string {
	home := os.Getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return ""
	}
	physical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return ""
	}
	return physical
}
