//go:build !windows

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The action tests replace curl through PATH, never through an installer option.
// Only the two fixed GitHub endpoints are served; no request can reach the network.
const actionCurlStub = `#!/bin/sh
printf 'curl' >>"$TEST_ACTION_LOG"
for arg in "$@"; do printf '\t%s' "$arg" >>"$TEST_ACTION_LOG"; done
printf '\n' >>"$TEST_ACTION_LOG"
out= url=
while [ "$#" -gt 0 ]; do
	case $1 in
	-o | --output) out=$2; shift ;;
	-H | --header | --proto | --connect-timeout | --max-time | --retry | --retry-delay | --retry-max-time) shift ;;
	-*) ;;
	*) url=$1 ;;
	esac
	shift
done
[ -n "$out" ] || exit 2
[ "$url" != "$TEST_ACTION_FAIL_URL" ] || exit 22
case $url in
https://api.github.com/repos/Tarektouati/made/releases/latest)
	cp "$TEST_ACTION_API_JSON" "$out" ;;
https://github.com/Tarektouati/made/releases/download/v1.2.3/*)
	cp "$TEST_ACTION_RELEASE/${url##*/}" "$out" ;;
*) exit 22 ;;
esac
`

type actionOpts struct {
	version, runnerOS, runnerArch string
	apiJSON, failURL, token       string
	omit                          []string
	shaTool                       string
	// existing is installed before the action, to check failed downloads preserve it.
	existing string
}

type actionResult struct {
	code                             int
	stdout, stderr, path, output     string
	events, cache, tmp, work, binary string
}

// A small executable keeps the installer tests independent of the size and
// compression cost of the CLI binary built by TestMain.
func actionRelease(t *testing.T, opts releaseOpts) string {
	t.Helper()
	if opts.bin == nil {
		opts.bin = []byte("#!/bin/sh\nprintf 'made v1.2.3\\n'\n")
	}
	return customRelease(t, opts)
}

func runAction(t *testing.T, release string, opts actionOpts) actionResult {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found on PATH; skipping")
	}
	script, err := filepath.Abs(filepath.Join("..", "scripts", "setup-action.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := filepath.Join(root, "tools")
	res := actionResult{cache: filepath.Join(root, "tool cache"), tmp: filepath.Join(root, "runner temp"), work: filepath.Join(root, "checkout")}
	for _, dir := range []string{bin, res.cache, res.tmp, res.work} {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"awk", "cp", "gzip", "install", "jq", "mkdir", "mktemp", "rm"} {
		if !slices.Contains(opts.omit, name) {
			linkTool(t, bin, name)
		}
	}
	log := filepath.Join(root, "events.log")
	realTar, err := exec.LookPath("tar")
	if err != nil {
		t.Skip("tar not found on PATH; skipping")
	}
	if !slices.Contains(opts.omit, "tar") {
		writeExec(t, filepath.Join(bin, "tar"), "#!/bin/sh\nprintf 'tar\\n' >>\"$TEST_ACTION_LOG\"\nexec \"$TEST_ACTION_REAL_TAR\" \"$@\"\n")
	}
	shaTool := opts.shaTool
	if shaTool == "" {
		shaTool = "sha256sum"
		if !lookPath(shaTool) {
			shaTool = "shasum"
		}
	}
	var realSHA string
	if shaTool != "none" {
		realSHA, err = exec.LookPath(shaTool)
		if err != nil {
			t.Skipf("%s not found on PATH; skipping", shaTool)
		}
		writeExec(t, filepath.Join(bin, shaTool), "#!/bin/sh\nprintf 'hash\\n' >>\"$TEST_ACTION_LOG\"\nexec \"$TEST_ACTION_REAL_SHA\" \"$@\"\n")
	}
	if !slices.Contains(opts.omit, "curl") {
		writeExec(t, filepath.Join(bin, "curl"), actionCurlStub)
	}
	api := filepath.Join(root, "release.json")
	response := opts.apiJSON
	if response == "" {
		response = `{"tag_name":"v1.2.3"}`
	}
	writeFile(t, api, response)
	pathFile, outputFile := filepath.Join(root, "github path"), filepath.Join(root, "github output")
	writeFile(t, pathFile, "")
	writeFile(t, outputFile, "")
	arch := "amd64"
	if opts.runnerArch == "ARM64" {
		arch = "arm64"
	}
	res.binary = filepath.Join(res.cache, "made", installVersion, arch, "made")
	if opts.existing != "" {
		if err = os.MkdirAll(filepath.Dir(res.binary), 0o755); err != nil {
			t.Fatal(err)
		}
		writeExec(t, res.binary, opts.existing)
	}
	cmd := exec.CommandContext(t.Context(), bash, script)
	cmd.Dir = res.work
	cmd.Env = []string{
		"PATH=" + bin, "HOME=" + root, "RUNNER_TEMP=" + res.tmp,
		"RUNNER_TOOL_CACHE=" + res.cache, "GITHUB_PATH=" + pathFile, "GITHUB_OUTPUT=" + outputFile,
		"MADE_ACTION_VERSION=" + opts.version, "MADE_ACTION_TOKEN=" + opts.token,
		"MADE_ACTION_OS=" + opts.runnerOS, "MADE_ACTION_ARCH=" + opts.runnerArch,
		"TEST_ACTION_LOG=" + log, "TEST_ACTION_RELEASE=" + release, "TEST_ACTION_API_JSON=" + api,
		"TEST_ACTION_FAIL_URL=" + opts.failURL, "TEST_ACTION_REAL_TAR=" + realTar, "TEST_ACTION_REAL_SHA=" + realSHA,
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err = cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		res.code = exit.ExitCode()
	}
	res.stdout, res.stderr = out.String(), stderr.String()
	for path, target := range map[string]*string{pathFile: &res.path, outputFile: &res.output, log: &res.events} {
		data, readErr := os.ReadFile(path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			t.Fatal(readErr)
		}
		*target = string(data)
	}
	entries, err := os.ReadDir(res.tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temporary downloads not removed: %v", entries)
	}
	return res
}

func TestGitHubActionInstall(t *testing.T) {
	const binary = "#!/bin/sh\nprintf 'made v1.2.3\\n'\n"
	for _, platform := range []struct{ runnerOS, runnerArch, os, arch string }{
		{"Linux", "X64", "linux", "amd64"},
		{"Linux", "ARM64", "linux", "arm64"},
		{"macOS", "X64", "darwin", "amd64"},
		{"macOS", "ARM64", "darwin", "arm64"},
	} {
		for _, version := range []string{"latest", "1.2.3", "v1.2.3"} {
			t.Run(platform.runnerOS+"/"+platform.runnerArch+"/"+version, func(t *testing.T) {
				release := actionRelease(t, releaseOpts{bin: []byte(binary), os: platform.os, arch: platform.arch})
				res := runAction(t, release, actionOpts{version: version, runnerOS: platform.runnerOS, runnerArch: platform.runnerArch})
				if res.code != 0 {
					t.Fatalf("exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
				}
				if res.path != filepath.Dir(res.binary)+"\n" || res.output != "version=1.2.3\n" {
					t.Errorf("PATH = %q, output = %q", res.path, res.output)
				}
				if out, err := exec.CommandContext(t.Context(), res.binary, "--version").CombinedOutput(); err != nil || string(out) != "made v1.2.3\n" {
					t.Errorf("installed binary = %q, %v", out, err)
				}
				hash, tar := strings.Index(res.events, "hash\n"), strings.Index(res.events, "tar\n")
				if hash < 0 || tar < hash {
					t.Errorf("archive extracted before SHA-256 verification: %s", res.events)
				}
				apiCalls := strings.Count(res.events, "https://api.github.com/")
				if (version == "latest" && apiCalls != 1) || (version != "latest" && apiCalls != 0) {
					t.Errorf("API calls = %d for version %s", apiCalls, version)
				}
			})
		}
	}
}

func TestGitHubActionTokenAndTransport(t *testing.T) {
	const token = "action-test-token"
	release := actionRelease(t, releaseOpts{os: "linux", arch: "amd64"})
	res := runAction(t, release, actionOpts{version: "latest", runnerOS: "Linux", runnerArch: "X64", token: token})
	if res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}
	for line := range strings.Lines(res.events) {
		if !strings.HasPrefix(line, "curl\t") {
			continue
		}
		args := strings.Split(strings.TrimSuffix(line, "\n"), "\t")[1:]
		if args[0] != "-q" {
			t.Error("curl must ignore curlrc before reading any other argument")
		}
		for _, flag := range []string{"--proto", "=https", "--tlsv1.2", "--connect-timeout", "--max-time", "--retry-max-time"} {
			if !slices.Contains(args, flag) {
				t.Errorf("curl missing %s: %s", flag, line)
			}
		}
		isAPI := strings.Contains(line, "https://api.github.com/")
		if strings.Contains(line, token) != isAPI {
			t.Errorf("token must be sent only to the API: %s", line)
		}
		if isAPI && (slices.Contains(args, "--location") || slices.Contains(args, "-L")) {
			t.Error("authenticated API request must not follow redirects")
		}
		if !isAPI && !slices.Contains(args, "--location") {
			t.Error("release assets need GitHub's download redirect")
		}
	}
	if strings.Contains(res.stdout+res.stderr+res.path+res.output, token) {
		t.Error("token appeared in action output")
	}
}

func TestGitHubActionRejectsUnsupportedInputs(t *testing.T) {
	for _, tc := range []struct {
		name, version, os, arch, want string
	}{
		{"Windows", "latest", "Windows", "X64", "does not support Windows runners"},
		{"OS", "latest", "FreeBSD", "X64", "unsupported runner OS"},
		{"arch", "latest", "Linux", "ARM", "unsupported runner architecture"},
		{"partial", "1", "Linux", "X64", "invalid version"},
		{"path", "../../1.2.3", "Linux", "X64", "invalid version"},
		{"shell", "1.2.3$(touch PWNED)", "Linux", "X64", "invalid version"},
		{"newline", "1.2.3\n::error::injected", "Linux", "X64", "invalid version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runAction(t, t.TempDir(), actionOpts{version: tc.version, runnerOS: tc.os, runnerArch: tc.arch})
			if res.code == 0 || !strings.Contains(res.stderr, tc.want) {
				t.Errorf("exit %d, stderr = %q, want %q", res.code, res.stderr, tc.want)
			}
			if res.events != "" || res.path != "" || res.output != "" {
				t.Errorf("unsupported input had effects: events=%q path=%q output=%q", res.events, res.path, res.output)
			}
			if strings.Contains(res.stderr, "::error::") {
				t.Error("untrusted version was printed as a workflow command")
			}
			if _, err := os.Stat(filepath.Join(res.work, "PWNED")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("version was evaluated: %v", err)
			}
		})
	}
}

func TestGitHubActionDownloadFailures(t *testing.T) {
	const base = "https://github.com/Tarektouati/made/releases/download/v1.2.3/"
	for _, tc := range []struct{ name, api, fail, want string }{
		{"API failure", "", "https://api.github.com/repos/Tarektouati/made/releases/latest", "could not resolve latest"},
		{"malformed JSON", "not JSON", "", "invalid release response"},
		{"missing tag", `{}`, "", "invalid release response"},
		{"numeric tag", `{"tag_name":12}`, "", "invalid release response"},
		{"bad tag", `{"tag_name":"../../outside"}`, "", "invalid version"},
		{"archive failure", "", base + "made_1.2.3_linux_amd64.tar.gz", "could not download"},
		{"checksum failure", "", base + "checksums.txt", "could not download"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := actionRelease(t, releaseOpts{os: "linux", arch: "amd64"})
			res := runAction(t, release, actionOpts{version: "latest", runnerOS: "Linux", runnerArch: "X64", apiJSON: tc.api, failURL: tc.fail, existing: "keep this binary"})
			if res.code == 0 || !strings.Contains(res.stderr, tc.want) {
				t.Errorf("exit %d, stderr = %q, want %q", res.code, res.stderr, tc.want)
			}
			if strings.Contains(res.events, "tar\n") || res.path != "" || res.output != "" {
				t.Errorf("failed download exposed an installation: %s", res.events)
			}
			data, err := os.ReadFile(res.binary)
			if err != nil || string(data) != "keep this binary" {
				t.Errorf("existing binary changed: %q, %v", data, err)
			}
		})
	}
}

func TestGitHubActionChecksums(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(string) string
		success bool
	}{
		{"mismatch", func(s string) string { return strings.Repeat("0", 64) + s[64:] }, false},
		{"missing", func(string) string { return "" }, false},
		{"wrong archive", func(s string) string { return strings.ReplaceAll(s, "amd64", "arm64") }, false},
		{"malformed", func(s string) string { return "not-a-hash" + s[64:] }, false},
		{"duplicate", func(s string) string { return s + s }, false},
		{"CRLF", func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }, true},
		{"uppercase", func(s string) string { return strings.ToUpper(s[:64]) + s[64:] }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := actionRelease(t, releaseOpts{os: "linux", arch: "amd64", checksums: tc.change})
			res := runAction(t, release, actionOpts{version: "1.2.3", runnerOS: "Linux", runnerArch: "X64"})
			if (res.code == 0) != tc.success {
				t.Fatalf("exit %d: %s", res.code, res.stderr)
			}
			if !tc.success {
				if !strings.Contains(res.stderr, "checksum") || strings.Contains(res.events, "tar\n") || res.path != "" || res.output != "" {
					t.Errorf("checksum failure did not stop before extraction: stderr=%s events=%s", res.stderr, res.events)
				}
				requireNotInstalled(t, res.binary)
			}
		})
	}
}

func TestGitHubActionTools(t *testing.T) {
	for _, name := range []string{"curl", "tar", "jq", "sha256sum or shasum"} {
		t.Run(name, func(t *testing.T) {
			opts := actionOpts{version: "latest", runnerOS: "Linux", runnerArch: "X64", omit: []string{name}}
			if name == "sha256sum or shasum" {
				opts.shaTool = "none"
			}
			res := runAction(t, t.TempDir(), opts)
			if res.code == 0 || !strings.Contains(res.stderr, "required command not found: "+name) {
				t.Errorf("exit %d: %s", res.code, res.stderr)
			}
			if res.events != "" {
				t.Errorf("missing tool made a request: %s", res.events)
			}
		})
	}
	t.Run("pinned without jq", func(t *testing.T) {
		release := actionRelease(t, releaseOpts{os: "linux", arch: "amd64"})
		res := runAction(t, release, actionOpts{version: "v1.2.3", runnerOS: "Linux", runnerArch: "X64", omit: []string{"jq"}})
		if res.code != 0 {
			t.Fatalf("exit %d: %s", res.code, res.stderr)
		}
	})
	t.Run("shasum fallback", func(t *testing.T) {
		release := actionRelease(t, releaseOpts{os: "darwin", arch: "arm64"})
		res := runAction(t, release, actionOpts{version: "1.2.3", runnerOS: "macOS", runnerArch: "ARM64", shaTool: "shasum"})
		if res.code != 0 {
			t.Fatalf("exit %d: %s", res.code, res.stderr)
		}
	})
}

func TestGitHubActionBadArchive(t *testing.T) {
	release := actionRelease(t, releaseOpts{os: "linux", arch: "amd64", archive: []byte("not a tar archive")})
	res := runAction(t, release, actionOpts{version: "1.2.3", runnerOS: "Linux", runnerArch: "X64"})
	if res.code == 0 || !strings.Contains(res.stderr, "could not extract made") {
		t.Errorf("exit %d: %s", res.code, res.stderr)
	}
	if res.path != "" || res.output != "" {
		t.Error("bad archive exposed outputs")
	}
	requireNotInstalled(t, res.binary)
}

func TestGitHubActionRejectsSymlinkExecutable(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, outside, "leave this file alone")
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "made", Typeflag: tar.TypeSymlink, Linkname: outside}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	release := actionRelease(t, releaseOpts{os: "linux", arch: "amd64", archive: archive.Bytes()})
	res := runAction(t, release, actionOpts{version: "1.2.3", runnerOS: "Linux", runnerArch: "X64"})
	if res.code == 0 || !strings.Contains(res.stderr, "regular made executable") {
		t.Errorf("exit %d: %s", res.code, res.stderr)
	}
	if res.path != "" || res.output != "" {
		t.Error("symlink archive exposed outputs")
	}
	requireNotInstalled(t, res.binary)
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "leave this file alone" {
		t.Errorf("symlink target changed: %q, %v", data, err)
	}
}

func TestGitHubActionReplacesExistingBinary(t *testing.T) {
	const binary = "#!/bin/sh\necho installed\n"
	release := actionRelease(t, releaseOpts{bin: []byte(binary), os: "linux", arch: "amd64"})
	res := runAction(t, release, actionOpts{version: "1.2.3", runnerOS: "Linux", runnerArch: "X64", existing: "old binary"})
	if res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}
	data, err := os.ReadFile(res.binary)
	if err != nil || string(data) != binary {
		t.Errorf("installed binary = %q, %v", data, err)
	}
	if got := strings.Count(res.events, "curl\t"); got != 2 {
		t.Errorf("existing cache skipped checksum verification: %d downloads", got)
	}
}
