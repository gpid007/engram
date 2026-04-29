#!/usr/bin/env bash
# install-hooks.sh: copy tracked hooks into .git/hooks/
# Run once after cloning: bash scripts/install-hooks.sh

set -e

ROOT=$(git rev-parse --show-toplevel)
HOOKS_SRC="$ROOT/scripts/git-hooks"
HOOKS_DST="$ROOT/.git/hooks"

for hook in "$HOOKS_SRC"/*; do
  name=$(basename "$hook")
  cp "$hook" "$HOOKS_DST/$name"
  chmod +x "$HOOKS_DST/$name"
  echo "Installed $name"
done

echo "All hooks installed."
