#!/bin/sh
# Install the `fulcrum` CLI.
#
# This is the LINUX path, and the macOS path for anyone who would rather not
# use Homebrew: Homebrew casks do not work on Linux at all, so the tap cannot
# be the whole answer.
#
#   curl -fsSL https://raw.githubusercontent.com/headwayio/fulcrum-cli/main/install.sh | sh
#
# Environment:
#   FULCRUM_VERSION  a tag such as v0.1.0; defaults to the latest release
#   FULCRUM_BIN_DIR  where to put the binary; defaults to the first writable
#                    of /usr/local/bin then ~/.local/bin
#
# POSIX sh on purpose — this runs on whatever a container happens to have.

set -eu

REPO="headwayio/fulcrum-cli"

say() { printf '%s\n' "$*"; }
die() { printf 'fulcrum install: %s\n' "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || die "this needs $1 on PATH"
}

need uname
need tar
need mktemp

# curl or wget, whichever is here.
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1"; }
	fetch_to() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO- "$1"; }
	fetch_to() { wget -qO "$2" "$1"; }
else
	die "this needs curl or wget on PATH"
fi

detect_platform() {
	os=$(uname -s)
	arch=$(uname -m)

	case "$os" in
		Darwin) os="Darwin" ;;
		Linux)  os="Linux" ;;
		*) die "unsupported operating system: $os (darwin and linux are built)" ;;
	esac

	case "$arch" in
		x86_64|amd64)  arch="x86_64" ;;
		arm64|aarch64) arch="arm64" ;;
		*) die "unsupported architecture: $arch (x86_64 and arm64 are built)" ;;
	esac

	printf '%s_%s' "$os" "$arch"
}

latest_version() {
	# Silenced on purpose: a 404 here means "no releases yet", which is an
	# ordinary answer, and curl's own error on top of ours reads like two
	# separate faults.
	fetch "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null |
		tr ',' '\n' | grep '"tag_name"' | head -1 | cut -d'"' -f4
}

choose_bin_dir() {
	if [ -n "${FULCRUM_BIN_DIR:-}" ]; then
		printf '%s' "$FULCRUM_BIN_DIR"
		return
	fi
	for candidate in /usr/local/bin "$HOME/.local/bin"; do
		if [ -d "$candidate" ] && [ -w "$candidate" ]; then
			printf '%s' "$candidate"
			return
		fi
	done
	# Nothing writable yet: make the per-user one rather than reaching for sudo
	# on somebody's behalf.
	printf '%s' "$HOME/.local/bin"
}

platform=$(detect_platform)
version="${FULCRUM_VERSION:-$(latest_version)}"
[ -n "$version" ] || die "could not work out the latest version; set FULCRUM_VERSION"

bin_dir=$(choose_bin_dir)
archive="fulcrum_${platform}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "fulcrum $version ($platform)"

fetch_to "$base/$archive" "$tmp/$archive" ||
	die "could not download $archive for $version — check that the release exists"

# VERIFY BEFORE UNPACKING. A pipe-to-shell installer is already asking for a
# lot of trust; running an unverified binary on top of that is not reasonable,
# and a truncated download is far more common than a malicious one.
if fetch_to "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
	expected=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
	if [ -z "$expected" ]; then
		die "no checksum listed for $archive"
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
	else
		actual=""
		say "  no sha256 tool found, skipping verification"
	fi
	if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
		die "checksum mismatch for $archive — refusing to install
  expected $expected
  actual   $actual"
	fi
else
	die "could not download checksums.txt — refusing to install unverified"
fi

tar -xzf "$tmp/$archive" -C "$tmp" || die "could not unpack $archive"
[ -f "$tmp/fulcrum" ] || die "the archive did not contain a fulcrum binary"

mkdir -p "$bin_dir"
install -m 0755 "$tmp/fulcrum" "$bin_dir/fulcrum" 2>/dev/null ||
	{ cp "$tmp/fulcrum" "$bin_dir/fulcrum" && chmod 0755 "$bin_dir/fulcrum"; } ||
	die "could not write to $bin_dir — set FULCRUM_BIN_DIR to somewhere you own"

say "installed $bin_dir/fulcrum"

# Say so plainly rather than leaving them to find out by typing the name. The
# CLI writes its own absolute path into harness config, so a PATH gap does not
# break the MCP server — but it does break `fulcrum` itself.
case ":$PATH:" in
	*":$bin_dir:"*) ;;
	*) say ""
	   say "$bin_dir is not on your PATH. Add it:"
	   say "  export PATH=\"$bin_dir:\$PATH\"" ;;
esac

say ""
say "Next: fulcrum login, then fulcrum mcp install in a project."
