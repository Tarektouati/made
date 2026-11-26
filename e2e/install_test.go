//go:build !windows

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// installVersion is the release served by the fake curl.
const installVersion = "1.2.3"

// curlStub stands in for curl: it answers install.sh's -V probe (unlogged, so the
// log holds downloads only), rejects --tlsv1.2 as a curl older than 7.34 does when
// @OLD@ is set, logs each call and each -o path, optionally sends @SIGNAL@ to the
// calling shell, answers the latest-release redirect with installVersion, and serves
// release files from @RELEASE@.
const curlStub = `#!/bin/sh
case " $* " in
*" --tlsv1.2 "*) [ -z '@OLD@' ] || { echo 'curl: option --tlsv1.2: is unknown' >&2; exit 2; } ;;
esac
case " $* " in
*" -V "*) exit 0 ;;
esac
echo "$*" >>'@LOG@'
out= url=
while [ $# -gt 0 ]; do
	case $1 in
	-o) out=$2; shift ;;
	-w | --proto) shift ;;
	-*) ;;
	*) url=$1 ;;
	esac
	shift
done
[ -z "$out" ] || printf 'out=%s\n' "$out" >>'@OUTLOG@'
[ -z '@SIGNAL@' ] || { kill -@SIGNAL@ "$PPID"; exit 22; }
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
	// workingSudo puts a sudo on PATH that logs its arguments and exits 0 without
	// running them.
	workingSudo bool
	// sha256Tool picks the SHA-256 tool on PATH: "" for sha256sum when available,
	// "shasum" for shasum only, "none" for neither.
	sha256Tool string
	unsetHome  bool
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
	// oldCurl makes the curl stub reject --tlsv1.2, as curl before 7.34 does.
	oldCurl bool
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
	// curlLog holds the arguments of each curl call, one line per call.
	curlLog string
	// curlOuts holds one out=PATH line per curl -o call.
	curlOuts string
	// sudoLog holds one line per call to the workingSudo stub.
	sudoLog string
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
	// #nosec G306 -- Non-sensitive fixture inside a private temporary directory.
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
	// #nosec G306 -- Executable fixture inside a private temporary directory.
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
	switch opts.sha256Tool {
	case "none":
	case "shasum":
		linkTool(t, bin, "shasum")
	case "":
		switch {
		case lookPath("sha256sum"):
			linkTool(t, bin, "sha256sum")
		case lookPath("shasum"):
			linkTool(t, bin, "shasum")
		default:
			t.Skip("no sha256sum or shasum on PATH; skipping")
		}
	default:
		t.Fatalf("sha256Tool = %q, want \"\", \"shasum\" or \"none\"", opts.sha256Tool)
	}
	log, outLog := writeCurlStub(t, bin, release, opts.signal, opts.oldCurl)
	if opts.failingSudo {
		writeExec(t, filepath.Join(bin, "sudo"), "#!/bin/sh\nexit 1\n")
	}
	sudoLog := filepath.Join(t.TempDir(), "sudo.log")
	if opts.workingSudo {
		writeExec(t, filepath.Join(bin, "sudo"), fmt.Sprintf("#!/bin/sh\necho \"$*\" >>'%s'\n", sudoLog))
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
	// #nosec G204 -- Runs the repository installer with a controlled PATH and local release fixtures.
	cmd := exec.CommandContext(t.Context(), sh, args...)
	cmd.Dir = res.work
	cmdEnv = append(cmdEnv, env...)
	cmd.Env = cmdEnv
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
	// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
	if data, err := os.ReadFile(log); err == nil {
		res.curlLog = string(data)
		res.curlCalls = strings.Count(res.curlLog, "\n")
	}
	// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
	if data, err := os.ReadFile(outLog); err == nil {
		res.curlOuts = string(data)
	}
	// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
	if data, err := os.ReadFile(sudoLog); err == nil {
		res.sudoLog = string(data)
	}
	return res
}

// writeCurlStub writes curlStub as bin/curl, serving release, sending signal (if set)
// and rejecting --tlsv1.2 when old is set, and returns the files it logs its calls and
// its -o paths to.
func writeCurlStub(t *testing.T, bin, release, signal string, old bool) (log, outLog string) {
	t.Helper()
	dir := t.TempDir()
	log, outLog = filepath.Join(dir, "curl.log"), filepath.Join(dir, "curl-out.log")
	writeExec(t, filepath.Join(bin, "curl"), strings.NewReplacer(
		"@LOG@", log, "@OUTLOG@", outLog, "@RELEASE@", release, "@VERSION@", installVersion, "@SIGNAL@", signal,
		"@OLD@", map[bool]string{true: "1"}[old],
	).Replace(curlStub))
	return log, outLog
}

// scriptTmp returns the temp dir install.sh downloaded into, taken from the first
// curl -o path other than /dev/null. It is not always under TMPDIR: macOS mktemp -d
// ignores it.
func scriptTmp(t *testing.T, res installResult) string {
	t.Helper()
	for line := range strings.Lines(res.curlOuts) {
		if out := strings.TrimSuffix(strings.TrimPrefix(line, "out="), "\n"); out != "/dev/null" {
			return filepath.Dir(out)
		}
	}
	t.Fatalf("no curl -o path other than /dev/null in %q", res.curlOuts)
	return ""
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func TestScriptTmpWithSpace(t *testing.T) {
	bin := t.TempDir()
	_, outLog := writeCurlStub(t, bin, t.TempDir(), "", false)
	tmp := filepath.Join(t.TempDir(), "tmp dir")
	archive := "made_" + installVersion + "_linux_amd64.tar.gz"
	url := "https://github.com/Tarektouati/made/releases/download/v" + installVersion + "/" + archive
	// The release is empty, so curl exits 22 after logging the call.
	// #nosec G204 -- Runs the test curl fixture to check output paths containing spaces.
	_ = exec.CommandContext(t.Context(), filepath.Join(bin, "curl"), "-fsSL", "-o", filepath.Join(tmp, archive), url).Run()
	// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
	data, err := os.ReadFile(outLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := scriptTmp(t, installResult{curlOuts: string(data)}); got != tmp {
		t.Errorf("scriptTmp = %q, want %q", got, tmp)
	}
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

// requireHTTPSOnly fails the test unless every logged curl call skips ~/.curlrc and
// restricts itself to HTTPS with TLS 1.2 or later.
func requireHTTPSOnly(t *testing.T, curlLog string) {
	t.Helper()
	const want = "-q --proto =https --tlsv1.2 -fsSL "
	for line := range strings.Lines(curlLog) {
		if !strings.HasPrefix(line, want) {
			t.Errorf("curl called with %q, want it to start with %q", strings.TrimSuffix(line, "\n"), want)
		}
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
				// #nosec G302 -- The read-only directory is required to test an unwritable installation.
				if err := os.Chmod(dir, 0o555); err != nil {
					t.Fatal(err)
				}
				// #nosec G302 -- Restore directory search/write access for temporary-file cleanup.
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
		{
			name:       "missing sha256 tool",
			env:        func(_ *testing.T, dir string) []string { return []string{"MADE_INSTALL_DIR=" + dir} },
			opts:       installOpts{sha256Tool: "none"},
			wantCode:   1,
			wantStderr: []string{"made-install: required command not found: sha256sum or shasum"},
			noCurl:     true,
		},
		{
			name:       "curl older than 7.34",
			env:        func(_ *testing.T, dir string) []string { return []string{"MADE_INSTALL_DIR=" + dir} },
			opts:       installOpts{oldCurl: true},
			wantCode:   1,
			wantStderr: []string{"made-install: curl 7.34 or later is required (needs --tlsv1.2 and --proto)"},
			noCurl:     true,
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
			requireHTTPSOnly(t, res.curlLog)
			if tc.wantCode == 0 {
				requireInstalled(t, filepath.Join(dir, "made"))
			} else {
				requireNotInstalled(t, filepath.Join(dir, "made"))
			}
			requireNotInstalled(t, filepath.Join(res.work, "~"))
		})
	}
}

// skipUnlessDefaultDirUnwritable skips tests of the default target where
// /usr/local/bin is writable, so they can never install into it.
func skipUnlessDefaultDirUnwritable(t *testing.T) {
	t.Helper()
	const wOK = 0x2
	if os.Geteuid() == 0 || syscall.Access("/usr/local/bin", wOK) == nil {
		t.Skipf("/usr/local/bin is writable (euid %d); skipping", os.Geteuid())
	}
}

func TestInstallScriptDefaultDir(t *testing.T) {
	skipUnlessDefaultDirUnwritable(t)
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

// TestInstallScriptSudo checks the commands run through a working sudo. The sudo
// stub runs nothing, so the check after installing finds no /usr/local/bin/made
// and the run fails there.
func TestInstallScriptSudo(t *testing.T) {
	skipUnlessDefaultDirUnwritable(t)
	if _, err := os.Lstat("/usr/local/bin/made"); err == nil {
		t.Skip("/usr/local/bin/made exists; skipping")
	}
	res := runInstall(t, releaseDir(t), installOpts{workingSudo: true})
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	if want := "made-install: installed /usr/local/bin/made, but it failed to run"; !strings.Contains(res.stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", res.stderr, want)
	}
	if strings.Contains(res.stderr, "sudo failed") {
		t.Errorf("stderr = %q, want no sudo fallback", res.stderr)
	}
	want := "mkdir -p /usr/local/bin\ninstall -m 0755 " + scriptTmp(t, res) + "/made /usr/local/bin/made\n"
	if res.sudoLog != want {
		t.Errorf("sudo calls = %q, want %q", res.sudoLog, want)
	}
	requireNotInstalled(t, filepath.Join(res.home, ".local", "bin", "made"))
}

func TestInstallScriptTruncated(t *testing.T) {
	release := releaseDir(t)
	script, afterMain := readInstallScript(t)
	cuts := []struct {
		name string
		data []byte
		// complete marks a cut that parses, so the shell must exit 0.
		complete bool
	}{
		{name: "half", data: script[:len(script)/2]},
		{name: "after main", data: afterMain, complete: true},
	}
	for _, cut := range cuts {
		if bytes.Contains(cut.data, []byte(`main "$@"`)) {
			t.Fatalf("%s: truncated script still calls main", cut.name)
		}
		partial := filepath.Join(t.TempDir(), "install.sh")
		writeFile(t, partial, string(cut.data))
		for _, tc := range []struct {
			name string
			opts installOpts
		}{
			{name: "file", opts: installOpts{script: partial}},
			{name: "stdin", opts: installOpts{stdin: cut.data}},
		} {
			t.Run(cut.name+"/"+tc.name, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "bin")
				res := runInstall(t, release, tc.opts, "MADE_INSTALL_DIR="+dir)
				if cut.complete && res.code != 0 {
					t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
				}
				if res.curlCalls != 0 {
					t.Errorf("curl called %d times, want 0", res.curlCalls)
				}
				requireNotInstalled(t, filepath.Join(dir, "made"))
			})
		}
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
			if tmp := scriptTmp(t, res); pathExists(tmp) {
				t.Errorf("temp dir %s still exists after the signal", tmp)
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
			name: "shasum only",
			opts: installOpts{sha256Tool: "shasum"},
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
				// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
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

// TestInstallTagMatchesSelfUpdate runs install.sh on each MADE_VERSION and checks it
// accepts the same tags as selfupdate.NormalizeTag and downloads the archive from the
// URL made self-update would use, so the two cannot drift apart.
func TestInstallTagMatchesSelfUpdate(t *testing.T) {
	release := customRelease(t, releaseOpts{os: "linux", arch: "amd64"})
	for _, in := range []string{
		"v1.2.3", "1.2.3", "V1.2.3", "v1.2.3-rc.1+b", "v0.10.0", "v1", "v", "vx",
		"x1.2.3", "vv1.2.3", "v1.2;rm", "v1.2.3/x", "v1.2 3", "v1.2.3_4", "V", "0",
	} {
		t.Run(in, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bin")
			res := runInstall(t, release, installOpts{uname: [2]string{"Linux", "x86_64"}}, "MADE_VERSION="+in, "MADE_INSTALL_DIR="+dir)
			tag, err := selfupdate.NormalizeTag(in)
			if err != nil {
				if res.code != 1 || res.curlCalls != 0 || !strings.Contains(res.stderr, "release tag") {
					t.Errorf("NormalizeTag(%q) fails (%v), but install.sh exited %d after %d curl calls: %s", in, err, res.code, res.curlCalls, res.stderr)
				}
				return
			}
			asset, _ := selfupdate.AssetName(tag, "linux", "amd64")
			url := selfupdate.ReleaseURL(selfupdate.DefaultBaseURL, tag, asset)
			if tag == "v"+installVersion {
				if res.code != 0 || !strings.Contains(res.stdout, "Downloading made "+tag+" (linux/amd64)") {
					t.Errorf("install.sh with MADE_VERSION=%q: exit %d, stdout %q, want it to install %s", in, res.code, res.stdout, tag)
				}
				return
			}
			if want := "made-install: download failed: " + url + "\n"; res.code != 1 || !strings.Contains(res.stderr, want) {
				t.Errorf("install.sh with MADE_VERSION=%q: exit %d, stderr %q, want it to request %s (as made self-update does)", in, res.code, res.stderr, url)
			}
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

// readInstallScript returns install.sh and its prefix up to the closing brace of
// main, which defines the functions without calling main.
func readInstallScript(t *testing.T) (script, withoutMain []byte) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	withoutMain = script[:bytes.LastIndex(script, []byte("\n}\n"))+3]
	if rest := bytes.TrimSpace(script[len(withoutMain):]); string(rest) != `main "$@"` {
		t.Fatalf("cut after the last closing brace drops %q, want only the main call", rest)
	}
	return script, withoutMain
}

// TestInstallScriptFetchHTTPSOnly runs install.sh's fetch under the real curl against
// local servers and checks it refuses plain HTTP, both as the URL and as a redirect
// from HTTPS, and TLS below 1.2, and that it ignores ~/.curlrc but honours
// CURL_CA_BUNDLE. Bare curl runs alongside as a control, so a failure is known to come
// from fetch's flags rather than from the test setup.
func TestInstallScriptFetchHTTPSOnly(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl not found on PATH; skipping")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on PATH; skipping")
	}
	_, withoutMain := readInstallScript(t)
	fetch := filepath.Join(t.TempDir(), "fetch.sh")
	writeFile(t, fetch, string(withoutMain)+"fetch \"$@\"\n")

	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainHits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(plain.Close)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/to-http" {
			http.Redirect(w, r, plain.URL+"/ok", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	secure := httptest.NewTLSServer(handler)
	t.Cleanup(secure.Close)
	old := httptest.NewUnstartedServer(handler)
	// #nosec G402 -- This local server deliberately offers TLS 1.0/1.1 to test their rejection.
	old.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	old.StartTLS()
	t.Cleanup(old.Close)

	certs := t.TempDir()
	cacert := func(name string, s *httptest.Server) string {
		path := filepath.Join(certs, name)
		writeFile(t, path, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})))
		return path
	}
	secureCA, oldCA := cacert("secure.pem", secure), cacert("old.pem", old)

	// HOME is a fresh dir so no ~/.curlrc applies, and no proxy variables are passed.
	home := t.TempDir()
	type result struct {
		code      int
		body      string
		plainHits int32
		stderr    string
	}
	// runEnv runs fetch (or bare curl -fsSL) with args and -o, env appended.
	runEnv := func(t *testing.T, viaFetch bool, env []string, args ...string) result {
		t.Helper()
		out := filepath.Join(t.TempDir(), "out")
		args = append(args, "-o", out)
		name := curl
		if viaFetch {
			name, args = sh, append([]string{fetch}, args...)
		} else {
			args = append([]string{"-fsSL"}, args...)
		}
		before := plainHits.Load()
		// #nosec G204 -- Runs the fixed fetch function or curl against local httptest servers.
		cmd := exec.CommandContext(t.Context(), name, args...)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}, env...)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		var res result
		if runErr := cmd.Run(); runErr != nil {
			var ee *exec.ExitError
			if !errors.As(runErr, &ee) {
				t.Fatalf("running %s: %v", name, runErr)
			}
			res.code = ee.ExitCode()
		}
		res.stderr = errb.String()
		res.plainHits = plainHits.Load() - before
		// #nosec G304 -- This path is a fixture or output inside the test's temporary directory.
		if data, readErr := os.ReadFile(out); readErr == nil {
			res.body = string(data)
		}
		return res
	}
	run := func(t *testing.T, viaFetch bool, args ...string) result {
		t.Helper()
		return runEnv(t, viaFetch, nil, args...)
	}
	// refused checks a fetch failed with CURLE_UNSUPPORTED_PROTOCOL (1) and never
	// reached the plain HTTP server.
	refused := func(t *testing.T, res result) {
		t.Helper()
		if res.code != 1 {
			t.Errorf("exit = %d, want 1 (protocol refused)\nstderr: %s", res.code, res.stderr)
		}
		if res.plainHits != 0 {
			t.Errorf("plain HTTP server got %d requests, want 0", res.plainHits)
		}
		if res.body != "" {
			t.Errorf("downloaded %q, want nothing", res.body)
		}
	}

	t.Run("https", func(t *testing.T) {
		if res := run(t, true, "--cacert", secureCA, secure.URL+"/ok"); res.code != 0 || res.body != "ok" {
			t.Fatalf("exit = %d, body %q, want 0 and %q\nstderr: %s", res.code, res.body, "ok", res.stderr)
		}
	})
	t.Run("plain http", func(t *testing.T) {
		if res := run(t, false, plain.URL+"/ok"); res.code != 0 || res.plainHits != 1 {
			t.Fatalf("control: bare curl exit = %d after %d requests, want 0 after 1\nstderr: %s", res.code, res.plainHits, res.stderr)
		}
		refused(t, run(t, true, plain.URL+"/ok"))
	})
	// --proto covers every redirect hop, so fetch needs no --proto-redir.
	t.Run("redirect to http", func(t *testing.T) {
		if res := run(t, false, "--cacert", secureCA, secure.URL+"/to-http"); res.code != 0 || res.plainHits != 1 {
			t.Fatalf("control: bare curl exit = %d after %d plain requests, want 0 after 1\nstderr: %s", res.code, res.plainHits, res.stderr)
		}
		refused(t, run(t, true, "--cacert", secureCA, secure.URL+"/to-http"))
	})
	t.Run("curlrc insecure", func(t *testing.T) {
		rcHome := t.TempDir()
		writeFile(t, filepath.Join(rcHome, ".curlrc"), "insecure\n")
		env := []string{"HOME=" + rcHome}
		if res := runEnv(t, false, env, secure.URL+"/ok"); res.code != 0 {
			t.Fatalf("control: bare curl with the curlrc exit = %d, want 0 (untrusted cert accepted)\nstderr: %s", res.code, res.stderr)
		}
		// 60 is CURLE_PEER_FAILED_VERIFICATION: the untrusted cert is still refused.
		if res := runEnv(t, true, env, secure.URL+"/ok"); res.code != 60 || res.body != "" {
			t.Errorf("exit = %d, body %q, want 60 and nothing downloaded\nstderr: %s", res.code, res.body, res.stderr)
		}
	})
	t.Run("CURL_CA_BUNDLE", func(t *testing.T) {
		if res := runEnv(t, true, []string{"CURL_CA_BUNDLE=" + secureCA}, secure.URL+"/ok"); res.code != 0 || res.body != "ok" {
			t.Errorf("exit = %d, body %q, want 0 and %q\nstderr: %s", res.code, res.body, "ok", res.stderr)
		}
	})
	t.Run("tls 1.1", func(t *testing.T) {
		if res := run(t, false, "--tlsv1.0", "--cacert", oldCA, old.URL+"/ok"); res.code != 0 {
			t.Skipf("this curl refuses TLS 1.1 on its own (exit %d), so fetch's --tlsv1.2 cannot be told apart: %s", res.code, strings.TrimSpace(res.stderr))
		}
		// 35 is CURLE_SSL_CONNECT_ERROR: the handshake fails on the version.
		if res := run(t, true, "--cacert", oldCA, old.URL+"/ok"); res.code != 35 || res.body != "" {
			t.Errorf("exit = %d, body %q, want 35 and nothing downloaded\nstderr: %s", res.code, res.body, res.stderr)
		}
	})
}
