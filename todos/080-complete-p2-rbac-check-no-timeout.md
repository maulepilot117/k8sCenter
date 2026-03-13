---
status: pending
priority: p2
issue_id: "080"
tags: [code-review, backend, websocket, architecture]
dependencies: []
---

# context.Background() in WebSocket RBAC Check — No Timeout

## Problem Statement
The RBAC access check during WebSocket subscription uses `context.Background()` with no timeout. If the k8s API server is slow or unreachable, this call blocks indefinitely, tying up the hub goroutine or client goroutine.

## Findings
- RBAC check in subscription handler uses `context.Background()`
- SelfSubjectAccessReview calls go to the k8s API server
- No deadline or timeout means the goroutine can hang forever
- This can cascade — if multiple subscriptions hang, goroutines pile up

## Proposed Solutions

### Option A: Use context.WithTimeout (5-10 seconds)
- **Pros:** Prevents indefinite blocking, fail-fast on API server issues
- **Cons:** None
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go` or wherever RBAC is checked for subscriptions

## Acceptance Criteria
- [ ] RBAC check has a reasonable timeout (5-10 seconds)
- [ ] Timeout results in subscription denial with a clear error message to the client
