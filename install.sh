#!/bin/sh
# Install the latest (or a pinned) made release binary.
#
#   curl -fsSL https://raw.githubusercontent.com/Tarektouati/made/main/install.sh | sh
#
# Environment:
#   MADE_VERSION      release tag to install, e.g. v1.2.3 or latest (default: latest)
#   MADE_INSTALL_DIR  target directory (default: /usr/local/bin, using sudo when it is
#                     not writable, falling back to ~/.local/bin)
#
# Downloads use HTTPS with TLS 1.2 or later only, redirects included (curl 7.34+).
#
# Everything runs from main, called on the last line, so a truncated download
# executes nothing.

set -eu

REPO="Tarektouati/made"

err() {
	echo "made-install: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

# fetch runs curl over HTTPS only, on every redirect too (--proto covers redirects,
# so --proto-redir is not needed), with TLS 1.2 or later. -q, which only works as the
# first argument, skips ~/.curlrc so a setting there (such as insecure) cannot weaken
# the download.
fetch() {
	curl -q --proto '=https' --tlsv1.2 -fsSL "$@"
}

# install_to copies the extracted binary into dir, creating dir if needed. An optional
# second argument (sudo) runs both steps under that command.
install_to() {
	${2:-} mkdir -p "$1" && ${2:-} install -m 0755 "$tmp/made" "$1/made"
}

install_home() {
	[ -n "${HOME:-}" ] || err "HOME is not set; set MADE_INSTALL_DIR to choose an install directory"
	install_dir="$HOME/.local/bin"
	echo "made-install: cannot write to /usr/local/bin; installing to $install_dir instead" >&2
	install_to "$install_dir" || err "cannot install to $install_dir; set MADE_INSTALL_DIR"
}

main() {
	case "${MADE_INSTALL_DIR:-}" in
		"~"*) err "MADE_INSTALL_DIR starts with '~', which was not expanded; use \$HOME instead" ;;
		[!/]*) err "MADE_INSTALL_DIR must be an absolute path (got '$MADE_INSTALL_DIR')" ;;
	esac

	install_dir="${MADE_INSTALL_DIR:-}"
	while [ "$install_dir" != / ] && [ "${install_dir%/}" != "$install_dir" ]; do
		install_dir="${install_dir%/}"
	done

	for cmd in curl tar gzip uname awk cut mktemp install; do
		need "$cmd"
	done
	# Without this, a curl too old for fetch's flags fails every download with a
	# misleading "could not resolve the latest release".
	curl -q --proto =https --tlsv1.2 -V >/dev/null 2>&1 ||
		err "curl 7.34 or later is required (needs --tlsv1.2 and --proto)"

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
	if [ "$tag" = latest ]; then
		tag=""
	fi
	if [ -z "$tag" ]; then
		latest_url=$(fetch -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
			err "could not resolve the latest release"
		tag="${latest_url##*/}"
	fi
	case "$tag" in
		v*) ;;
		V*) tag="v${tag#V}" ;;
		*) tag="v$tag" ;;
	esac
	case "$tag" in
		v[0-9]*) ;;
		*) err "could not determine a release tag (got '$tag')" ;;
	esac
	case "${tag#v}" in
		*[!0-9A-Za-z.+-]*) err "invalid release tag '$tag'" ;;
	esac
	version="${tag#v}"

	archive="made_${version}_${os}_${arch}.tar.gz"
	base_url="https://github.com/$REPO/releases/download/$tag"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	# dash skips the EXIT trap when killed by a signal, so turn signals into exits.
	trap 'exit 130' INT
	trap 'exit 143' TERM
	trap 'exit 129' HUP

	echo "Downloading made $tag ($os/$arch)..."
	fetch -o "$tmp/$archive" "$base_url/$archive" || err "download failed: $base_url/$archive"
	fetch -o "$tmp/checksums.txt" "$base_url/checksums.txt" || err "download failed: $base_url/checksums.txt"

	expected=$(awk -v f="$archive" '{ sub(/\r$/, "") } $2 == f { print tolower($1) }' "$tmp/checksums.txt")
	[ -n "$expected" ] || err "no checksum found for $archive"
	actual=$(sha256 "$tmp/$archive")
	[ "$expected" = "$actual" ] || err "checksum mismatch for $archive"

	out=$(tar -xzf "$tmp/$archive" -C "$tmp" made 2>&1) || err "cannot extract made from $archive: $out"

	# Check the binary runs before it replaces a working one. On a noexec TMPDIR the shell
	# reports "<path>: Permission denied" (with an exit status that varies by shell); that
	# case is left to the check after installing.
	if ! out=$("$tmp/made" --version 2>&1); then
		case "$out" in
			*"$tmp/made: Permission denied"*) ;;
			*) err "downloaded made failed to run, so nothing was installed: $out" ;;
		esac
	fi

	if [ -n "$install_dir" ]; then
		install_to "$install_dir" || err "cannot install to $install_dir; choose another MADE_INSTALL_DIR"
	else
		install_dir=/usr/local/bin
		if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
			install_to "$install_dir" || err "cannot install to $install_dir"
		elif command -v sudo >/dev/null 2>&1; then
			if ! install_to "$install_dir" sudo; then
				echo "made-install: sudo failed; falling back to a directory in HOME" >&2
				install_home
			fi
		else
			install_home
		fi
	fi

	installed=$("$install_dir/made" --version 2>&1) ||
		err "installed $install_dir/made, but it failed to run: $installed"
	echo "Installed $installed to $install_dir/made"

	case ":$PATH:" in
		*":$install_dir:"*) ;;
		*) echo "Note: $install_dir is not on your PATH; add it to use 'made' directly." ;;
	esac
	found=$(command -v made 2>/dev/null) || found=""
	if [ -n "$found" ] && [ "$found" != "${install_dir%/}/made" ]; then
		echo "Note: 'made' on your PATH is $found, which takes precedence over $install_dir/made."
	fi
}

main "$@"
