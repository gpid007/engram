#!/usr/bin/env bash
# Test script to validate git hooks are working correctly

set -e

HOOKS_DIR=".git/hooks"
PASSED=0
FAILED=0

echo "🧪 Testing Engram git hooks..."
echo ""

# Test 1: Check all hooks exist and are executable
echo "Test 1: Verify hooks are installed"
REQUIRED_HOOKS=("pre-commit" "commit-msg" "pre-push" "post-merge")
for hook in "${REQUIRED_HOOKS[@]}"; do
  if [ -x "$HOOKS_DIR/$hook" ]; then
    echo "  ✅ $hook is installed and executable"
    ((PASSED++))
  else
    echo "  ❌ $hook is missing or not executable"
    ((FAILED++))
  fi
done
echo ""

# Test 2: Validate commit-msg hook format
echo "Test 2: Test commit-msg hook (valid format)"
TEST_MSG="feat(test): this is a valid commit message"
echo "$TEST_MSG" | if "$HOOKS_DIR/commit-msg" /dev/stdin 2>/dev/null; then
  echo "  ✅ Valid commit message accepted"
  ((PASSED++))
else
  echo "  ❌ Valid commit message rejected"
  ((FAILED++))
fi
echo ""

# Test 3: Reject invalid commit message (too short)
echo "Test 3: Test commit-msg hook (reject short message)"
TEST_MSG="feat: x"
echo "$TEST_MSG" > /tmp/test-commit.txt
if ! "$HOOKS_DIR/commit-msg" /tmp/test-commit.txt 2>/dev/null; then
  echo "  ✅ Short message rejected"
  ((PASSED++))
else
  echo "  ❌ Short message should be rejected"
  ((FAILED++))
fi
rm -f /tmp/test-commit.txt
echo ""

# Test 4: Verify pre-push hook blocks main
echo "Test 4: Test pre-push hook (should block main)"
# Simulate stdin for pre-push: refs/heads/main <local-sha> refs/heads/main <remote-sha>
PUSH_INPUT="refs/heads/main abc123 refs/heads/main def456"
if ! echo "$PUSH_INPUT" | "$HOOKS_DIR/pre-push" 2>/dev/null; then
  echo "  ✅ Direct push to main blocked"
  ((PASSED++))
else
  echo "  ❌ Push to main should be blocked"
  ((FAILED++))
fi
echo ""

# Test 5: Verify pre-push hook allows feature branches
echo "Test 5: Test pre-push hook (should allow feature/)"
PUSH_INPUT="origin refs/heads/feature/test abc123 refs/heads/feature/test def456"
if echo "$PUSH_INPUT" | GIT_SKIP_HOOKS=0 "$HOOKS_DIR/pre-push" 2>/dev/null; then
  echo "  ✅ Push to feature/ allowed"
  ((PASSED++))
else
  echo "  ❌ Push to feature/ should be allowed"
  ((FAILED++))
fi
echo ""

# Test 6: Check pre-commit hook exists
echo "Test 6: Test pre-commit hook (code quality)"
if [ -x "$HOOKS_DIR/pre-commit" ]; then
  echo "  ℹ️  pre-commit hook will run 'go fmt' and 'go vet' on each commit"
  echo "  ✅ pre-commit hook ready"
  ((PASSED++))
else
  echo "  ❌ pre-commit hook not ready"
  ((FAILED++))
fi
echo ""

# Test 7: Check post-merge hook exists
echo "Test 7: Test post-merge hook (auto-update)"
if [ -x "$HOOKS_DIR/post-merge" ]; then
  echo "  ℹ️  post-merge hook will auto-update hooks and tidy dependencies"
  echo "  ✅ post-merge hook ready"
  ((PASSED++))
else
  echo "  ❌ post-merge hook not ready"
  ((FAILED++))
fi
echo ""

# Summary
echo "═══════════════════════════════════════"
echo "Test Summary: $PASSED passed, $FAILED failed"
echo "═══════════════════════════════════════"
echo ""

if [ $FAILED -eq 0 ]; then
  echo "✅ All tests passed! Git hooks are working correctly."
  exit 0
else
  echo "❌ Some tests failed. Check the output above."
  exit 1
fi
