---
status: pending
priority: p2
issue_id: "082"
tags: [code-review, backend, security, websocket]
dependencies: []
---

# Stale RBAC on Long-Lived WebSocket Subscriptions

## Problem Statement
RBAC is checked only at subscription time. A user who loses access to a namespace (RBAC change) continues receiving events for that namespace on their existing WebSocket subscription indefinitely.

## Findings
- RBAC AccessChecker cache is 60 seconds, but that only affects new subscriptions
- Existing subscriptions are never re-validated
- User could receive events for hours after losing access
- No periodic RBAC re-check mechanism

## Proposed Solutions

### Option A: Periodic RBAC re-validation (every 5 minutes) for active subscriptions
- **Pros:** Ensures access changes are respected, configurable interval
- **Cons:** Additional API server load for periodic checks
- **Effort:** Medium
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go`, `backend/internal/websocket/client.go`

## Acceptance Criteria
- [ ] Active subscriptions are periodically re-validated against current RBAC
- [ ] Subscriptions for which the user no longer has access are removed
- [ ] Client is notified when a subscription is revoked
