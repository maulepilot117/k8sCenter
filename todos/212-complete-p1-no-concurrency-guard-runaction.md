---
status: pending
priority: p1
issue_id: "212"
tags: [code-review, frontend-races, ui]
dependencies: []
---

# No concurrency guard on runAction — double-click fires two API calls

## Problem Statement

`runAction` sets `actionLoading.value = true` and disables confirm dialog buttons,
but `handleActionClick` can call `runAction` directly (line 339) for non-confirming
actions without checking `actionLoading`. A fast double-click fires two concurrent
`executeAction` calls. The first `finally` block sets `actionLoading = false` while
the second is still in-flight, re-enabling buttons. For delete actions, the second
call 404s with a confusing error toast.

**Location:** `frontend/islands/ResourceTable.tsx:289-313, 315-340`

## Findings

- Line 339: `runAction(actionId, resource)` — no guard against concurrent execution
- `actionLoading` is only checked as a `disabled` prop on dialog buttons, not at
  the function entry point
- The confirm dialog can also be overwritten: user opens confirm for A, then opens
  kebab on B while A's action is in-flight — B's dialog replaces A's

## Proposed Solutions

### Option A: Guard both runAction and handleActionClick
```typescript
const runAction = async (...) => {
  if (actionLoading.value) return;
  actionLoading.value = true;
  // ...
};

const handleActionClick = (...) => {
  if (actionLoading.value) return;
  // ...
};
```
- **Pros:** 3 lines, eliminates the race
- **Cons:** None
- **Effort:** Small
- **Risk:** None

## Recommended Action

Option A — add the guard at both entry points.

## Acceptance Criteria

- [ ] Rapid double-click on a non-confirming action only fires one API call
- [ ] Cannot open a second action dialog while first action is in-flight

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found by frontend-races reviewer during PR #46 review |
