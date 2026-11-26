package e2e

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Native Apple tools are deliberately stubbed, on macOS too. These tests exercise
// release gating, argument order and cleanup; they cannot establish that Apple
// accepted a real Developer ID signature.
const notarizationStub = `#!/bin/bash
set -eu
tool=${0##*/}
stage=$tool
case "$tool" in
  uname) printf '%s\n' "${NOTARY_HOST:-Darwin}"; exit 0 ;;
  security) stage=$1 ;;
  codesign) if [ "$1" = -vvv ]; then stage=verify; else stage=sign; fi ;;
  ditto) stage=zip ;;
  xcrun) stage=submit ;;
  PlistBuddy) stage=extract ;;
esac
printf '%s\n' "$stage" >> "$NOTARY_LOG"
printf '%s\n' "$tool $*" >> "$NOTARY_ARGS"
if [ "${NOTARY_FAIL:-}" = "$stage" ]; then
  printf '%s\n' "$MACOS_TEST_PRIVATE_VALUE"
  printf '%s\n' "$MACOS_TEST_PRIVATE_VALUE" >&2
  exit 17
fi
case "$stage" in
  openssl) printf '%s\n' temporary-keychain-password ;;
  create-keychain) touch "${!#}" ;;
  delete-keychain) rm -f "${!#}" ;;
  import)
    [ "$(cat "$2")" = fixture-certificate ]
    [ "$(stat -c %a "$2" 2>/dev/null || stat -f %Lp "$2")" = 600 ]
    ;;
  sign) printf '%s\n' signed >> "${!#}" ;;
  verify) [ "$(tail -n 1 "${!#}")" = signed ] ;;
  zip)
    [ "$1 $2 $3" = '-c -k --keepParent' ]
    [ "$(tail -n 1 "$4")" = signed ]
    cp "$4" "$5"
    ;;
  submit)
    [ "$1 $2" = 'notarytool submit' ]
    [ "$(tail -n 1 "$3")" = signed ]
    if [ "${NOTARY_SIGNAL:-}" != '' ]; then kill -"$NOTARY_SIGNAL" "$PPID"; exit 0; fi
    case "${NOTARY_STATUS:-Accepted}" in
      malformed) printf '%s\n' 'not a plist' ;;
      missing) printf '%s\n' '<plist><dict/></plist>' ;;
      *) printf '<plist><dict><key>status</key><string>%s</string></dict></plist>\n' "${NOTARY_STATUS:-Accepted}" ;;
    esac
    ;;
  extract)
    [ "$1 $2" = '-c Print :status' ]
    grep -q '<key>status</key>' "$3" || exit 1
    sed -n 's/.*<string>\([^<]*\)<\/string>.*/\1/p' "$3"
    ;;
esac
`

type notarizationFixture struct {
	dir, workspace, binary, log, args, script, bin string
	env                                            []string
}

func newNotarizationFixture(t *testing.T) notarizationFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the release hook runs on macOS; these shell stubs require Unix")
	}
	requireCmd(t, "bash")
	dir := t.TempDir()
	f := notarizationFixture{
		dir: dir, workspace: filepath.Join(dir, "made-signing.ABC12345"),
		binary: filepath.Join(dir, "binary with spaces"), log: filepath.Join(dir, "calls"),
		args: filepath.Join(dir, "arguments"), bin: filepath.Join(dir, "tools"),
	}
	var err error
	f.script, err = filepath.Abs(filepath.Join("..", "scripts", "notarize-macos.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(f.workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(f.bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"uname", "security", "codesign", "ditto", "xcrun", "PlistBuddy", "openssl"} {
		// #nosec G306 -- Private executable stub models the Apple tool without using credentials.
		if err = os.WriteFile(filepath.Join(f.bin, tool), []byte(notarizationStub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// #nosec G306 -- The private fixture models the executable being signed.
	if err = os.WriteFile(f.binary, []byte("unsigned\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.env = []string{
		"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RUNNER_TEMP=" + dir, "MADE_SIGNING_DIR=" + f.workspace,
		"NOTARY_LOG=" + f.log, "NOTARY_ARGS=" + f.args,
		"MACOS_SIGN_P12=" + base64.StdEncoding.EncodeToString([]byte("fixture-certificate")),
		"MACOS_SIGN_PASSWORD=fixture-p12-password", "MACOS_SIGN_IDENTITY=Developer ID Application: Fixture",
		"MACOS_NOTARY_KEY=" + base64.StdEncoding.EncodeToString([]byte("fixture-private-key")),
		"MACOS_NOTARY_KEY_ID=fixture-key-id", "MACOS_NOTARY_ISSUER_ID=fixture-issuer",
		"MACOS_TEST_PRIVATE_VALUE=fixture-secret-never-log",
	}
	return f
}

func (f notarizationFixture) run(t *testing.T, osname string, env ...string) (string, int) {
	t.Helper()
	// -x checks that the script disables tracing before touching credential values.
	args := []string{"-x", f.script, osname}
	if osname != "cleanup" {
		args = append(args, f.binary)
	}
	// #nosec G204 -- Runs the checked-in hook with literal fixture arguments and mocked Apple tools.
	cmd := exec.CommandContext(t.Context(), "bash", args...)
	cmd.Env = append(append(os.Environ(), f.env...), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if err != nil {
		if cmd.ProcessState == nil {
			t.Fatalf("start hook: %v", err)
		}
		code = cmd.ProcessState.ExitCode()
	}
	for _, secret := range []string{"fixture-secret-never-log", "fixture-p12-password", "fixture-private-key", "fixture-certificate", "temporary-keychain-password", "Developer ID Application: Fixture"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("hook logged a credential: %q", &out)
		}
	}
	return out.String(), code
}

func (f notarizationFixture) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func (f notarizationFixture) clean(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temporary signing files remain: %v", entries)
	}
}

func TestMacOSNotarizationOrderAndCleanup(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			f := newNotarizationFixture(t)
			artifactDir := filepath.Join(f.dir, "made darwin "+arch)
			if err := os.Mkdir(artifactDir, 0o700); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(artifactDir, "made")
			if err := os.Rename(f.binary, artifact); err != nil {
				t.Fatal(err)
			}
			f.binary = artifact
			out, code := f.run(t, "darwin")
			if code != 0 || !strings.Contains(out, "notarization accepted") {
				t.Fatalf("hook exit %d: %s", code, out)
			}
			want := []string{"openssl", "create-keychain", "set-keychain-settings", "unlock-keychain", "import", "set-key-partition-list", "sign", "verify", "zip", "submit", "extract", "delete-keychain"}
			if got := f.calls(t); !slices.Equal(got, want) {
				t.Errorf("call order = %q, want %q", got, want)
			}
			f.clean(t)
			b, err := os.ReadFile(f.args)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{"--timestamp -o runtime", "--keychain ", "-i com.github.Tarektouati.made", "--wait --timeout 20m --output-format plist", "--key-id fixture-key-id --issuer fixture-issuer"} {
				if !strings.Contains(string(b), required) {
					t.Errorf("missing required invocation %q", required)
				}
			}
			for _, forbidden := range []string{"list-keychain", "default-keychain", "stapler", "xattr"} {
				if strings.Contains(string(b), forbidden) {
					t.Errorf("unexpected global/bypass command %q", forbidden)
				}
			}
		})
	}
}

func TestMacOSNotarizationFailsClosed(t *testing.T) {
	for _, stage := range []string{"openssl", "create-keychain", "set-keychain-settings", "unlock-keychain", "import", "set-key-partition-list", "sign", "verify", "zip", "submit", "extract", "delete-keychain"} {
		t.Run(stage, func(t *testing.T) {
			f := newNotarizationFixture(t)
			out, code := f.run(t, "darwin", "NOTARY_FAIL="+stage)
			if code == 0 {
				t.Fatalf("failed %s returned success: %s", stage, out)
			}
			f.clean(t)
			calls := f.calls(t)
			if !slices.Contains(calls, stage) {
				t.Errorf("did not reach the failed %s stage: %q", stage, calls)
			}
			if stage == "sign" || stage == "verify" || stage == "zip" {
				if slices.Contains(calls, "submit") {
					t.Errorf("notarization submitted after %s failed: %q", stage, calls)
				}
			}
		})
	}
	for _, status := range []string{"Invalid", "In Progress", "Rejected", "malformed", "missing"} {
		t.Run(status, func(t *testing.T) {
			f := newNotarizationFixture(t)
			out, code := f.run(t, "darwin", "NOTARY_STATUS="+status)
			if code == 0 || strings.Contains(out, "notarization accepted") {
				t.Fatalf("unfinished/rejected submission returned success: %d %s", code, out)
			}
			f.clean(t)
			if calls := f.calls(t); !slices.Contains(calls, "submit") || !slices.Contains(calls, "delete-keychain") {
				t.Errorf("did not submit and clean the rejected response: %q", calls)
			}
		})
	}
}

func TestMacOSNotarizationRequiresCredentials(t *testing.T) {
	for _, key := range []string{"MACOS_SIGN_P12", "MACOS_SIGN_PASSWORD", "MACOS_SIGN_IDENTITY", "MACOS_NOTARY_KEY", "MACOS_NOTARY_KEY_ID", "MACOS_NOTARY_ISSUER_ID"} {
		t.Run(key, func(t *testing.T) {
			f := newNotarizationFixture(t)
			out, code := f.run(t, "darwin", key+"=")
			if code == 0 || !strings.Contains(out, key) {
				t.Fatalf("missing %s: exit %d, %s", key, code, out)
			}
			if got := f.calls(t); len(got) != 0 {
				t.Errorf("native setup ran without credentials: %q", got)
			}
			f.clean(t)
		})
	}
}

func TestMacOSNotarizationLeavesLinuxAlone(t *testing.T) {
	f := newNotarizationFixture(t)
	out, code := f.run(t, "linux", "MACOS_SIGN_P12=", "NOTARY_HOST=Linux")
	if code != 0 || len(f.calls(t)) != 0 {
		t.Fatalf("Linux hook = %d, %s; calls %q", code, out, f.calls(t))
	}
	b, err := os.ReadFile(f.binary)
	if err != nil || string(b) != "unsigned\n" {
		t.Errorf("Linux artifact changed: %q, %v", b, err)
	}
	f.clean(t)
}

func TestMacOSNotarizationInterrupted(t *testing.T) {
	for _, tc := range []struct {
		signal string
		code   int
	}{{"HUP", 129}, {"INT", 130}, {"TERM", 143}} {
		t.Run(tc.signal, func(t *testing.T) {
			f := newNotarizationFixture(t)
			out, code := f.run(t, "darwin", "NOTARY_SIGNAL="+tc.signal)
			if code != tc.code || strings.Contains(out, "notarization accepted") {
				t.Errorf("interrupted hook = %d, want %d: %s", code, tc.code, out)
			}
			if calls := f.calls(t); !slices.Contains(calls, "delete-keychain") {
				t.Errorf("interruption did not delete the keychain: %q", calls)
			}
			f.clean(t)
		})
	}
}

func TestMacOSNotarizationBadInputs(t *testing.T) {
	for _, tc := range []struct {
		name, env, want string
	}{
		{"wrong host", "NOTARY_HOST=Linux", "requires a macOS host"},
		{"invalid certificate encoding", "MACOS_SIGN_P12=not valid base64!", "cannot decode signing certificate"},
		{"invalid key encoding", "MACOS_NOTARY_KEY=not valid base64!", "cannot decode notary key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNotarizationFixture(t)
			out, code := f.run(t, "darwin", tc.env)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Errorf("bad input = %d, %s; want %q", code, out, tc.want)
			}
			if got := f.calls(t); len(got) != 0 {
				t.Errorf("native setup ran with bad inputs: %q", got)
			}
			f.clean(t)
		})
	}
}

func TestMacOSNotarizationCleanupFallback(t *testing.T) {
	f := newNotarizationFixture(t)
	leftover := filepath.Join(f.workspace, "notarize.ABC123")
	if err := os.Mkdir(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"signing.keychain-db", "certificate.p12", "AuthKey.p8", "result.plist"} {
		if err := os.WriteFile(filepath.Join(leftover, name), []byte("leftover fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, code := f.run(t, "cleanup", "MACOS_SIGN_P12=", "MACOS_NOTARY_KEY=")
	if code != 0 {
		t.Fatalf("cleanup = %d, %s", code, out)
	}
	if got := f.calls(t); !slices.Equal(got, []string{"delete-keychain"}) {
		t.Errorf("cleanup calls = %q", got)
	}
	if _, err := os.Stat(f.workspace); !os.IsNotExist(err) {
		t.Errorf("signing workspace remains: %v", err)
	}
}

func TestMacOSNotarizationCleanupIsScoped(t *testing.T) {
	f := newNotarizationFixture(t)
	outside := t.TempDir()
	keep := filepath.Join(outside, "signing.keychain-db")
	if err := os.WriteFile(keep, []byte("keep this unrelated keychain"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, filepath.Join(f.workspace, ".."), f.workspace + "/../"} {
		out, code := f.run(t, "cleanup", "MADE_SIGNING_DIR="+path)
		if code == 0 {
			t.Errorf("cleanup accepted an unrelated/traversing path %q: %s", path, out)
		}
	}
	if err := os.Symlink(outside, filepath.Join(f.workspace, "notarize.ABC123")); err != nil {
		t.Fatal(err)
	}
	if out, code := f.run(t, "cleanup"); code == 0 {
		t.Errorf("cleanup followed or silently left a symlink: %s", out)
	}
	// #nosec G304 -- Read the unrelated fixture to verify cleanup leaves it intact.
	b, err := os.ReadFile(keep)
	if err != nil || string(b) != "keep this unrelated keychain" {
		t.Errorf("cleanup touched an unrelated keychain: %q, %v", b, err)
	}
	if got := f.calls(t); len(got) != 0 {
		t.Errorf("cleanup used security on an unrelated keychain: %q", got)
	}
}
