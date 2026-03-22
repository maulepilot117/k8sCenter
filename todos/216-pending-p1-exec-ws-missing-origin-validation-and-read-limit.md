---
status: pending
priority: p1
issue_id: "216"
tags: [code-review, security, websocket]
dependencies: []
---

# Exec WebSocket missing origin validation and read size limit

## Problem Statement

The pod exec WebSocket handler in `pods.go` has two security gaps:
1. No origin validation — `CheckOrigin` unconditionally returns `true`, unlike all other WS endpoints which call `s.validateWSOrigin()`. Exec is the most dangerous endpoint (arbitrary command execution) but has the weakest origin checking.
2. No `conn.SetReadLimit()` — a malicious client can send arbitrarily large messages, causing unbounded memory allocation. Compare with `handle_ws_logs.go` and `handle_ws_flows.go` which both set explicit read limits.

**Location:** `backend/internal/k8s/resources/pods.go` lines 204-207 (upgrader), lines 377-405 (readPump)

## Proposed Solutions

### Option A: Add origin validation + read limit
- Move origin checking to the route level or pass a validator to the handler
- Add `conn.SetReadLimit(16384)` after upgrade (16KB is sufficient for input + resize JSON)
- **Pros:** Consistent with all other WS endpoints
- **Effort:** Small
- **Risk:** Low

## Acceptance Criteria
- [ ] Exec WS validates Origin header (matching pattern from handle_ws.go)
- [ ] Exec WS has explicit read limit (≤16KB)
- [ ] No regression in exec functionality

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-22 | Created | Security sentinel review of PR #59 |
