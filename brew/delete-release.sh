#!/usr/bin/env bash
set -euo pipefail

# Usage: ./brew/delete-release.sh --version 0.0.1-test

RELEASES_REPO="repoguide-dev/repoguide-releases"

VERSION=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    *) echo "Usage: $0 --version <version>"; exit 1 ;;
  esac
done

[[ -z "$VERSION" ]] && { echo "ERROR: --version is required"; exit 1; }
VERSION="${VERSION#v}"
TAG="v$VERSION"

echo "This will delete release $TAG from $RELEASES_REPO and remove its tag."
echo "Type YES to confirm:"
read -r answer
[[ "$answer" == "YES" ]] || { echo "Aborted."; exit 1; }

gh release delete "$TAG" --repo "$RELEASES_REPO" --cleanup-tag --yes 2>/dev/null || echo "No GitHub release found (already deleted)"
git tag -d "$TAG" 2>/dev/null && echo "Deleted local tag $TAG" || echo "No local tag $TAG"
git push origin ":refs/tags/$TAG" 2>/dev/null && echo "Deleted remote tag $TAG" || echo "No remote tag $TAG"

echo "Done. Run ./brew/release.sh to re-release."
