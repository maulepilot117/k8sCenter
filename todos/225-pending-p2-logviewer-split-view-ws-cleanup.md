---
status: pending
priority: p2
issue_id: "225"
tags: [code-review, frontend, resource-leak, logviewer, websocket]
dependencies: []
---

# LogViewer Split View WebSocket Cleanup

## Problem Statement

Toggling split-view off does not close the second tab's WebSocket. The WS keeps streaming log data, burning bandwidth and server resources for an invisible panel. The `splitView.value` signal is not in the effect dependency array, so the cleanup effect does not re-run when split view is toggled off.

**Location:** `frontend/islands/LogViewer.tsx` lines 400-416

## Proposed Solutions

### Option A: Close split tab's WS in onChange handler
- In the split-view toggle's `onChange` handler, when unchecking (disabling split view), explicitly close the second tab's WebSocket connection and clear its log buffer.
- **Effort:** Low — add WS close call in the toggle handler.
- **Risk:** Low.

### Option B: Add splitView to effect dependencies
- Include `splitView.value` in the effect dependency array so the cleanup function runs when split view is toggled, closing the WS automatically.
- **Effort:** Low.
- **Risk:** Low — but need to ensure the effect re-establishes connections correctly when re-enabled.

## Acceptance Criteria

- [ ] Toggling split view off closes the second tab's WebSocket connection
- [ ] No bandwidth is consumed by an invisible split panel
- [ ] Toggling split view back on re-establishes the WebSocket correctly
- [ ] Primary tab's WebSocket is unaffected by split view toggling

## Work Log

- 2026-03-22: Created from Phase 4A code review.
