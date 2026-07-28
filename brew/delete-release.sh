#!/usr/bin/env bash
set -euo pipefail

# Usage: ./brew/delete-release.sh --version 0.0.1-test

RELEASES_REPO="repoguide-dev/repoguide-releases"
TAP_REPO="repoguide-dev/homebrew-tap"
TAP_CASK="Casks/repoguide.rb"

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

# The Homebrew cask is written by GoReleaser during a release and is not
# touched here, so deleting the release it points at leaves `brew install`
# downloading assets that no longer exist. Nothing can be done about that
# except releasing again, but the operator has to know the window is open.
cask_version="$(gh api "repos/$TAP_REPO/contents/$TAP_CASK" \
  --jq '.content' 2>/dev/null | base64 -d 2>/dev/null \
  | sed -n 's/^[[:space:]]*version "\(.*\)"/\1/p' | head -1 || true)"

if [[ "$cask_version" == "$VERSION" ]]; then
  echo
  echo "WARNING: the Homebrew cask still pins $VERSION, whose assets are now gone."
  echo "         \`brew install repoguide\` will fail until a new release is published."
  echo "         Fix it now: bump brew/VERSION and push (the Release workflow"
  echo "         regenerates the cask), or run ./brew/release.sh locally."
elif [[ -n "$cask_version" ]]; then
  echo "Homebrew cask pins $cask_version, unaffected by this deletion."
fi

echo
echo "Done. Bump brew/VERSION and push to re-release; do not create the tag by hand."
