---
status: pending
priority: p2
issue_id: "083"
tags: [code-review, backend, websocket, performance]
dependencies: []
---

# Hub Single-Threaded Broadcast Blocks Event Ingestion

## Problem Statement
The hub goroutine processes events sequentially — while broadcasting one event to all clients (iterating the client map, writing to send channels), new events queue in the events channel. Under high event throughput, the events channel can fill up and either block informer callbacks or drop events.

## Findings
- Hub `run()` loop processes one event at a time
- Broadcasting to N clients with some slow consumers takes O(N) time per event
- During broadcast, new events accumulate in the channel
- If events channel has fixed buffer, informer callbacks may block

## Proposed Solutions

### Option A: Use a worker pool for broadcasting (fan-out goroutines)
- **Pros:** Broadcasting doesn't block event ingestion
- **Cons:** More complex, need to coordinate workers
- **Effort:** Medium
- **Risk:** Medium — need to ensure ordering is acceptable

### Option B: Increase events channel buffer and accept some latency
- **Pros:** Simple, handles burst scenarios
- **Cons:** Doesn't solve the root cause, just delays it
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go`

## Acceptance Criteria
- [ ] Event ingestion is not blocked by slow broadcast to clients
- [ ] Events channel does not fill up under normal load
