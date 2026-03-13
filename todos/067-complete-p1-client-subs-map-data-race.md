---
status: pending
priority: p1
issue_id: "067"
tags: [code-review, backend, websocket, concurrency]
dependencies: []
---

# Data Race on client.subs Map in WebSocket Hub

## Problem Statement
`client.subs` map is read from the hub goroutine (`subIDForKey` in hub.go) and written from the readPump goroutine (`handleSubscribe` in client.go). Go maps are not safe for concurrent read/write — this will cause a fatal panic under load.

## Findings
- `hub.go` hub goroutine iterates over `client.subs` to find matching subscriptions for event broadcasting
- `client.go` readPump goroutine writes to `client.subs` when handling subscribe/unsubscribe messages
- No mutex or channel-based synchronization protects the map
- Under concurrent load (multiple subscriptions + events), this will panic with `concurrent map read and map write`

## Proposed Solutions

### Option A: Move subscription management into the hub goroutine via channels
- **Pros:** Follows existing hub pattern (all map mutations via channels), no locks needed
- **Cons:** Slightly more complex message routing, subscribe becomes async
- **Effort:** Medium
- **Risk:** Low — proven pattern already used for register/unregister

### Option B: Protect client.subs with sync.RWMutex
- **Pros:** Simple, direct fix
- **Cons:** Adds lock contention, diverges from hub's channel-based pattern
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go`, `backend/internal/websocket/client.go`
- **Components:** WebSocket hub, client subscription management

## Acceptance Criteria
- [ ] `client.subs` is never accessed concurrently from multiple goroutines without synchronization
- [ ] `go test -race ./internal/websocket/...` passes
- [ ] Subscription and event delivery still work correctly under load
