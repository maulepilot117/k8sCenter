---
status: complete
priority: p1
issue_id: "164"
tags: [code-review, security, websocket, step-11]
dependencies: []
---

# WebSocket "alerts" Subscription RBAC Check Broken at Subscribe Time

## Problem Statement
The `alwaysAllowKinds` bypass in `hub.go` only applies to the 5-minute RBAC revalidation ticker, NOT to the initial subscription in `client.go`'s `handleSubscribe`. When a client subscribes to kind "alerts", the RBAC check performs `SelfSubjectAccessReview` for `list alerts` in the core API group — which will always fail since there is no "alerts" resource in Kubernetes. This means WS alert subscriptions are completely broken.

## Findings
- **Source**: Security review (C2)
- **Location**: `backend/internal/websocket/client.go` (handleSubscribe) and `backend/internal/websocket/hub.go` (alwaysAllowKinds)
- **Evidence**: `alwaysAllowKinds` is checked in `revalidateSubscriptions` but not in `handleSubscribe`

## Proposed Solutions

### Option A: Add alwaysAllowKinds check in client.go handleSubscribe (Recommended)
Before calling `c.hub.accessChecker.CanAccess(...)`, check if the normalized kind is in `alwaysAllowKinds`. If so, skip the RBAC check.
- **Pros**: Minimal change, consistent with revalidation bypass
- **Cons**: None
- **Effort**: Small
- **Risk**: None

## Acceptance Criteria
- [ ] Authenticated user can subscribe to kind "alerts" via WebSocket
- [ ] RBAC check still enforced for all other kinds
- [ ] Test verifying alerts subscription succeeds

## Resources
- PR: #17
- Files: `backend/internal/websocket/client.go`, `backend/internal/websocket/hub.go`
