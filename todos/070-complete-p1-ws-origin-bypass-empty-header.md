---
status: pending
priority: p1
issue_id: "070"
tags: [code-review, backend, security, websocket]
dependencies: []
---

# WebSocket Origin Bypass on Empty Header (CSWSH)

## Problem Statement
The Cross-Site WebSocket Hijacking (CSWSH) check in `handle_ws.go` only validates the Origin header when it is present. An attacker can bypass this check by sending a request with no Origin header, enabling cross-site WebSocket hijacking attacks.

## Findings
- `handle_ws.go:26` checks `if origin != ""` before validating — empty origin passes through
- WebSocket connections from non-browser clients (or crafted browser requests) may omit Origin
- This allows unauthorized cross-site connections to the WebSocket endpoint

## Proposed Solutions

### Option A: Require Origin header for browser-initiated connections
- **Pros:** Prevents CSWSH, follows security best practices
- **Cons:** May need to allow empty Origin for non-browser clients (e.g., CLI tools) with alternative auth
- **Effort:** Small
- **Risk:** Low — JWT auth is still required, this is defense-in-depth

## Technical Details
- **Affected files:** `backend/internal/server/handle_ws.go`

## Acceptance Criteria
- [ ] Empty Origin header is rejected (or explicitly handled with alternative validation)
- [ ] Valid Origin headers are still accepted
- [ ] Non-browser clients can still connect via a documented mechanism
