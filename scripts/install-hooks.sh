#!/usr/bin/env bash
# Install git hooks for Engram development

set -e

HOOKS_DIR="scripts/git-hooks"
GIT_HOOKS_DIR=".git/hooks"

echo "📦 Installing git hooks for Engram..."

# Check if we're in a git repository
if [ ! -d ".git" ]; then
  echo "❌ Error: Not a git repository. Run this from the repo root."
  exit 1
fi

# Check if hooks source directory exists
if [ ! -d "$HOOKS_DIR" ]; then
  echo "❌ Error: $HOOKS_DIR not found"
  exit 1
fi

# Create .git/hooks if it doesn't exist
mkdir -p "$GIT_HOOKS_DIR"

# Install each hook
HOOKS_INSTALLED=0
for hook_file in "$HOOKS_DIR"/*; do
  hook_name=$(basename "$hook_file")
  
  if [ ! -f "$hook_file" ]; then
    continue
  fi
  
  # Copy hook to .git/hooks
  cp "$hook_file" "$GIT_HOOKS_DIR/$hook_name"
  chmod +x "$GIT_HOOKS_DIR/$hook_name"
  
  echo "  ✅ Installed $hook_name"
  ((HOOKS_INSTALLED++))
done

if [ $HOOKS_INSTALLED -eq 0 ]; then
  echo "❌ Error: No hook files found in $HOOKS_DIR"
  exit 1
fi

echo ""
echo "✅ Successfully installed $HOOKS_INSTALLED git hooks!"
echo ""
echo "Hooks are now active and will run automatically:"
echo "  • pre-commit   — Code formatting & linting"
echo "  • commit-msg   — Commit message validation"
echo "  • pre-push     — Branch protection (no direct push to main/develop)"
echo "  • post-merge   — Auto-update hooks, tidy dependencies"
echo ""
echo "To verify hooks work correctly, run:"
echo "  bash scripts/test-hooks.sh"
echo ""
echo "To bypass hooks (not recommended):"
echo "  GIT_SKIP_HOOKS=1 git commit ..."
echo "  GIT_SKIP_HOOKS=1 git push ..."

exit 0
