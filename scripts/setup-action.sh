#!/usr/bin/env bash
# Installer for action.yml. Inputs are passed through env, never expanded as code.
set -euo pipefail

fail() {
    printf 'made-action: %s\n' "$*" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

fetch() {
    # -q must come first to ignore curlrc. Redirects are enabled only for public
    # release assets; the authenticated API request below does not follow them.
    curl -q --proto '=https' --tlsv1.2 --fail --silent --show-error \
        --connect-timeout 10 --max-time 120 --retry 2 --retry-delay 1 \
        --retry-max-time 120 "$@"
}

release_version() {
    local pattern='^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?(\+[0-9A-Za-z][0-9A-Za-z.-]*)?$'
    [[ $1 =~ $pattern ]] || fail "invalid version; use latest or a full release version such as 0.7.0"
    printf '%s' "${1#v}"
}

case "${MADE_ACTION_OS:-}" in
    Linux) os=linux ;;
    macOS) os=darwin ;;
    Windows) fail "this action does not support Windows runners; release binaries are available for Linux and macOS" ;;
    *) fail "unsupported runner OS; expected Linux or macOS" ;;
esac
case "${MADE_ACTION_ARCH:-}" in
    X64) arch=amd64 ;;
    ARM64) arch=arm64 ;;
    *) fail "unsupported runner architecture; expected X64 or ARM64" ;;
esac

requested=${MADE_ACTION_VERSION:-latest}
if [[ $requested != latest ]]; then
    version=$(release_version "$requested")
fi
for tool in curl tar gzip awk mktemp mkdir install rm; do
    need "$tool"
done
if command -v sha256sum >/dev/null 2>&1; then
    hash_command=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
    hash_command=(shasum -a 256)
else
    fail "required command not found: sha256sum or shasum"
fi
if [[ $requested == latest ]]; then
    need jq
fi

case "${RUNNER_TOOL_CACHE:-}" in
    /*) ;;
    *) fail "RUNNER_TOOL_CACHE must be an absolute path" ;;
esac
case "$RUNNER_TOOL_CACHE" in
    *$'\r'* | *$'\n'*) fail "RUNNER_TOOL_CACHE must not contain line breaks" ;;
esac
[[ -n ${GITHUB_PATH:-} && -n ${GITHUB_OUTPUT:-} ]] || fail "GITHUB_PATH and GITHUB_OUTPUT must be set by the runner"
[[ -n ${RUNNER_TEMP:-} ]] || fail "RUNNER_TEMP must be set by the runner"

work_dir=$(mktemp -d "$RUNNER_TEMP/made-action.XXXXXXXX")
trap 'rm -rf "$work_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

if [[ $requested == latest ]]; then
    api_headers=(-H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28')
    token=${MADE_ACTION_TOKEN:-}
    case "$token" in
        *$'\r'* | *$'\n'*) fail "GitHub token must not contain line breaks" ;;
    esac
    if [[ -n $token ]]; then
        api_headers+=(-H "Authorization: Bearer $token")
    fi
    fetch --max-time 30 "${api_headers[@]}" -o "$work_dir/release.json" \
        'https://api.github.com/repos/Tarektouati/made/releases/latest' || fail "could not resolve latest from the GitHub Releases API"
    tag=$(jq -er '.tag_name | select(type == "string" and length > 0)' "$work_dir/release.json") || fail "invalid release response from the GitHub Releases API"
    version=$(release_version "$tag")
fi

archive="made_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/Tarektouati/made/releases/download/v$version"
fetch --location -o "$work_dir/$archive" "$base_url/$archive" || fail "could not download $archive"
fetch --location -o "$work_dir/checksums.txt" "$base_url/checksums.txt" || fail "could not download checksums.txt"

expected=$(awk -v file="$archive" '{ sub(/\r$/, "") } $2 == file { print tolower($1) }' "$work_dir/checksums.txt")
[[ $expected =~ ^[0-9a-f]{64}$ ]] || fail "expected one SHA-256 checksum for $archive"
actual=$("${hash_command[@]}" "$work_dir/$archive") || fail "could not calculate the archive checksum"
actual=${actual%% *}
[[ $actual == "$expected" ]] || fail "checksum mismatch for $archive"

# Extract only the executable, and only after its archive has been verified.
tar -xzf "$work_dir/$archive" -C "$work_dir" made || fail "could not extract made from $archive"
[[ -f $work_dir/made && ! -L $work_dir/made ]] || fail "archive does not contain a regular made executable"
install_dir="$RUNNER_TOOL_CACHE/made/$version/$arch"
mkdir -p "$install_dir"
install -m 0755 "$work_dir/made" "$install_dir/made"

printf '%s\n' "$install_dir" >> "$GITHUB_PATH"
printf 'version=%s\n' "$version" >> "$GITHUB_OUTPUT"
printf 'Installed made v%s (%s/%s)\n' "$version" "$os" "$arch"
