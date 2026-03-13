---
status: pending
priority: p2
issue_id: "079"
tags: [code-review, backend, websocket, performance]
dependencies: []
---

# Per-Event JSON Serialization (Serialize Once, Not Per-Client)

## Problem Statement
The WebSocket hub serializes each event to JSON individually for every matching client subscription. If 50 clients subscribe to pods and a pod update fires, the same object is serialized 50 times. This wastes CPU and memory.

## Findings
- Hub iterates clients and calls `json.Marshal` (or equivalent) per client per event
- High-churn resources (pods, events) with many subscribers amplify the waste
- Should serialize once and send the same `[]byte` to all matching clients

## Proposed Solutions

### Option A: Pre-serialize the event message before broadcasting to clients
- **Pros:** O(1) serialization instead of O(clients), significant CPU savings
- **Cons:** Minimal refactor needed
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go`

## Acceptance Criteria
- [ ] Each event is serialized to JSON at most once, regardless of subscriber count
