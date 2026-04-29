# Git Flow Branching Strategy

Engram follows **Git Flow** for organized, predictable releases and feature development.

## Branch Structure

```
main (production-ready, tagged releases)
  ├── hotfix/* (critical production fixes)
  └── release/* (release preparation)

develop (integration, daily work)
  ├── feature/* (new features)
  ├── fix/* (bug fixes)
  ├── docs/* (documentation)
  ├── refactor/* (code cleanup)
  └── perf/* (performance improvements)
```

## Branch Rules

| Branch | Source | PR Required | Status Checks | Auto-delete |
|--------|--------|------------|---------------|------------|
| `main` | `release/*`, `hotfix/*` | ✅ Yes | Full suite + signed commits | On merge |
| `develop` | `feature/*`, `fix/*`, `release/*`, `hotfix/*` | ✅ Yes | Test + lint | No |
| `feature/*` | `develop` | ✅ Yes | Test + lint | Yes |
| `fix/*` | `develop` | ✅ Yes | Test + lint | Yes |
| `docs/*` | `develop` | ✅ Yes (optional) | None | Yes |
| `refactor/*` | `develop` | ✅ Yes | Test + lint | Yes |
| `hotfix/*` | `main` | ✅ Yes | Full suite | Yes |
| `release/*` | `develop` | ✅ Yes | Full suite | Yes |

## Workflow by Task Type

### Feature Development

```bash
git checkout develop
git pull origin develop
git checkout -b feature/short-description
# ... work ...
git commit -m "feat(scope): description"
git push origin feature/short-description
# → Create PR to develop
# → Wait for approval + CI pass
# → Merge and auto-delete
```

### Bug Fixes

```bash
git checkout develop
git pull origin develop
git checkout -b fix/issue-description
# ... fix the bug ...
git commit -m "fix(scope): description"
git push origin fix/issue-description
# → Create PR to develop
# → Wait for approval + CI pass
# → Merge and auto-delete
```

### Releases

```bash
git checkout -b release/v0.3.0 develop
# Update CHANGELOG.md, bump version
git commit -m "chore: release v0.3.0"
git push origin release/v0.3.0
# → Create PR to main
# → Full CI suite runs
# → Merge to main
# → Tag: git tag -a v0.3.0 -m "v0.3.0"
# → Also merge back to develop
```

### Hotfixes (Critical Production Bugs)

```bash
git checkout -b hotfix/critical-issue main
# Fix the critical issue
git commit -m "fix: critical issue"
git push origin hotfix/critical-issue
# → Create PR to main
# → Full CI suite + code review required
# → Merge to main + tag v0.3.1
# → Cherry-pick/merge back to develop
```

## Commit Message Conventions

Engram uses **Conventional Commits**:

```
type(scope): subject (lowercase, imperative, 50 chars max)

Optional body (70 char wrap, explain "why", not "what")

Optional footer (closes #123)
```

**Valid types:**
- `feat` — New feature
- `fix` — Bug fix
- `docs` — Documentation
- `perf` — Performance improvement
- `refactor` — Code reorganization
- `chore` — Version bump, dependencies, tooling
- `test` — Test additions/changes
- `style` — Code style (formatting, semicolons)
- `ci` — CI/CD changes

**Example:**
```
feat(cli): add memory deletion with semantic search

Previously users could only delete by ID. Now they can
use `engram rm -q "pattern"` to find and delete by semantic search.

Closes #42
```

## Git Hooks (Automated Enforcement)

The following hooks run automatically:

### `pre-commit`
- Runs `go fmt ./...` (fails if code not formatted)
- Runs `go vet ./...` (fails on linting errors)
- Warns if CHANGELOG.md not updated when .go files changed
- Bypass: `GIT_SKIP_HOOKS=1 git commit ...`

### `commit-msg`
- Validates conventional commit format
- Rejects messages < 10 characters
- Allows: `Merge`, `Revert` (git-generated)
- Provides format hints on failure

### `pre-push`
- Blocks direct push to `main` (use PR instead)
- Blocks direct push to `develop` (use PR instead)
- Allows: `feature/*`, `fix/*`, `docs/*`, `hotfix/*`, `release/*`
- Bypass: `GIT_SKIP_HOOKS=1 git push ...`

### `post-merge`
- Auto-updates hooks from `scripts/git-hooks/` if they changed
- Runs `go mod tidy` if go.mod/go.sum changed
- Warns if CHANGELOG.md not updated on main←develop merge

## GitHub Branch Protection

### Main Branch
- ✅ Require pull request review (1 approval)
- ✅ Require status checks: `go test`, `go build`, `lint`
- ✅ Require signed commits
- ✅ Auto-delete head branch on merge
- ✅ Dismiss stale PR approvals when new commits pushed

### Develop Branch
- ✅ Require pull request review (1 approval)
- ✅ Require status checks: `go test`, `lint` (fast checks only)
- ✅ Auto-delete head branch on merge
- ✅ Dismiss stale PR approvals when new commits pushed

## Installation

Run this once to set up local git hooks:

```bash
bash scripts/install-hooks.sh
```

Verify hooks work:

```bash
bash scripts/test-hooks.sh
```

## FAQ

**Q: Can I push directly to main?**
A: No. The pre-push hook blocks it. Create a `release/*` or `hotfix/*` branch and use a PR.

**Q: My commit message got rejected. What format do I use?**
A: Use conventional commits: `type(scope): message`. Example: `feat(cli): add find command`

**Q: How do I bypass the hooks?**
A: Set `GIT_SKIP_HOOKS=1` before your command (use sparingly):
```bash
GIT_SKIP_HOOKS=1 git commit -m "..."
```

**Q: How often should we release?**
A: Release from `develop` when features are stable. Tag on `main`. Hotfixes from `main` as-needed.

**Q: What if I merged to develop and forgot to update CHANGELOG.md?**
A: The `post-merge` hook will warn you. Amend with the CHANGELOG update and force-push your PR (if not merged yet), or create a follow-up commit.
