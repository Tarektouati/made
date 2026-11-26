package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Exit codes of made self-update besides 0 (updated, already current, dry run) and 2
// (usage error).
const (
	// ExitFailure: a download, checksum, extraction, verification or replace failure.
	ExitFailure = 1
	// ExitNotSelfUpdatable: the install belongs to Homebrew or go install, is a
	// development build, or has no release archive; the command to use is printed.
	ExitNotSelfUpdatable = 3
	// ExitNotWritable: the binary's directory cannot be written.
	ExitNotWritable = 4
)

// Size limits for downloads; a release archive is a few MiB.
const (
	maxChecksumsBytes = 1 << 20
	maxArchiveBytes   = 64 << 20
)

// maxBinaryBytes is the largest made binary extracted from an archive. Tests lower it.
var maxBinaryBytes int64 = 64 << 20

// versionTimeout bounds the run of the downloaded binary's --version, and
// versionWaitDelay how long its output may stay open once it has exited or been
// killed.
const (
	versionTimeout   = 10 * time.Second
	versionWaitDelay = 2 * time.Second
)

// tempPrefix starts the name of the new binary while it is written next to the old one.
const tempPrefix = ".made-update-"

// Error is a failed update. Nothing has been replaced when it is returned.
type Error struct {
	// Code is ExitFailure or ExitNotWritable.
	Code int
	Err  error
	// Hint, when set, says what to do instead.
	Hint string
}

func (e *Error) Error() string { return e.Err.Error() }

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

func failure(format string, args ...any) *Error {
	return &Error{Code: ExitFailure, Err: fmt.Errorf(format, args...)}
}

// notWritable is the error for a directory that cannot hold the new binary.
func notWritable(dir string, err error) *Error {
	return &Error{
		Code: ExitNotWritable,
		Err:  fmt.Errorf("cannot write to %s: %w", dir, pathReason(err)),
		Hint: "run sudo made self-update, or reinstall with: " + installScript,
	}
}

// pathReason returns the error inside a *fs.PathError or *os.LinkError, dropping
// the operation and paths.
func pathReason(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// isPermission reports whether err means the directory cannot be written.
func isPermission(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// Updater replaces the running binary, installed manually (see Detect), with a
// release.
type Updater struct {
	Client Client
	// Exe is the binary to replace; a symlink is followed and its target replaced.
	Exe string
	// Current is the release version Exe reports.
	Current string
	// Target is the release to install; "" means the latest. Unlike the latest, it
	// may be older than Current.
	Target string
	// GOOS and GOARCH pick the release archive.
	GOOS, GOARCH string
	// DryRun reports what would happen without downloading or writing anything.
	DryRun bool
	// Stdout receives the progress messages.
	Stdout io.Writer
}

// Run installs the target release in place of Exe. The archive is checked against
// checksums.txt from the same release, which catches a corrupted download but not
// a tampered release. The new binary is written next to Exe, run with --version,
// and only then renamed over Exe, so a failure leaves the old binary in place. The
// returned error is an *Error.
func (u Updater) Run(ctx context.Context) error {
	return u.run(ctx, os.Stat)
}

func (u Updater) run(ctx context.Context, stat func(string) (os.FileInfo, error)) error {
	if !IsRelease(u.Current) {
		return failure("cannot update from %s, which is not a release version", u.Current)
	}
	target, err := u.target(ctx)
	if err != nil {
		return err
	}
	if target == "" {
		return nil
	}
	asset, ok := AssetName(target, u.GOOS, u.GOARCH)
	if !ok {
		return failure("no release archive is published for %s/%s", u.GOOS, u.GOARCH)
	}
	exe, err := filepath.EvalSymlinks(u.Exe)
	if err != nil {
		return failure("cannot find the running binary: %w", err)
	}
	dir := filepath.Dir(exe)
	base := u.Client.base()
	archiveURL := ReleaseURL(base, target, asset)

	if u.DryRun {
		fmt.Fprintf(u.Stdout, "made: would update %s -> %s\n", u.Current, target)
		fmt.Fprintf(u.Stdout, "made: would download %s\n", archiveURL)
		if writable(dir) {
			fmt.Fprintf(u.Stdout, "made: would replace %s (directory writable)\n", exe)
		} else {
			fmt.Fprintf(u.Stdout, "made: would replace %s, but its directory is not writable: run sudo made self-update, or reinstall with: %s\n", exe, installScript)
		}
		return nil
	}

	// Fail fast for the common unwritable /usr/local/bin. Creating the temporary file
	// below is the check that counts.
	if !writable(dir) {
		return notWritable(dir, fs.ErrPermission)
	}
	info, err := stat(exe)
	if err != nil {
		return failure("cannot read the running binary's permissions: %w", err)
	}
	sumsURL := ReleaseURL(base, target, ChecksumsName)
	sums, err := u.Client.get(ctx, sumsURL, maxChecksumsBytes)
	if err != nil {
		return failure("download failed: %s: %w", sumsURL, err)
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return failure("%w", err)
	}
	archive, err := u.Client.get(ctx, archiveURL, maxArchiveBytes)
	if err != nil {
		return failure("download failed: %s: %w", archiveURL, err)
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return failure("checksum mismatch for %s", asset)
	}
	bin, err := extractMade(archive)
	if err != nil {
		return failure("cannot extract made from %s: %w", asset, err)
	}

	tmp, err := writeTemp(dir, bin, info)
	if err != nil {
		if isPermission(err) {
			return notWritable(dir, err)
		}
		return failure("cannot write the new binary to %s: %w", dir, pathReason(err))
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	if err := checkVersion(ctx, tmp, target); err != nil {
		return failure("downloaded made failed to run, so nothing was replaced: %w", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		if isPermission(err) {
			return notWritable(dir, err)
		}
		return failure("cannot replace %s: %w", exe, pathReason(err))
	}
	renamed = true
	// The new binary is in place; a failure to persist the rename is not reported,
	// as it would wrongly say the update failed.
	syncDir(dir)
	fmt.Fprintf(u.Stdout, "made: updated %s -> %s\n", u.Current, target)
	return nil
}

// target returns the release to install, or "" after printing why there is nothing
// to do: Current is already the requested or the latest release, or is newer than
// the latest (it never downgrades unless Target asks for it). A named Target must be
// a release: a pre-release would install as a development build, which Detect then
// keeps made self-update from replacing.
func (u Updater) target(ctx context.Context) (string, error) {
	if u.Target != "" {
		target, err := NormalizeTag(u.Target)
		if err != nil {
			return "", failure("%w", err)
		}
		if !IsRelease(target) {
			return "", failure("%s is not a release version; give a release such as v1.2.3", target)
		}
		if Compare(target, u.Current) == 0 {
			fmt.Fprintf(u.Stdout, "made: %s is already installed\n", u.Current)
			return "", nil
		}
		return target, nil
	}
	latest, err := u.Client.LatestTag(ctx)
	if err != nil {
		return "", failure("cannot resolve the latest release: %w", err)
	}
	if !IsRelease(latest) {
		return "", failure("the latest release tag %s is not a release version", latest)
	}
	switch c := Compare(latest, u.Current); {
	case c == 0:
		fmt.Fprintf(u.Stdout, "made: %s is the latest version\n", u.Current)
		return "", nil
	case c < 0:
		fmt.Fprintf(u.Stdout, "made: %s is newer than the latest release %s; not downgrading (run made self-update %s to install it)\n", u.Current, latest, latest)
		return "", nil
	}
	return latest, nil
}

// checksumFor returns the lowercased SHA-256 listed for asset in a checksums.txt, as
// install.sh's awk does: the first line whose second field is asset, after removing
// a trailing carriage return.
func checksumFor(sums []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(strings.TrimSuffix(line, "\r"))
		if len(f) >= 2 && f[1] == asset {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum found for %s", asset)
}

// extractMade returns the regular file named made at the top of a .tar.gz archive.
func extractMade(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the archive has no made binary")
		}
		if err != nil {
			return nil, err
		}
		if h.Name != "made" && h.Name != "./made" {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return nil, errors.New("made in the archive is not a regular file")
		}
		if h.Size > maxBinaryBytes {
			return nil, fmt.Errorf("made in the archive is larger than %d bytes", maxBinaryBytes)
		}
		return io.ReadAll(io.LimitReader(tr, maxBinaryBytes))
	}
}

// writeTemp writes bin to a new file in dir with the original permissions and,
// when permitted, group, and returns its path. The file is next to the binary it
// replaces, so the rename is atomic. It is flushed to disk before it is closed,
// so a crash after the rename cannot leave an empty or partly written made.
func writeTemp(dir string, bin []byte, info fs.FileInfo) (string, error) {
	f, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return "", err
	}
	_, err = f.Write(bin)
	if err == nil {
		preserveGroup(f, info)
		err = f.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// checkVersion runs path --version and checks that it reports made tag.
func checkVersion(ctx context.Context, path, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	// Without it, a process the binary leaves behind holding the output pipe would
	// keep CombinedOutput waiting past the timeout.
	cmd.WaitDelay = versionWaitDelay
	out, err := cmd.CombinedOutput()
	got := strings.TrimSpace(string(out))
	if len(got) > 200 {
		got = got[:200] + "..."
	}
	if err != nil {
		if got != "" {
			return fmt.Errorf("%w: %s", err, got)
		}
		return err
	}
	if want := "made " + tag; got != want {
		return fmt.Errorf("it reports %q, want %q", got, want)
	}
	return nil
}
