---
status: complete
priority: p1
issue_id: "044"
tags: [code-review, frontend, data-integrity]
dependencies: []
---

# Login Response Type Mismatch — Frontend vs Backend

## Problem Statement
The frontend `login()` function in `frontend/lib/auth.ts` expects `res.data.user` from the login response, but the Go backend's `POST /api/v1/auth/login` returns `{accessToken, expiresIn}` with NO user field. This means login succeeds (token is stored) but the user object is never populated, breaking any UI that depends on user identity.

Similarly, `fetchCurrentUser()` expects `res.data` to be the user directly, but the backend's `GET /api/v1/auth/me` wraps the response as `{user: {...}, rbac: {...}}`, so the frontend gets the wrong shape.

## Findings
- `frontend/lib/auth.ts` login() — sets `currentUser.value = res.data.user` but backend returns `{accessToken, expiresIn}`
- `frontend/lib/auth.ts` fetchCurrentUser() — sets `currentUser.value = res.data` but backend returns `{user: {...}, rbac: {...}}`
- This means `currentUser.value` will be `undefined` after login, and the wrong shape after fetchCurrentUser
- TopBar and other components that read `currentUser.value.username` will fail

Flagged by: Data Integrity Guardian (CRITICAL), TypeScript Reviewer

## Proposed Solutions

### Option A: Fix frontend to match backend response shape
- **Pros**: No backend changes needed, quick fix
- **Cons**: None
- **Effort**: Small
- **Risk**: Low

In `login()`: After getting token, call `fetchCurrentUser()` to populate user.
In `fetchCurrentUser()`: Use `res.data.user` instead of `res.data`.

### Option B: Change backend to include user in login response
- **Pros**: Fewer round trips
- **Cons**: Requires backend change
- **Effort**: Medium
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/lib/auth.ts`
- **Backend reference**: `backend/internal/server/handle_auth.go` (login handler, me handler)

## Acceptance Criteria
- [ ] After login, `currentUser.value` contains valid `{username, role}` object
- [ ] TopBar displays correct username after login
- [ ] fetchCurrentUser correctly extracts user from `{user, rbac}` envelope
