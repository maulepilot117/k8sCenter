---
status: pending
priority: p1
issue_id: "071"
tags: [code-review, backend, security, websocket]
dependencies: []
---

# No Kind Allowlist for WebSocket Subscriptions

## Problem Statement
There is no explicit allowlist of subscribable resource kinds in the WebSocket handler. A client could subscribe to `"secrets"` and receive real-time secret change events, bypassing the REST API's secret masking. While secrets are excluded from informers, this is not enforced at the subscription layer — defense-in-depth is missing.

## Findings
- `handle_ws.go` / `hub.go` accept any kind string in subscription messages
- Secrets table uses `enableWS={false}` on the frontend, but backend doesn't enforce this
- If secrets were accidentally added to informers, events would be broadcast unmasked
- No validation that the subscribed kind matches a known informer resource

## Proposed Solutions

### Option A: Add explicit kind allowlist in the subscription handler
- **Pros:** Defense-in-depth, prevents future accidents, validates input
- **Cons:** Must keep allowlist in sync with informer registration
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go` or `backend/internal/server/handle_ws.go`

## Acceptance Criteria
- [ ] Subscription requests for unknown or disallowed kinds are rejected with an error message
- [ ] `"secrets"` is explicitly excluded from the allowlist
- [ ] Allowlist matches the set of informer-registered resource kinds
