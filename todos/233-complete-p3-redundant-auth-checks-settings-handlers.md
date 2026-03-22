---
status: complete
priority: p3
issue_id: "233"
tags: [code-review, backend, dead-code, refactor]
dependencies: []
---

# Redundant Auth Checks in Settings Handlers

## Problem Statement

`handleGetAppSettings` and `handleUpdateAppSettings` manually check `auth.UserFromContext` but are already behind `RequireAdmin` middleware, which performs the same check. The redundant checks add ~12 lines of dead code that can never trigger (the middleware would reject the request first).

**Location:** `backend/internal/server/handle_settings.go` lines 91-97, 116-121

## Proposed Solutions

### Option A: Remove redundant checks
- Remove the manual `auth.UserFromContext` checks from both handlers since `RequireAdmin` middleware already guarantees an authenticated admin user.
- **Effort:** Low — delete ~12 lines.
- **Risk:** Low — middleware already enforces the invariant.

## Acceptance Criteria

- [ ] Redundant `UserFromContext` checks are removed from both handlers
- [ ] RequireAdmin middleware continues to protect both endpoints
- [ ] Settings endpoints continue to function correctly for admin users
- [ ] Non-admin requests are still rejected (by middleware)

## Work Log

- 2026-03-22: Created from Phase 4A code review.
