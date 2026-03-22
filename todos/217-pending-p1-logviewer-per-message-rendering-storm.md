---
status: pending
priority: p1
issue_id: "217"
tags: [code-review, performance, frontend]
dependencies: []
---

# LogViewer per-message rendering storm — O(n) copy + full DOM rebuild per log line

## Problem Statement

Every incoming WebSocket log message triggers: (1) full array spread of all lines `[...state.lines, msg.data]`, (2) full `tabStates` array clone via `updateTabState`, (3) Preact signal notification causing re-render, (4) `renderLogContent` re-processes all lines through ansi_to_html + search regex, (5) full innerHTML replacement on the `<pre>` element.

At 100 lines/sec (common for debug output), this produces 100 full array copies of 10K strings per second + 100 re-renders rebuilding 2MB of HTML. The browser will freeze.

**Location:** `frontend/islands/LogViewer.tsx` lines 91-95 (updateTabState), 134-157 (onmessage), 268-290 (renderLogContent)

## Proposed Solutions

### Option A: RAF-batched message coalescing + cached ANSI HTML
- Accumulate incoming lines in a `useRef` buffer
- Flush to signal once per `requestAnimationFrame` (~60 updates/sec max)
- Pre-convert lines to HTML at ingestion time, store `{raw, html}` tuples
- Search operates on `raw`, display uses pre-computed `html`
- **Pros:** 10-20x improvement under load, eliminates redundant ANSI conversion
- **Effort:** Medium
- **Risk:** Low

## Acceptance Criteria
- [ ] Log messages batched via RAF (max 60 signal updates/sec)
- [ ] ANSI HTML cached per line, not recomputed on every render
- [ ] No visible jank with 100+ lines/sec log output
- [ ] Search still works on raw text

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-22 | Created | Performance oracle + frontend races review of PR #59 |
