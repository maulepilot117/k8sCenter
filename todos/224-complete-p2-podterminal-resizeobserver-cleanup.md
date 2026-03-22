---
status: complete
priority: p2
issue_id: "224"
tags: [code-review, frontend, resource-leak, podterminal]
dependencies: []
---

# PodTerminal ResizeObserver Cleanup

## Problem Statement

ResizeObserver created in `openSession` is never disconnected. The stale observer continues to fire `fitAddon.fit()` on a disposed terminal instance, which can cause errors or undefined behavior. Additionally, `resizeTimer` is used before its `let` declaration in the closure, which is a temporal dead zone issue.

**Location:** `frontend/islands/PodTerminal.tsx` lines 112-117

## Proposed Solutions

### Option A: Store ResizeObserver on session, disconnect in closeSession
- Save the ResizeObserver reference on the session object. In `closeSession`, call `observer.disconnect()` before disposing the terminal. Move the `let resizeTimer` declaration before the closure that references it.
- **Effort:** Low — store reference, add cleanup call, reorder declaration.
- **Risk:** Low.

## Acceptance Criteria

- [ ] ResizeObserver is disconnected when session is closed
- [ ] No `fitAddon.fit()` calls fire after terminal is disposed
- [ ] `resizeTimer` declaration is properly ordered before its usage in the closure
- [ ] Opening and closing multiple sessions does not leak observers
- [ ] Terminal resize still works correctly during an active session

## Work Log

- 2026-03-22: Created from Phase 4A code review.
