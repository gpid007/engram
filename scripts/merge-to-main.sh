#!/usr/bin/env bash
# merge-to-main.sh: merge current feature branch to main, run QA, delete branch
#
# Usage: ./scripts/merge-to-main.sh
#
# Workflow:
#   1. Confirm you are on a feature branch
#   2. Rebase onto latest main (fast-forward, no merge commits)
#   3. Push feature branch — pre-push hook runs go test ./...
#   4. Merge to main (ff-only)
#   5. Push main
#   6. Delete feature branch (local + remote)

set -e

ROOT=$(git rev-parse --show-toplevel)
CURRENT=$(git rev-parse --abbrev-ref HEAD)

if [ "$CURRENT" = "main" ]; then
  echo "Already on main. Checkout a feature branch first."
  exit 1
fi

echo "Branch: $CURRENT"

# Ensure working tree is clean
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "Working tree is dirty. Commit or stash changes first."
  exit 1
fi

# Rebase onto latest main
echo "Fetching origin..."
git fetch origin main

echo "Rebasing onto origin/main..."
git rebase origin/main

# Push feature branch (pre-push hook runs QA tests here)
echo "Pushing $CURRENT (QA gate runs now)..."
git push origin "$CURRENT"

# Merge to main (fast-forward only — no merge commits)
echo "Merging $CURRENT → main..."
git checkout main
git merge --ff-only "$CURRENT"
git push origin main

# Delete feature branch
echo "Deleting $CURRENT..."
git branch -d "$CURRENT"
git push origin --delete "$CURRENT" 2>/dev/null || true

echo "Done. $CURRENT merged to main and deleted."
