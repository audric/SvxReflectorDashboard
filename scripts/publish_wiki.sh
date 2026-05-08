#!/usr/bin/env bash
# Publish the local wiki/ folder to the GitHub wiki repo.
#
# Clones <origin>.wiki.git into a temp dir, mirrors wiki/ over it,
# and pushes any diff. Exits cleanly when there's nothing to ship.
#
# Usage:
#   scripts/publish_wiki.sh
#   scripts/publish_wiki.sh "Custom commit message"
#
# Fails fast if remote has commits we don't (manual edits via the
# GitHub wiki UI) — resolve those before re-running.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
WIKI_SRC="$REPO_ROOT/wiki"

if [ ! -d "$WIKI_SRC" ]; then
  echo "error: $WIKI_SRC does not exist" >&2
  exit 1
fi

ORIGIN_URL="$(git -C "$REPO_ROOT" remote get-url origin)"
WIKI_URL="${ORIGIN_URL%.git}.wiki.git"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Cloning $WIKI_URL ..."
git clone --depth 50 --quiet "$WIKI_URL" "$TMP_DIR/wiki"

echo "Mirroring $WIKI_SRC -> wiki repo ..."
rsync -a --delete --exclude='.git' "$WIKI_SRC/" "$TMP_DIR/wiki/"

cd "$TMP_DIR/wiki"

if git diff --quiet && git diff --cached --quiet; then
  echo "No changes to publish."
  exit 0
fi

echo
echo "Pending changes:"
git status --short

git add -A
COMMIT_MSG="${1:-Sync wiki with main repo}"
git commit -m "$COMMIT_MSG" --quiet

echo "Pushing ..."
git push --quiet origin HEAD
echo "Published."
