---
status: pending
priority: p2
issue_id: "078"
tags: [code-review, backend, websocket, data-integrity]
dependencies: []
---

# Event Channel Drops with No Client Notification

## Problem Statement
When a client's send channel is full (slow consumer), the hub drops the event silently. The client has no way to know it missed events, leading to permanent state divergence between the client's view and actual cluster state.

## Findings
- Hub uses `select { case client.send <- msg: default: }` pattern — drops on full channel
- Client never receives notification that events were dropped
- Client's local resource list becomes permanently stale
- Only manual refresh or page reload recovers the correct state

## Proposed Solutions

### Option A: Send a "resync_required" message to the client when events are dropped
- **Pros:** Client can auto-refetch via REST, self-healing
- **Cons:** Additional message type to handle
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go`, `frontend/lib/ws.ts`, `frontend/islands/ResourceTable.tsx`

## Acceptance Criteria
- [ ] When events are dropped for a client, the client receives a notification
- [ ] Frontend handles the notification by re-fetching resources via REST
