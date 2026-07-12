#!/usr/bin/env bash
# Run a local RepoGuide report without installing the CLI:
# curl -fsSL https://repoguide.dev/report.sh | sh
set -euo pipefail

REPO="repoguide-dev/repoguide-releases"
BIN="repoguide"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "error: unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin) ;;
  mingw*|msys*|cygwin*)
    os=windows
    [ "$arch" = amd64 ] || { echo "error: windows builds are amd64-only" >&2; exit 1; }
    ;;
  *) echo "error: unsupported OS: $os" >&2; exit 1 ;;
esac

version="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep -m1 '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/')"
[ -n "$version" ] || { echo "error: could not determine latest version" >&2; exit 1; }

if [ "$os" = windows ]; then
  archive="repoguide_${version}_${os}_${arch}.zip"
  BIN="repoguide.exe"
else
  archive="repoguide_${version}_${os}_${arch}.tar.gz"
fi
base_url="https://github.com/$REPO/releases/download/v${version}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "==> Downloading temporary RepoGuide $version"
curl -fsSL "$base_url/$archive" -o "$tmpdir/$archive"
curl -fsSL "$base_url/checksums.txt" -o "$tmpdir/checksums.txt"

echo "==> Verifying checksum"
(cd "$tmpdir" && grep " $archive\$" checksums.txt | shasum -a 256 -c -)

if [ "$os" = windows ]; then
  unzip -q "$tmpdir/$archive" -d "$tmpdir"
else
  tar -xzf "$tmpdir/$archive" -C "$tmpdir"
fi

echo "==> Running local report (nothing installed)"
"$tmpdir/$BIN" report
