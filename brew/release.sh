#!/usr/bin/env bash
set -euo pipefail

# Run from repo root: ./brew/release.sh [version]
# Requires: goreleaser, GITHUB_TOKEN, HOMEBREW_TAP_GITHUB_TOKEN

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
VERSION_FILE="$SCRIPT_DIR/VERSION"

cd "$ROOT_DIR"

command -v goreleaser >/dev/null || {
  echo "ERROR: goreleaser is not installed"
  exit 1
}

: "${GITHUB_TOKEN:=$(gh auth token)}"
: "${HOMEBREW_TAP_GITHUB_TOKEN:=$(gh auth token)}"
export GITHUB_TOKEN HOMEBREW_TAP_GITHUB_TOKEN

if [[ $# -ge 1 ]]; then
  VERSION="$1"
else
  VERSION="$(cat "$VERSION_FILE")"
fi

VERSION="${VERSION#v}"
TAG="v$VERSION"

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
  echo "ERROR: Version must look like 0.1.0 or 0.1.0-beta.1"
  exit 1
fi

echo "==> Release version: $VERSION"
echo "==> Git tag: $TAG"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "ERROR: Working tree is dirty. Commit or stash changes before release."
  git status --short
  exit 1
fi

git fetch --tags origin

if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "ERROR: Tag $TAG already exists locally."
  exit 1
fi

if git ls-remote --tags origin "refs/tags/$TAG" | grep -q "$TAG"; then
  echo "ERROR: Tag $TAG already exists on origin."
  exit 1
fi

echo "==> Checking GoReleaser config"
goreleaser check --config brew/.goreleaser.yaml

echo "==> Creating tag $TAG"
git tag -a "$TAG" -m "Release $TAG"

echo "==> Pushing tag $TAG"
git push origin "$TAG"

echo "==> Running GoReleaser"
goreleaser release --config brew/.goreleaser.yaml --clean