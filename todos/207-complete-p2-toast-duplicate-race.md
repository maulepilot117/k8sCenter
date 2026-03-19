---
status: pending
priority: p2
issue_id: "207"
tags: [code-review, frontend-races, ui]
dependencies: []
---

# Toast auto-dismiss doesn't reset on duplicate messages

## Problem Statement

The toast auto-dismiss effect depends on `[toast.value]` as a dependency. If the
same success/error message fires twice (e.g., user deletes two resources
quickly), the second `toast.value = { message: "Deleted foo", type: "success" }`
may be referentially different (new object), but if the exact same message string
is reused and Preact's signal comparison treats it as unchanged, the timeout
won't reset and the toast may disappear prematurely.

**Location:** `frontend/islands/ResourceTable.tsx:280-286`

## Findings

- `useEffect` dependency is `[toast.value]` — Preact signals compare by reference
- New object `{ message, type }` is created each time, so in practice this is
  likely fine since each `toast.value = {...}` creates a new reference
- However, the pattern is fragile — if the signal implementation changes or
  the same object reference is reused, the effect won't re-run

## Proposed Solutions

### Option A: Add a counter/timestamp to toast to guarantee uniqueness
- **Pros:** Bulletproof — always resets timeout
- **Cons:** Slightly more complex
- **Effort:** Small
- **Risk:** Low

### Option B: Accept current behavior (new object = new reference)
- **Pros:** No change needed
- **Cons:** Fragile assumption about signal reference equality
- **Effort:** None
- **Risk:** Low

## Recommended Action

Option A — add a timestamp field to the toast signal for guaranteed uniqueness.

## Acceptance Criteria

- [ ] Rapid successive actions each show their own 4-second toast
- [ ] Previous toast timeout is properly cleared when new toast appears

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found during PR #46 frontend races review |
