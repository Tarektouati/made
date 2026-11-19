//go:build !windows

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// installVersion is the release served by the fake curl.
const installVersion = "1.2.3"

// curlStub stands in for curl: it logs each call, optionally sends @SIGNAL@ to the
// calling shell, answers the latest-release redirect with installVersion, and serves
// release files from @RELEASE@.
const curlStub = `#!/bin/sh
echo "$*" >>'@LOG@'
[ -z '@SIGNAL@' ] || { kill -@SIGNAL@ "$PPID"; exit 22; }
out= url=
while [ $# -gt 0 ]; do
	case $1 in
	-o) out=$2; shift ;;
	-w) shift ;;
	-*) ;;
	*) url=$1 ;;
	esac
	shift
done
case $url in
*/releases/latest) printf '%s' "${url%/latest}/tag/v@VERSION@"; exit 0 ;;
*/releases/download/v@VERSION@/*) ;;
*) exit 22 ;;
esac
[ -f '@RELEASE@'/"${url##*/}" ] || exit 22
cp '@RELEASE@'/"${url##*/}" "$out"
`

// installTools are the real commands install.sh (and curlStub) may run, besides gzip
// and a SHA-256 tool, which are linked separately.
var installTools = []string{"tar", "uname", "mkdir", "install", "awk", "cut", "mktemp", "rm", "cp"}

type installOpts struct {
	// omit lists commands (from installTools or gzip) left off PATH.
	omit        []string
	failingSudo bool
	unsetHome   bool
	// shadowMade puts a stub made on PATH, as an older install would be.
	shadowMade bool
	// stdin, when set, is fed to sh instead of running install.sh as a file.
	stdin []byte
	// script, when set, is run instead of install.sh.
	script string
	// shell names the shell that runs the script (default sh).
	shell string
	// signal, when set (e.g. INT), is sent by curl to the shell running install.sh.
	signal string
	// path is prepended to PATH.
	path string
	// noexecTmp wraps tar to strip the exec bit from the extracted made, so running it
	// from the temp dir fails as it would on a noexec TMPDIR.
	noexecTmp bool
	// uname, when set, replaces uname with a stub printing uname[0] for -s and
	// uname[1] for -m.
	uname [2]string
}

// unameStub stands in for uname, answering only -s and -m.
const unameStub = `#!/bin/sh
case $1 in
-s) echo '@S@' ;;
-m) echo '@M@' ;;
*) exit 1 ;;
esac
`

// tarNoexecStub runs the real tar, then makes the extracted made non-executable.
const tarNoexecStub = `#!/bin/sh
'@TAR@' "$@" || exit
while [ $# -gt 0 ]; do
	[ "$1" = -C ] && '@CHMOD@' a-x "$2/made"
	shift
done
`

type installResult struct {
	stdout, stderr  string
	code            int
	home, work, tmp string
	curlCalls       int
}

// releaseOpts alters the fake release written by customRelease.
type releaseOpts struct {
	// bin replaces the made binary in the archive.
	bin []byte
	// archive replaces the whole archive.
	archive []byte
	// checksums rewrites the checksums.txt line for the archive.
	checksums func(line string) string
	// os and arch name the archive's platform (default: the host's).
	os, arch string
}

// releaseDir writes a fake made release for the host platform (the archive holding the
// binary under test, and checksums.txt) and returns its directory.
func releaseDir(t *testing.T) string {
	t.Helper()
	return customRelease(t, releaseOpts{})
}

func customRelease(t *testing.T, opts releaseOpts) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("install.sh does not support %s", runtime.GOOS)
	}
	bin := opts.bin
	if bin == nil {
		var err error
		if bin, err = os.ReadFile(madeBin); err != nil {
			t.Fatal(err)
		}
	}
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
	data := buf.Bytes()
	if opts.archive != nil {
		data = opts.archive
	}
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if opts.os != "" {
		goos = opts.os
	}
	if opts.arch != "" {
		goarch = opts.arch
	}
	archive := fmt.Sprintf("made_%s_%s_%s.tar.gz", installVersion, goos, goarch)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, archive), data, 0o644); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), archive)
	if opts.checksums != nil {
		line = opts.checksums(line)
	}
	writeFile(t, filepath.Join(dir, "checksums.txt"), line)
	return dir
}

// linkTool symlinks the real command name into bin, skipping the test when it is missing.
func linkTool(t *testing.T, bin, name string) {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found on PATH; skipping", name)
	}
	if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
		t.Fatal(err)
	}
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runInstall runs install.sh offline against release, with PATH holding only stubs and
// the tools it needs, HOME and the working directory set to fresh TempDirs, and env
// (KEY=VALUE) appended.
func runInstall(t *testing.T, release string, opts installOpts, env ...string) installResult {
	t.Helper()
	shell := opts.shell
	if shell == "" {
		shell = "sh"
	}
	sh, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s not found on PATH; skipping", shell)
	}
	script := opts.script
	if script == "" {
		if script, err = filepath.Abs(filepath.Join("..", "install.sh")); err != nil {
			t.Fatal(err)
		}
	}

	bin := t.TempDir()
	for _, name := range append(installTools[:len(installTools):len(installTools)], "gzip") {
		stubbed := (name == "tar" && opts.noexecTmp) || (name == "uname" && opts.uname != [2]string{})
		if !slices.Contains(opts.omit, name) && !stubbed {
			linkTool(t, bin, name)
		}
	}
	if opts.uname != [2]string{} {
		writeExec(t, filepath.Join(bin, "uname"), strings.NewReplacer(
			"@S@", opts.uname[0], "@M@", opts.uname[1],
		).Replace(unameStub))
	}
	if opts.noexecTmp {
		var tarPath, chmodPath string
		if tarPath, err = exec.LookPath("tar"); err != nil {
			t.Skip("tar not found on PATH; skipping")
		}
		if chmodPath, err = exec.LookPath("chmod"); err != nil {
			t.Skip("chmod not found on PATH; skipping")
		}
		writeExec(t, filepath.Join(bin, "tar"), strings.NewReplacer(
			"@TAR@", tarPath, "@CHMOD@", chmodPath,
		).Replace(tarNoexecStub))
	}
	switch {
	case lookPath("sha256sum"):
		linkTool(t, bin, "sha256sum")
	case lookPath("shasum"):
		linkTool(t, bin, "shasum")
	default:
		t.Skip("no sha256sum or shasum on PATH; skipping")
	}
	log := filepath.Join(t.TempDir(), "curl.log")
	writeExec(t, filepath.Join(bin, "curl"), strings.NewReplacer(
		"@LOG@", log, "@RELEASE@", release, "@VERSION@", installVersion, "@SIGNAL@", opts.signal,
	).Replace(curlStub))
	if opts.failingSudo {
		writeExec(t, filepath.Join(bin, "sudo"), "#!/bin/sh\nexit 1\n")
	}
	if opts.shadowMade {
		writeExec(t, filepath.Join(bin, "made"), "#!/bin/sh\necho made v0.0.1\n")
	}

	res := installResult{home: t.TempDir(), work: t.TempDir(), tmp: t.TempDir()}
	path := bin
	if opts.path != "" {
		path = opts.path + ":" + bin
	}
	cmdEnv := []string{"PATH=" + path, "TMPDIR=" + res.tmp}
	if !opts.unsetHome {
		cmdEnv = append(cmdEnv, "HOME="+res.home)
	}
	var args []string
	if opts.stdin == nil {
		args = []string{script}
	}
	cmd := exec.CommandContext(t.Context(), sh, args...)
	cmd.Dir = res.work
	cmd.Env = append(cmdEnv, env...)
	if opts.stdin != nil {
		cmd.Stdin = bytes.NewReader(opts.stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("running install.sh: %v", err)
		}
		res.code = ee.ExitCode()
	}
	res.stdout, res.stderr = out.String(), errb.String()
	if data, err := os.ReadFile(log); err == nil {
		res.curlCalls = strings.Count(string(data), "\n")
	}
	return res
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// requireInstalled fails the test unless path is an executable made binary.
func requireInstalled(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("made not installed: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("%s mode = %v, want executable", path, fi.Mode())
	}
}

func requireNotInstalled(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s exists, want nothing installed", path)
	}
}

type installCase struct {
	name string
	// env returns the extra environment for a run that should install into dir.
	env        func(t *testing.T, dir string) []string
	opts       installOpts
	wantCode   int
	wantStdout string
	wantStderr []string
	noCurl     bool
}

func TestInstallScript(t *testing.T) {
	release := releaseDir(t)
	cases := []installCase{
		{
			name: "explicit dir",
			env: func(_ *testing.T, dir string) []string {
				return []string{"MADE_VERSION=v1.2.3", "MADE_INSTALL_DIR=" + dir}
			},
			wantStdout: "Installed ",
		},
		{
			name:       "latest when unset",
			env:        func(_ *testing.T, dir string) []string { return []string{"MADE_INSTALL_DIR=" + dir} },
			wantStdout: "Downloading made v1.2.3",
		},
		{
			name: "latest keyword",
			env: func(_ *testing.T, dir string) []string {
				return []string{"MADE_VERSION=latest", "MADE_INSTALL_DIR=" + dir}
			},
			wantStdout: "Downloading made v1.2.3",
		},
		{
			name: "version without v",
			env: func(_ *testing.T, dir string) []string {
				return []string{"MADE_VERSION=1.2.3", "MADE_INSTALL_DIR=" + dir}
			},
			wantStdout: "Downloading made v1.2.3",
		},
		{
			name: "path in tag",
			env: func(_ *testing.T, dir string) []string {
				return []string{"MADE_VERSION=v1/../x", "MADE_INSTALL_DIR=" + dir}
			},
			wantCode:   1,
			wantStderr: []string{"invalid release tag 'v1/../x'"},
			noCurl:     true,
		},
		{
			name: "non-version tag",
			env: func(_ *testing.T, dir string) []string {
				return []string{"MADE_VERSION=nope", "MADE_INSTALL_DIR=" + dir}
			},
			wantCode:   1,
			wantStderr: []string{"could not determine a release tag"},
			noCurl:     true,
		},
		{
			name:       "unexpanded tilde",
			env:        func(_ *testing.T, _ string) []string { return []string{"MADE_INSTALL_DIR=~/bin"} },
			wantCode:   1,
			wantStderr: []string{"which was not expanded"},
			noCurl:     true,
		},
		{
			name: "unwritable dir",
			env: func(t *testing.T, dir string) []string {
				if os.Geteuid() == 0 {
					t.Skip("root can write to any directory; skipping")
				}
				mkdir(t, dir)
				if err := os.Chmod(dir, 0o555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
				return []string{"MADE_VERSION=v1.2.3", "MADE_INSTALL_DIR=" + dir}
			},
			wantCode:   1,
			wantStderr: []string{"cannot install to ", "Permission denied"},
		},
		{
			name: "dir under a file",
			env: func(t *testing.T, dir string) []string {
				file := filepath.Join(filepath.Dir(dir), "file")
				writeFile(t, file, "")
				return []string{"MADE_VERSION=v1.2.3", "MADE_INSTALL_DIR=" + filepath.Join(file, "bin")}
			},
			wantCode:   1,
			wantStderr: []string{"cannot install to ", "Not a directory"},
		},
		{
			name:       "relative dir",
			env:        func(_ *testing.T, _ string) []string { return []string{"MADE_INSTALL_DIR=bin"} },
			wantCode:   1,
			wantStderr: []string{"MADE_INSTALL_DIR must be an absolute path (got 'bin')"},
			noCurl:     true,
		},
		{
			name:       "shadowed by an older made",
			env:        func(_ *testing.T, dir string) []string { return []string{"MADE_INSTALL_DIR=" + dir} },
			opts:       installOpts{shadowMade: true},
			wantStdout: "takes precedence over ",
		},
		{
			name: "uppercase V",
			env: func(_ *testing.T, dir string) []string {
				return []string{"MADE_VERSION=V1.2.3", "MADE_INSTALL_DIR=" + dir}
			},
			wantStdout: "Downloading made v1.2.3",
		},
	}
	for _, tool := range []string{"gzip", "awk", "cut", "mktemp", "install"} {
		cases = append(cases, installCase{
			name:       "missing " + tool,
			env:        func(_ *testing.T, dir string) []string { return []string{"MADE_INSTALL_DIR=" + dir} },
			opts:       installOpts{omit: []string{tool}},
			wantCode:   1,
			wantStderr: []string{"made-install: required command not found: " + tool},
			noCurl:     true,
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bin")
			res := runInstall(t, release, tc.opts, tc.env(t, dir)...)
			if res.code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, tc.wantCode, res.stdout, res.stderr)
			}
			if !strings.Contains(res.stdout, tc.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", res.stdout, tc.wantStdout)
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(res.stderr, want) {
					t.Errorf("stderr = %q, want it to contain %q", res.stderr, want)
				}
			}
			if strings.Contains(res.stdout+res.stderr, "vlatest") {
				t.Errorf("output mentions vlatest:\nstdout: %s\nstderr: %s", res.stdout, res.stderr)
			}
			if tc.noCurl && res.curlCalls != 0 {
				t.Errorf("curl called %d times, want 0", res.curlCalls)
			}
			if tc.wantCode == 0 {
				requireInstalled(t, filepath.Join(dir, "made"))
			} else {
				requireNotInstalled(t, filepath.Join(dir, "made"))
			}
			requireNotInstalled(t, filepath.Join(res.work, "~"))
		})
	}
}

// TestInstallScriptDefaultDir covers the default target, so it runs only where
// /usr/local/bin is not writable and can never be installed into.
func TestInstallScriptDefaultDir(t *testing.T) {
	const wOK = 0x2
	if os.Geteuid() == 0 || syscall.Access("/usr/local/bin", wOK) == nil {
		t.Skip("/usr/local/bin is writable; skipping")
	}
	release := releaseDir(t)
	cases := []struct {
		name       string
		opts       installOpts
		wantCode   int
		wantStderr string
	}{
		{name: "failing sudo", opts: installOpts{failingSudo: true}, wantStderr: "sudo failed; falling back"},
		{name: "no sudo", wantStderr: "installing to "},
		{name: "no sudo and no HOME", opts: installOpts{unsetHome: true}, wantCode: 1, wantStderr: "HOME is not set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runInstall(t, release, tc.opts)
			if res.code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, tc.wantCode, res.stdout, res.stderr)
			}
			if !strings.Contains(res.stderr, tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", res.stderr, tc.wantStderr)
			}
			bin := filepath.Join(res.home, ".local", "bin", "made")
			if tc.wantCode == 0 {
				requireInstalled(t, bin)
			} else {
				requireNotInstalled(t, bin)
			}
		})
	}
}

func TestInstallScriptTruncated(t *testing.T) {
	release := releaseDir(t)
	script, err := os.ReadFile(filepath.Join("..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	half := script[:len(script)/2]
	if bytes.Contains(half, []byte(`main "$@"`)) {
		t.Fatal("truncated script still calls main")
	}
	partial := filepath.Join(t.TempDir(), "install.sh")
	writeFile(t, partial, string(half))
	cases := []struct {
		name string
		opts installOpts
	}{
		{name: "file", opts: installOpts{script: partial}},
		{name: "stdin", opts: installOpts{stdin: half}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bin")
			res := runInstall(t, release, tc.opts, "MADE_INSTALL_DIR="+dir)
			if res.curlCalls != 0 {
				t.Errorf("curl called %d times, want 0", res.curlCalls)
			}
			requireNotInstalled(t, filepath.Join(dir, "made"))
		})
	}
}

// TestInstallScriptSignal runs under dash, which skips the EXIT trap when killed by a
// signal, and checks the temp dir is still removed.
func TestInstallScriptSignal(t *testing.T) {
	release := releaseDir(t)
	cases := []struct {
		signal   string
		wantCode int
	}{
		{signal: "INT", wantCode: 130},
		{signal: "TERM", wantCode: 143},
		{signal: "HUP", wantCode: 129},
	}
	for _, tc := range cases {
		t.Run(tc.signal, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bin")
			res := runInstall(t, release, installOpts{shell: "dash", signal: tc.signal},
				"MADE_VERSION=v1.2.3", "MADE_INSTALL_DIR="+dir)
			if res.code != tc.wantCode {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, tc.wantCode, res.stdout, res.stderr)
			}
			entries, err := os.ReadDir(res.tmp)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("TMPDIR holds %d entries after the signal, want 0 (first: %s)", len(entries), entries[0].Name())
			}
			requireNotInstalled(t, filepath.Join(dir, "made"))
		})
	}
}

func TestInstallScriptRelease(t *testing.T) {
	cases := []struct {
		name    string
		release releaseOpts
		opts    installOpts
		// existing, when set, is a made already installed in the target dir.
		existing string
		// installedAnyway marks a failing run that still leaves made in the target dir.
		installedAnyway bool
		wantCode        int
		wantStderr      []string
	}{
		{
			name:    "CRLF checksums",
			release: releaseOpts{checksums: func(line string) string { return strings.ReplaceAll(line, "\n", "\r\n") }},
		},
		{
			name: "uppercase checksum",
			release: releaseOpts{checksums: func(line string) string {
				sum, rest, _ := strings.Cut(line, " ")
				return strings.ToUpper(sum) + " " + rest
			}},
		},
		{
			name: "checksum mismatch",
			release: releaseOpts{checksums: func(line string) string {
				_, rest, _ := strings.Cut(line, " ")
				return fmt.Sprintf("%x %s", sha256.Sum256([]byte("tampered")), rest)
			}},
			wantCode:   1,
			wantStderr: []string{"made-install: checksum mismatch for made_"},
		},
		{
			name: "no checksum entry",
			release: releaseOpts{checksums: func(line string) string {
				sum, _, _ := strings.Cut(line, " ")
				return sum + "  made_" + installVersion + "_plan9_amd64.tar.gz\n"
			}},
			wantCode:   1,
			wantStderr: []string{"made-install: no checksum found for made_"},
		},
		{
			name:       "corrupt archive",
			release:    releaseOpts{archive: []byte("not a tarball")},
			wantCode:   1,
			wantStderr: []string{"made-install: cannot extract made from made_"},
		},
		{
			name:       "binary fails to run",
			release:    releaseOpts{bin: []byte("#!/bin/sh\necho boom >&2\nexit 3\n")},
			wantCode:   1,
			wantStderr: []string{"made-install: ", "failed to run", "boom"},
		},
		{
			name:       "binary fails to run over a working made",
			release:    releaseOpts{bin: []byte("#!/bin/sh\necho boom >&2\nexit 3\n")},
			existing:   "#!/bin/sh\necho made v0.0.1\n",
			wantCode:   1,
			wantStderr: []string{"made-install: ", "failed to run", "boom"},
		},
		{
			name: "noexec TMPDIR",
			opts: installOpts{noexecTmp: true},
		},
		{
			name: "noexec TMPDIR under dash",
			opts: installOpts{noexecTmp: true, shell: "dash"},
		},
		{
			name:            "noexec TMPDIR and binary fails to run",
			release:         releaseOpts{bin: []byte("#!/bin/sh\necho boom >&2\nexit 3\n")},
			opts:            installOpts{noexecTmp: true},
			installedAnyway: true,
			wantCode:        1,
			wantStderr:      []string{"made-install: installed ", "failed to run", "boom"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			release := customRelease(t, tc.release)
			dir := filepath.Join(t.TempDir(), "bin")
			if tc.existing != "" {
				mkdir(t, dir)
				writeExec(t, filepath.Join(dir, "made"), tc.existing)
			}
			res := runInstall(t, release, tc.opts, "MADE_INSTALL_DIR="+dir)
			if tc.existing != "" {
				got, err := os.ReadFile(filepath.Join(dir, "made"))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.existing {
					t.Errorf("existing made was replaced: %q", got)
				}
			}
			if res.code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, tc.wantCode, res.stdout, res.stderr)
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(res.stderr, want) {
					t.Errorf("stderr = %q, want it to contain %q", res.stderr, want)
				}
			}
			if tc.wantCode == 0 {
				if !strings.Contains(res.stdout, "Installed made ") {
					t.Errorf("stdout = %q, want it to report the installed version", res.stdout)
				}
				requireInstalled(t, filepath.Join(dir, "made"))
			} else {
				if strings.Contains(res.stdout, "Installed ") {
					t.Errorf("stdout = %q, want no success message", res.stdout)
				}
				if tc.existing == "" && !tc.installedAnyway {
					requireNotInstalled(t, filepath.Join(dir, "made"))
				}
			}
		})
	}
}

func TestInstallScriptUname(t *testing.T) {
	cases := []struct {
		name  string
		uname [2]string
		// os and arch name the platform install.sh should pick, unset when it must refuse.
		os, arch   string
		wantStderr string
	}{
		{name: "Linux x86_64", uname: [2]string{"Linux", "x86_64"}, os: "linux", arch: "amd64"},
		{name: "Linux aarch64", uname: [2]string{"Linux", "aarch64"}, os: "linux", arch: "arm64"},
		{name: "Darwin arm64", uname: [2]string{"Darwin", "arm64"}, os: "darwin", arch: "arm64"},
		{name: "Darwin amd64", uname: [2]string{"Darwin", "amd64"}, os: "darwin", arch: "amd64"},
		{name: "unsupported OS", uname: [2]string{"FreeBSD", "amd64"}, wantStderr: "made-install: unsupported OS: FreeBSD"},
		{name: "unsupported arch", uname: [2]string{"Linux", "i686"}, wantStderr: "made-install: unsupported architecture: i686"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			release := customRelease(t, releaseOpts{os: tc.os, arch: tc.arch})
			dir := filepath.Join(t.TempDir(), "bin")
			res := runInstall(t, release, installOpts{uname: tc.uname}, "MADE_VERSION=v1.2.3", "MADE_INSTALL_DIR="+dir)
			if tc.wantStderr != "" {
				if res.code != 1 {
					t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
				}
				if !strings.Contains(res.stderr, tc.wantStderr) {
					t.Errorf("stderr = %q, want it to contain %q", res.stderr, tc.wantStderr)
				}
				if res.curlCalls != 0 {
					t.Errorf("curl called %d times, want 0", res.curlCalls)
				}
				requireNotInstalled(t, filepath.Join(dir, "made"))
				return
			}
			if res.code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
			}
			if want := fmt.Sprintf("Downloading made v1.2.3 (%s/%s)", tc.os, tc.arch); !strings.Contains(res.stdout, want) {
				t.Errorf("stdout = %q, want it to contain %q", res.stdout, want)
			}
			requireInstalled(t, filepath.Join(dir, "made"))
		})
	}
}

func TestInstallScriptTrailingSlash(t *testing.T) {
	release := releaseDir(t)
	dir := filepath.Join(t.TempDir(), "bin")
	res := runInstall(t, release, installOpts{path: dir}, "MADE_INSTALL_DIR="+dir+"//")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	if want := " to " + dir + "/made\n"; !strings.Contains(res.stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", res.stdout, want)
	}
	if strings.Contains(res.stdout, "Note:") {
		t.Errorf("stdout = %q, want no PATH note", res.stdout)
	}
	requireInstalled(t, filepath.Join(dir, "made"))
}
