---
status: pending
priority: p1
issue_id: "219"
tags: [code-review, frontend-races, xterm]
dependencies: []
---

# PodTerminal DOM race + close-during-import zombie sessions

## Problem Statement

Two related issues:

1. **DOM race**: `openSession` adds session to signal (line 101), then uses `requestAnimationFrame` to find `document.getElementById(\`term-\${id}\`)`. If user clicks "Open Terminal" twice rapidly, Preact batches renders and the first session's container may be `hidden` when xterm opens it, measuring 0x0. The terminal renders as a black void.

2. **Close-during-import**: `openSession` is async with three dynamic `import()` calls (lines 75-77). If user closes the tab before imports resolve, `closeSession` finds nothing (session not yet in signal). When imports resolve, a zombie session + WS is created that can never be closed.

**Location:** `frontend/islands/PodTerminal.tsx` lines 66-181 (openSession), 100-107 (RAF lookup), 189-202 (closeSession)

## Proposed Solutions

### Option A: Ref-based init + cancellation token
- Replace RAF-based DOM lookup with ref callback on the `<div>` element
- Add cancellation token checked after each `import()` call
- Store pending opens in a ref so closeSession can cancel them
- **Effort:** Medium
- **Risk:** Low

## Acceptance Criteria
- [ ] Terminal initializes correctly even when opening multiple sessions rapidly
- [ ] Closing a session during import cancels the pending initialization
- [ ] No zombie WS connections after close-during-import

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-22 | Created | Frontend races review of PR #59 |
