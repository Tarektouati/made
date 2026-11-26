// Package discover finds and reads Madefiles, including parent-directory trust checks.
package discover

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Names returns the exact task filenames, in discovery priority order.
func Names() [3]string { return [3]string{"Madefile.md", "madefile.md", "MADEFILE.md"} }

// MaxMadefileBytes is the largest task file made will read.
const MaxMadefileBytes = 10 << 20 // 10 MiB

// Resolved is the task file chosen by ResolveFile.
type Resolved struct {
	Path       string   // path to read; a bare name when found in the current directory
	Explicit   bool     // given with -f rather than discovered
	FromParent bool     // discovered in a parent of the current directory
	Others     []string // other Madefile names found in the same directory, by priority
	Skipped    []SkippedMadefile
	Trusted    fs.FileInfo // for a parent Madefile, the file checkParentMadefile checked
}

// SkippedMadefile is a Madefile name present in a directory that could not be used.
type SkippedMadefile struct {
	Path string
	Err  error
}

// ResolveFile returns the task file to read. A non-empty file (given with -f) is used
// as is. Otherwise the nearest Madefile in the current directory or one of its parents
// is used (see FindMadefile); if there is none, ResolveFile returns an error. The walk
// starts from the physical current directory, with symlinks resolved, and on unix
// stops after the home directory (see discoveryHome). Unusable candidates are
// reported in skipped, also alongside the not-found error. A Madefile found in a
// parent directory must pass checkParentMadefile, or ResolveFile returns its
// *UntrustedError; the file it checked is returned in trusted, for ReadMadefile.
func ResolveFile(file string) (Resolved, error) {
	if file != "" {
		return Resolved{Path: file, Explicit: true}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Resolved{}, fmt.Errorf("cannot determine current directory: %w", err)
	}
	start := cwd
	if physical, err := filepath.EvalSymlinks(cwd); err == nil {
		start = physical
	}
	dir, name, others, skipped, ok := FindMadefile(start, discoveryHome())
	if !ok {
		return Resolved{Skipped: skipped}, fmt.Errorf("no %s found in %s or its parents (use -f to choose a file, or made init to create one)", Names()[0], start)
	}
	res := Resolved{Path: name, Others: others, Skipped: skipped} // keep the short name when it's right here
	if dir != start {
		res.Path = filepath.Join(dir, name)
		res.FromParent = true
		trusted, err := checkParentMadefile(res.Path)
		if err != nil {
			return res, err
		}
		res.Trusted = trusted
	}
	return res, nil
}

// FindMadefile walks from start upward and returns the first directory holding a
// regular file named exactly like one of Names(), with the highest-priority
// name found and any other matching names in that directory. The walk stops after
// the first directory containing a .git entry (a directory, or a file for worktrees
// and submodules), or after stop when it is not empty, otherwise at the filesystem
// root. Names that are present but
// unusable (broken or looping symlinks, non-regular files, names that cannot be
// looked up because a directory cannot be searched), and directories that exist
// but cannot be listed, are returned in skipped, nearest first, and the walk goes
// on. A regular file that cannot be read is still returned, so the caller fails on
// it rather than running a parent's Madefile.
func FindMadefile(start, stop string) (dir, name string, others []string, skipped []SkippedMadefile, ok bool) {
	for dir = start; ; {
		entries, err := os.ReadDir(dir)
		boundary := false
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			skipped = append(skipped, SkippedMadefile{dir, pathErrReason(err)})
		}
		if err == nil {
			present := map[string]bool{}
			for _, e := range entries {
				present[e.Name()] = true
			}
			boundary = present[".git"]
			for _, n := range Names() {
				if !present[n] {
					continue
				}
				if err := CheckMadefile(filepath.Join(dir, n)); err != nil {
					skipped = append(skipped, SkippedMadefile{filepath.Join(dir, n), err})
					continue
				}
				if name == "" {
					name = n
				} else {
					others = append(others, n)
				}
			}
			if name != "" {
				return dir, name, others, skipped, true
			}
		}
		parent := filepath.Dir(dir)
		if boundary || dir == stop || parent == dir {
			return "", "", nil, skipped, false
		}
		dir = parent
	}
}

// ReadMadefile reads a task file of at most MaxMadefileBytes. A discovered file must
// be a regular file, checked before it is opened, because opening a FIFO blocks. An
// explicit (-f) file may be anything readable but a directory, such as a pipe or
// /dev/stdin; a FIFO with no writer then blocks, like cat. The size check is repeated
// while reading, because some files report a wrong size (0 for /proc files) or grow
// after the stat. regular reports whether the file read was a regular file. When
// trusted is not nil (a parent Madefile that passed checkParentMadefile), the file
// opened must be that same file, or ReadMadefile fails with ErrChangedAfterCheck.
func ReadMadefile(path string, explicit bool, trusted fs.FileInfo) (source []byte, regular bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if explicit && info.IsDir() {
		return nil, false, &fs.PathError{Op: "read", Path: path, Err: errIsDir}
	}
	if !explicit && !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().IsRegular() && info.Size() > MaxMadefileBytes {
		return nil, false, fmt.Errorf("%s %w (%d bytes)", path, ErrTooLarge, MaxMadefileBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	if info, err = f.Stat(); err != nil {
		return nil, false, err
	}
	if trusted != nil && !os.SameFile(trusted, info) {
		return nil, false, ErrChangedAfterCheck
	}
	regular = info.Mode().IsRegular()
	if !explicit && !regular {
		return nil, false, fmt.Errorf("%s is not a regular file", path)
	}
	source, err = readLimited(f, path)
	return source, regular, err
}

// ErrChangedAfterCheck is ReadMadefile's reason for a parent Madefile that is no
// longer the file checkParentMadefile checked.
var ErrChangedAfterCheck = errors.New("it was replaced after its owner and mode were checked")

// errIsDir is ReadMadefile's reason for an explicit path that is a directory.
var errIsDir = errors.New("is a directory")

// readLimited reads r to the end, or fails with ErrTooLarge once it passes
// MaxMadefileBytes.
func readLimited(r io.Reader, path string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxMadefileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxMadefileBytes {
		return nil, fmt.Errorf("%s %w (%d bytes)", path, ErrTooLarge, MaxMadefileBytes)
	}
	return b, nil
}

// ErrTooLarge is wrapped by ReadMadefile for a file over MaxMadefileBytes.
var ErrTooLarge = errors.New("exceeds maximum size")

// errNotRegular is CheckMadefile's reason for an existing path that is not a regular file.
var errNotRegular = errors.New("not a regular file")

// CheckMadefile returns nil if path is a regular file (following symlinks), otherwise
// the reason it cannot be used, without the path.
func CheckMadefile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if target, lerr := os.Readlink(path); lerr == nil {
				return fmt.Errorf("broken symlink to %s", target)
			}
		}
		return pathErrReason(err)
	}
	if !info.Mode().IsRegular() {
		return errNotRegular
	}
	return nil
}

// pathErrReason returns the error inside a *fs.PathError, dropping the operation
// and path, or err itself otherwise.
func pathErrReason(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
