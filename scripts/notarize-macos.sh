#!/bin/bash
# GoReleaser build post-hook. Darwin artifacts must be signed and accepted by
# Apple before this returns; Linux artifacts are left alone.
set +x # Never trace credential values, even when the caller enabled tracing.
set -euo pipefail
umask 077

fail() {
    printf 'macOS signing: %s\n' "$1" >&2
    exit 1
}

# The workflow creates this private, per-job directory. Restrict cleanup to its
# direct children; never accept an arbitrary path for recursive removal.
workspace() {
    runner_temp=${RUNNER_TEMP:-}
    runner_temp=${runner_temp%/}
    signing_root=${MADE_SIGNING_DIR:-}
    case "$runner_temp" in /*) ;; *) fail 'RUNNER_TEMP must be an absolute directory' ;; esac
    case "$signing_root" in
        "$runner_temp"/made-signing.*) suffix=${signing_root#"$runner_temp"/made-signing.} ;;
        *) fail 'MADE_SIGNING_DIR is not a signing workspace' ;;
    esac
    case "$suffix" in ''|*[!A-Za-z0-9]*) fail 'invalid signing workspace name' ;; esac
    [ "${#suffix}" -eq 8 ] || fail 'invalid signing workspace name'
    [ ! -L "$signing_root" ] || fail 'signing workspace must not be a symlink'
}

clean_material() {
    local dir=$1 failed=0 keychain
    keychain="$dir/signing.keychain-db"
    if [ -L "$keychain" ]; then
        failed=1
    elif [ -e "$keychain" ]; then
        security delete-keychain "$keychain" >/dev/null 2>&1 || failed=1
    fi
    rm -rf -- "$dir" || failed=1
    return "$failed"
}

# Also run by an always() workflow step if GoReleaser forcibly terminates a hook.
# Successful hooks have already removed their own material before publication.
if [ "${1:-}" = cleanup ]; then
    [ "$#" -eq 1 ] || fail 'cleanup takes no arguments'
    [ -n "${MADE_SIGNING_DIR:-}" ] || exit 0
    workspace
    [ -e "$signing_root" ] || exit 0
    [ -d "$signing_root" ] || fail 'signing workspace is not a directory'
    failed=0
    for dir in "$signing_root"/notarize.*; do
        [ -d "$dir" ] && [ ! -L "$dir" ] || continue
        suffix=${dir##*/notarize.}
        case "$suffix" in ''|*[!A-Za-z0-9]*) continue ;; esac
        [ "${#suffix}" -eq 6 ] || continue
        clean_material "$dir" || failed=1
    done
    rmdir "$signing_root" 2>/dev/null || failed=1
    [ "$failed" -eq 0 ] || fail 'could not completely clean the signing workspace'
    exit 0
fi

[ "$#" -eq 2 ] || fail 'expected target OS and binary path'
case "$1" in
    linux) exit 0 ;;
    darwin) ;;
    *) fail 'unsupported release target OS' ;;
esac

for name in MACOS_SIGN_P12 MACOS_SIGN_PASSWORD MACOS_SIGN_IDENTITY MACOS_NOTARY_KEY MACOS_NOTARY_KEY_ID MACOS_NOTARY_ISSUER_ID; do
    [ -n "${!name:-}" ] || fail "$name is required for a Darwin release"
done
[ "$(uname -s)" = Darwin ] || fail 'Darwin release signing requires a macOS host'
binary=$2
[ -f "$binary" ] || fail 'release binary is not a regular file'
workspace
[ -d "$signing_root" ] || fail 'signing workspace does not exist'

# PlistBuddy is a native Apple tool kept outside the default executable PATH.
export PATH="$PATH:/usr/libexec"
for tool in security codesign ditto xcrun PlistBuddy openssl base64; do
    command -v "$tool" >/dev/null || fail "$tool is required for a Darwin release"
done

work=$(mktemp -d "$signing_root/notarize.XXXXXX") || fail 'cannot create signing workspace'
finish() {
    local status=$?
    trap - EXIT HUP INT TERM
    if ! clean_material "$work"; then
        printf '%s\n' 'macOS signing: could not completely clean temporary signing material' >&2
        [ "$status" -ne 0 ] || status=1
    fi
    exit "$status"
}
trap finish EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Keep native tool diagnostics private: certificate import/authentication errors
# must never echo credentials into the release log. Each failure names its stage.
private_command() {
    local stage=$1
    shift
    "$@" >"$work/tool.out" 2>"$work/tool.err" || fail "$stage failed"
}

certificate="$work/certificate.p12"
keychain="$work/signing.keychain-db"
notary_key="$work/AuthKey.p8"
if ! printf '%s' "$MACOS_SIGN_P12" | base64 --decode >"$certificate" 2>"$work/tool.err"; then
    fail 'cannot decode signing certificate'
fi
if ! printf '%s' "$MACOS_NOTARY_KEY" | base64 --decode >"$notary_key" 2>"$work/tool.err"; then
    fail 'cannot decode notary key'
fi
unset MACOS_SIGN_P12 MACOS_NOTARY_KEY
keychain_password=$(openssl rand -hex 32 2>"$work/tool.err") || fail 'cannot create temporary keychain password'
[ -n "$keychain_password" ] || fail 'cannot create temporary keychain password'
private_command 'keychain creation' security create-keychain -p "$keychain_password" "$keychain"
private_command 'keychain settings' security set-keychain-settings -lut 3600 "$keychain"
private_command 'keychain unlock' security unlock-keychain -p "$keychain_password" "$keychain"
private_command 'certificate import' security import "$certificate" -P "$MACOS_SIGN_PASSWORD" -A -t cert -f pkcs12 -k "$keychain"
private_command 'key access setup' security set-key-partition-list -S apple-tool:,apple: -k "$keychain_password" "$keychain"
unset MACOS_SIGN_PASSWORD keychain_password
rm -f -- "$certificate"

# Explicit keychain selection avoids changing the user's default/search list.
# -f replaces Go's ad-hoc signature, including on arm64. No relaxed entitlements
# are needed for this standalone Go executable.
private_command 'code signing' codesign --sign "$MACOS_SIGN_IDENTITY" -f --timestamp -o runtime --keychain "$keychain" -i com.github.Tarektouati.made "$binary"
private_command 'signature verification' codesign -vvv --strict "$binary"
private_command 'notarization archive' ditto -c -k --keepParent "$binary" "$work/notarize.zip"

if ! xcrun notarytool submit "$work/notarize.zip" --key "$notary_key" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" --wait --timeout 20m --output-format plist >"$work/result.plist" 2>"$work/tool.err"; then
    fail 'notarization failed or did not finish within 20 minutes'
fi
rm -f -- "$notary_key"
status=$(PlistBuddy -c 'Print :status' "$work/result.plist" 2>"$work/tool.err") || fail 'cannot read notarization status'
[ "$status" = Accepted ] || fail 'notarization was not accepted'
# Standalone binaries cannot be stapled. Gatekeeper retrieves their tickets from
# Apple online; the signed bytes below are the ones GoReleaser will archive.
printf '%s\n' 'macOS signing: notarization accepted'
