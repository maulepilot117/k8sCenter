---
status: complete
priority: p2
issue_id: "021"
tags: [code-review, testing, auth]
dependencies: []
---

# Missing Handler-Level Integration Tests

## Problem Statement
The HTTP handlers (`handle_auth.go`, `handle_setup.go`) are the integration point of all components but have zero test coverage. Unit tests exist for auth, JWT, sessions, middleware — but the wiring and orchestration in handlers is untested. Both the architecture-strategist and pattern-recognition-specialist flagged this independently.

## Findings
- **Agents**: architecture-strategist, pattern-recognition-specialist
- **Location**: `backend/internal/server/handle_auth.go`, `handle_setup.go`
- **Evidence**: No `handle_*_test.go` files exist; middleware tests use `httptest` showing the pattern is established

## Proposed Solutions

### Option A: Add httptest-based handler tests
- Create `handle_auth_test.go` with fully wired `Server` using test helpers
- Test: setup init, login, refresh, logout, /auth/me flows
- **Pros**: Validates full middleware chain, route registration, cookie handling, audit logging
- **Cons**: Requires mock k8s client for /auth/me (RBAC summary)
- **Effort**: Medium
- **Risk**: Low

## Recommended Action
Option A — add as part of Step 3 or as a standalone testing pass.

## Acceptance Criteria
- [ ] `handle_auth_test.go` exists with tests for login, refresh, logout flows
- [ ] `handle_setup_test.go` exists with test for first-user setup
- [ ] Tests use `httptest.Server` with real middleware chain

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Both arch + pattern agents flagged independently |

## Resources
- PR #2: feat/step-2-auth
