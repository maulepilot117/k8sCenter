---
status: complete
priority: p3
issue_id: "232"
tags: [code-review, frontend, performance, logviewer, resource-leak]
dependencies: []
---

# LogViewer requestAnimationFrame IDs Not Tracked

## Problem Statement

`requestAnimationFrame` IDs for auto-scroll are not stored or cancelled. When N messages arrive per frame, N RAFs are queued — only one is needed. A stale RAF can also fire after component unmount, accessing disposed DOM elements.

**Location:** `frontend/islands/LogViewer.tsx` lines 152-156

## Proposed Solutions

### Option A: Coalesce into single pending RAF with ref tracking
- Store the RAF ID in a ref. Before requesting a new frame, cancel the previous one with `cancelAnimationFrame`. Cancel any pending RAF on cleanup/unmount.
- **Effort:** Low — add a ref, cancel before re-request, cancel on cleanup.
- **Risk:** Low.

## Acceptance Criteria

- [ ] Only one RAF is pending at any given time for auto-scroll
- [ ] RAF is cancelled on component unmount
- [ ] Auto-scroll behavior remains smooth and responsive
- [ ] No console errors from stale RAF callbacks

## Work Log

- 2026-03-22: Created from Phase 4A code review.
