---
status: complete
priority: p3
issue_id: "019"
tags: [code-review, quality, auth]
dependencies: []
---

# Use Sentinel Errors Instead of String Comparison

## Problem Statement
Error handling in some auth code uses `err.Error()` string comparison which is fragile and can break if error messages change. Sentinel errors (`var ErrNotFound = errors.New(...)`) with `errors.Is()` are more robust.

## Findings
- **Agent**: code-simplicity-reviewer (re-review of PR #2)
- **Location**: `backend/internal/auth/local.go`, `backend/internal/server/handle_auth.go`
- **Evidence**: Error strings compared rather than typed/sentinel errors

## Proposed Solutions

### Option A: Define sentinel errors in auth package
- Add `var ErrInvalidCredentials`, `ErrUserNotFound`, `ErrDuplicateUser` etc.
- Use `errors.Is()` in handlers
- **Pros**: Type-safe, refactor-proof
- **Cons**: Minor refactor
- **Effort**: Small
- **Risk**: Low

## Recommended Action
Option A — good hygiene, easy win. Can be done in a future step.

## Acceptance Criteria
- [ ] Sentinel errors defined in `auth` package
- [ ] Handlers use `errors.Is()` instead of string checks
- [ ] Tests updated

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Code simplicity reviewer flagged |

## Resources
- PR #2: feat/step-2-auth
