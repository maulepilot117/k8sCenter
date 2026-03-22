---
status: pending
priority: p1
issue_id: "218"
tags: [code-review, frontend-races, websocket]
dependencies: []
---

# LogViewer tab/mode switch leaves ghost WebSocket connections

## Problem Statement

Two related race conditions in LogViewer WS lifecycle:

1. **Tab switch**: switching from tab A to tab B does not close tab A's WS. Tab A's `onmessage` keeps firing, mutating signal state and causing re-renders for an invisible tab. On switch back, `connectWS(0)` creates a new WS but the old one is a ghost (no reference to close it).

2. **Mode switch (follow→snapshot)**: `ws.close()` is async but `fetchSnapshot()` is called immediately after. WS `onmessage` can still fire between `.close()` call and actual close event, appending streaming lines into what user expects is a static snapshot.

**Location:** `frontend/islands/LogViewer.tsx` lines 220-247 (useEffect), 231-238 (mode switch), 98-186 (connectWS)

## Proposed Solutions

### Option A: Mode guard + per-effect WS cleanup
- In `onmessage`, check `mode.value === "follow"` before processing (bail if snapshot mode)
- `connectWS` returns the WS reference; effect cleanup closes the specific WS it opened
- Track active WS per tab in a `useRef<Map<number, WebSocket>>` separate from signal state
- **Effort:** Medium
- **Risk:** Low

## Acceptance Criteria
- [ ] Tab switch closes previous tab's WS connection
- [ ] Mode switch to snapshot stops processing WS messages immediately
- [ ] No ghost WS connections after tab/mode switching

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-22 | Created | Frontend races review of PR #59 |
