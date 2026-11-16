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
	"strings"
	"syscall"
	"testing"
)

// installVersion is the release served by the fake curl.
const installVersion = "1.2.3"

// curlStub stands in for curl: it logs each call, answers the latest-release redirect
// with installVersion, and serves release files from @RELEASE@.
const curlStub = `#!/bin/sh
echo "$*" >>'@LOG@'
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
	noGzip      bool
	failingSudo bool
	unsetHome   bool
	// shadowMade puts a stub made on PATH, as an older install would be.
	shadowMade bool
	// stdin, when set, is fed to sh instead of running install.sh as a file.
	stdin []byte
	// script, when set, is run instead of install.sh.
	script string
}

type installResult struct {
	stdout, stderr string
	code           int
	home, work     string
	curlCalls      int
}

// releaseDir writes a fake made release for the host platform (the archive holding the
// binary under test, and checksums.txt) and returns its directory.
func releaseDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("install.sh does not support %s", runtime.GOOS)
	}
	bin, err := os.ReadFile(madeBin)
	if err != nil {
		t.Fatal(err)
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
	archive := fmt.Sprintf("made_%s_%s_%s.tar.gz", installVersion, runtime.GOOS, runtime.GOARCH)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, archive), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "checksums.txt"), fmt.Sprintf("%x  %s\n", sha256.Sum256(buf.Bytes()), archive))
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
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on PATH; skipping")
	}
	script := opts.script
	if script == "" {
		if script, err = filepath.Abs(filepath.Join("..", "install.sh")); err != nil {
			t.Fatal(err)
		}
	}

	bin := t.TempDir()
	tools := installTools
	if !opts.noGzip {
		tools = append(tools[:len(tools):len(tools)], "gzip")
	}
	for _, name := range tools {
		linkTool(t, bin, name)
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
		"@LOG@", log, "@RELEASE@", release, "@VERSION@", installVersion,
	).Replace(curlStub))
	if opts.failingSudo {
		writeExec(t, filepath.Join(bin, "sudo"), "#!/bin/sh\nexit 1\n")
	}
	if opts.shadowMade {
		writeExec(t, filepath.Join(bin, "made"), "#!/bin/sh\necho made v0.0.1\n")
	}

	res := installResult{home: t.TempDir(), work: t.TempDir()}
	cmdEnv := []string{"PATH=" + bin, "TMPDIR=" + t.TempDir()}
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

func TestInstallScript(t *testing.T) {
	release := releaseDir(t)
	cases := []struct {
		name string
		// env returns the extra environment for a run that should install into dir.
		env        func(t *testing.T, dir string) []string
		opts       installOpts
		wantCode   int
		wantStdout string
		wantStderr []string
		noCurl     bool
	}{
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
			name:       "missing gzip",
			env:        func(_ *testing.T, dir string) []string { return []string{"MADE_INSTALL_DIR=" + dir} },
			opts:       installOpts{noGzip: true},
			wantCode:   1,
			wantStderr: []string{"required command not found: gzip"},
			noCurl:     true,
		},
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
