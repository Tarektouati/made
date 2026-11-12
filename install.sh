#!/bin/sh
# Install the latest (or a pinned) made release binary.
#
#   curl -fsSL https://raw.githubusercontent.com/Tarektouati/made/main/install.sh | sh
#
# Environment:
#   MADE_VERSION      release tag to install, e.g. v1.2.3 (default: latest)
#   MADE_INSTALL_DIR  target directory (default: /usr/local/bin)

set -eu

REPO="Tarektouati/made"

err() {
	echo "made-install: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

need curl
need tar
need uname

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) err "unsupported OS: $(uname -s) (use 'go install github.com/$REPO@latest')" ;;
esac

case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) err "unsupported architecture: $(uname -m)" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	err "required command not found: sha256sum or shasum"
fi

tag="${MADE_VERSION:-}"
if [ -z "$tag" ]; then
	latest_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
		err "could not resolve the latest release"
	tag="${latest_url##*/}"
fi
case "$tag" in
	v*) ;;
	*) tag="v$tag" ;;
esac
case "$tag" in
	v[0-9]*) ;;
	*) err "could not determine a release tag (got '$tag')" ;;
esac
version="${tag#v}"

archive="made_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading made $tag ($os/$arch)..."
curl -fsSL -o "$tmp/$archive" "$base_url/$archive" || err "download failed: $base_url/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base_url/checksums.txt" || err "download failed: $base_url/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || err "no checksum found for $archive"
actual=$(sha256 "$tmp/$archive")
[ "$expected" = "$actual" ] || err "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" made

install_dir="${MADE_INSTALL_DIR:-/usr/local/bin}"
sudo=""
if [ -z "${MADE_INSTALL_DIR:-}" ] && ! { [ -d "$install_dir" ] && [ -w "$install_dir" ]; }; then
	if command -v sudo >/dev/null 2>&1; then
		sudo="sudo"
	else
		install_dir="$HOME/.local/bin"
	fi
fi

$sudo mkdir -p "$install_dir"
$sudo install -m 0755 "$tmp/made" "$install_dir/made"

echo "Installed $("$install_dir/made" --version) to $install_dir/made"

case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) echo "Note: $install_dir is not on your PATH; add it to use 'made' directly." ;;
esac
