package main

import (
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strconv"
)

// A Madefile discovered in a parent of the current directory is only used if nobody
// but the current user or root could have put it there or changed it (see
// checkParentMadefile, unix only). This is the check git added for CVE-2022-24765:
// without it, another user who can write to a parent directory (a world-writable
// /tmp/x, a shared mount) gets their tasks run as whoever runs made below it. A
// Madefile in the current directory, and any file given with -f, are not checked.

// trustProblem is why a path on the way to a parent Madefile is not trusted.
type trustProblem int

const (
	trustOK trustProblem = iota
	untrustedOwner
	worldWritable
	groupWritable
)

// checkTrust is the trust rule for one path, given its owner uid and mode: the owner
// must be one of trusted, and the path must not be group- or world-writable. A
// directory with the sticky bit (like /tmp) may be writable by others, because they
// cannot rename or remove a file they do not own in it, and a file they create there
// fails the owner check. A symlink's own mode means nothing, so only its owner is
// checked (isLink).
func checkTrust(uid uint32, mode fs.FileMode, isDir, isLink bool, trusted []uint32) trustProblem {
	switch {
	case !slices.Contains(trusted, uid):
		return untrustedOwner
	case isLink, isDir && mode&fs.ModeSticky != 0:
		return trustOK
	case mode&0o002 != 0:
		return worldWritable
	case mode&0o020 != 0:
		return groupWritable
	}
	return trustOK
}

// trustedUIDsFor returns the owners a parent Madefile may have when made runs with
// effective uid euid: root and euid and, when euid is root and sudoUID (SUDO_UID) is
// valid, the user who ran sudo, as git does, so that sudo made in your own project
// still finds its Madefile. sudoUID only counts when made already runs as root, so
// it cannot widen anyone's trust.
func trustedUIDsFor(euid int, sudoUID string) []uint32 {
	trusted := []uint32{0}
	if euid > 0 && euid < math.MaxUint32 {
		trusted = append(trusted, uint32(euid))
	}
	if euid == 0 {
		if uid, ok := parseSudoUID(sudoUID); ok && uid != 0 {
			trusted = append(trusted, uid)
		}
	}
	return trusted
}

// parseSudoUID parses a SUDO_UID value: a decimal uid without a sign. It rejects
// 4294967295, which is (uid_t)-1 and never a real user.
func parseSudoUID(s string) (uint32, bool) {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == math.MaxUint32 {
		return 0, false
	}
	return uint32(n), true
}

// untrustedError is the error for a parent Madefile that failed checkTrust.
type untrustedError struct {
	path    string // the Madefile as discovered
	subject string // what failed: "it", or a kind and path such as "directory /tmp/x"
	problem trustProblem
	owner   string // the owner, as a name and uid when known, for untrustedOwner
	mode    fs.FileMode
}

func (e *untrustedError) Error() string {
	var why string
	switch e.problem {
	case untrustedOwner:
		why = fmt.Sprintf("is owned by %s, not by you or root", e.owner)
	case worldWritable:
		why = fmt.Sprintf("is world-writable (mode %04o)", e.mode.Perm())
	case groupWritable:
		why = fmt.Sprintf("is group-writable (mode %04o)", e.mode.Perm())
	default:
		why = fmt.Sprintf("failed the trust check (problem %d)", e.problem)
	}
	return fmt.Sprintf("refusing %s found in a parent directory: %s %s", e.path, e.subject, why)
}
