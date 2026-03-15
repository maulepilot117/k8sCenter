---
status: complete
priority: p3
issue_id: "022"
tags: [code-review, quality, auth]
dependencies: []
---

# CreateUser/CreateFirstUser Missing Context Parameter

## Problem Statement
`CreateUser` and `CreateFirstUser` don't accept a `context.Context`. The internal `createUser` does a blocking semaphore acquire (`p.hashSem <- struct{}{}`) without context, unlike `Authenticate` which uses `select` with `ctx.Done()`. If the semaphore is full and the request is cancelled, `createUser` blocks indefinitely.

## Findings
- **Agent**: pattern-recognition-specialist
- **Location**: `backend/internal/auth/local.go`, `createUser()` line 122
- **Evidence**: `Authenticate` uses context-aware select; `createUser` does bare channel send

## Proposed Solutions

### Option A: Add context to CreateUser/CreateFirstUser
- Pass context through, use `select` with `ctx.Done()` like `Authenticate`
- **Pros**: Consistent cancellation, request-scoped timeout
- **Cons**: API change
- **Effort**: Small
- **Risk**: Low

### Option B: Defer (accept for MVP)
- User creation is infrequent (setup + admin operations)
- **Effort**: None

## Recommended Action
Option B for MVP — user creation is rare. Fix when adding OIDC/LDAP user sync.

## Acceptance Criteria
- [ ] Triage decision made

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Pattern specialist flagged inconsistency |

## Resources
- PR #2: feat/step-2-auth
