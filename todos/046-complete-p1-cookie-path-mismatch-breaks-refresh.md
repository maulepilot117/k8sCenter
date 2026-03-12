---
status: complete
priority: p1
issue_id: "046"
tags: [code-review, frontend, auth, data-integrity]
dependencies: []
---

# Cookie Path Mismatch May Break Token Refresh

## Problem Statement
The Go backend sets the refresh token cookie with `Path: /api/v1/auth/refresh`. The frontend BFF proxy sits at `/api/[...path]` which means the browser sends requests to `/api/v1/auth/refresh` on the frontend origin. While the paths nominally match, the cookie's `Domain` and `SameSite` attributes are set by the backend (running on a different port in dev), which may cause the browser to reject or not attach the cookie.

## Findings
- Backend `handle_auth.go` sets cookie with `Path: "/api/v1/auth/refresh"`
- In development, backend runs on :8080 and frontend on :8000 — different origins
- The Set-Cookie header passes through the proxy, but cookie domain may not match frontend origin
- `SameSite=Strict` or `Secure` flags from the backend may prevent cookie attachment in dev
- This needs end-to-end testing to confirm the exact failure mode

Flagged by: Data Integrity Guardian (HIGH)

## Proposed Solutions

### Option A: Set cookie in the BFF proxy instead of forwarding backend's Set-Cookie
- **Pros**: Full control over cookie attributes for the frontend domain
- **Cons**: More proxy logic
- **Effort**: Medium
- **Risk**: Low

### Option B: Ensure backend cookie attributes work through the proxy
- **Pros**: Simpler, less proxy logic
- **Cons**: Requires careful testing in both dev and prod
- **Effort**: Small
- **Risk**: Medium

## Technical Details
- **Affected files**: `frontend/routes/api/[...path].ts`, `backend/internal/server/response.go`

## Acceptance Criteria
- [ ] Token refresh works in development (frontend :8000, backend :8080)
- [ ] Token refresh works in production (same-origin or configured domain)
- [ ] Cookie Domain and SameSite attributes are correct for the deployment scenario
