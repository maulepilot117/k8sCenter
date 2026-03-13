---
status: pending
priority: p1
issue_id: "072"
tags: [code-review, frontend, websocket, data-integrity]
dependencies: []
---

# REST-to-WS Event Gap — Lost Events Cause Stale UI

## Problem Statement
In `ResourceTable.tsx`, the REST fetch and WebSocket subscription are set up in separate `useEffect` hooks. Events that occur between the REST response and the WS subscription being established are permanently lost, causing the UI to show stale data until the next manual refresh or re-fetch.

## Findings
- `ResourceTable.tsx:66-69` — REST fetch effect fires on namespace change
- `ResourceTable.tsx:72-113` — WS subscription effect fires separately
- Events during the gap (REST complete → WS connected + subscribed) are lost
- This is especially visible during rapid namespace switching or high-churn clusters

## Proposed Solutions

### Option A: Fetch after WS subscription is established
- **Pros:** Guaranteed no gap — WS is live before REST data arrives, any concurrent events are captured
- **Cons:** Requires restructuring effects, need to handle duplicate events (WS ADDED + REST data overlap)
- **Effort:** Medium
- **Risk:** Low — need deduplication by UID which is already in place

### Option B: Include resourceVersion in REST response, use it for WS watch
- **Pros:** Standard k8s pattern (list then watch from resourceVersion), zero gap guaranteed
- **Cons:** Requires backend changes to return resourceVersion and support watch-from-version
- **Effort:** Large
- **Risk:** Low but more work

## Technical Details
- **Affected files:** `frontend/islands/ResourceTable.tsx`, potentially `frontend/lib/ws.ts`

## Acceptance Criteria
- [ ] No events are lost between REST fetch and WS subscription
- [ ] UI reflects the latest state without requiring manual refresh
- [ ] Solution handles namespace switching without stale data
