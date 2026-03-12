---
status: complete
priority: p2
issue_id: "006"
tags: [code-review, security, concurrency]
dependencies: []
---

# TOCTOU Race Condition in Setup Endpoint

## Problem Statement
`handleSetupInit` checks `UserCount() > 0` and then calls `CreateUser()` in separate operations. Two concurrent requests with different usernames could both pass the check and create two admin accounts.

## Findings
- **Source**: Security agent (Finding 9), Patterns agent
- **File**: `backend/internal/server/routes.go:96-145`

## Proposed Solutions

### Option A: Atomic CreateFirstUser method (Recommended)
Add `CreateFirstUser(username, password string) (*User, error)` to `LocalProvider` that checks user count and creates under the same write lock. Returns error if any users exist.
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Concurrent setup requests cannot create multiple admin accounts
- [ ] Only the first request succeeds; subsequent requests get 410
