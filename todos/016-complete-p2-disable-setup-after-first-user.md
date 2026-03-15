---
status: complete
priority: p2
issue_id: "016"
tags: [code-review, security, setup]
dependencies: []
---

# Setup Endpoint Remains Active After First User Created

## Problem Statement
The `/api/v1/setup/init` endpoint is always registered and rate-limited, but returns a 409 after the first user exists. While functionally safe (atomic CreateFirstUser prevents duplicates), the endpoint still accepts and processes requests unnecessarily. A middleware guard or feature flag could disable it entirely post-setup.

## Findings
- **Agent**: security-sentinel (re-review of PR #2)
- **Location**: `backend/internal/server/handle_setup.go`, `backend/internal/server/routes.go`
- **Evidence**: Setup route always registered; relies on handler-level check

## Proposed Solutions

### Option A: Setup-guard middleware
- Add middleware that checks `LocalAuth.UserCount() > 0` and returns 404 for setup routes
- **Pros**: Endpoint disappears entirely post-setup
- **Cons**: UserCount() called on every request to setup path
- **Effort**: Small
- **Risk**: Low

### Option B: Accept current behavior (defer)
- The atomic CreateFirstUser + 409 response is already safe
- **Pros**: No code change needed
- **Cons**: Endpoint still processes requests
- **Effort**: None
- **Risk**: Low — rate limiting already applied

## Recommended Action
Option B for MVP. The current implementation is safe. Revisit when adding OIDC/LDAP setup flows.

## Technical Details
- **Affected files**: `backend/internal/server/routes.go`, `backend/internal/server/handle_setup.go`

## Acceptance Criteria
- [ ] Decide on approach during triage
- [ ] If implementing: setup endpoint returns 404 post-setup

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Already safe, optimization only |

## Resources
- PR #2: feat/step-2-auth
