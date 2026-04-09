---
status: pending
priority: p2
issue_id: 261
tags: [code-review, frontend, memory-leak, pr-157]
dependencies: []
---

# usePoll AbortController Not Connected to Fetch

## Problem Statement

The `usePoll` hook creates an `AbortController` (line 45) and calls `controller.abort()` on cleanup (line 111), but never passes the signal to `apiGet()`. This means:
1. The abort call is a no-op — in-flight requests are not cancelled on unmount
2. Unmounted components may have stale signal updates (minor with Preact signals)

## Findings

**Location:** `frontend/lib/hooks/use-poll.ts:45-49,111`

```typescript
const controller = new AbortController(); // created
// ...
const resp = await apiGet<T>(url); // signal NOT passed
// ...
controller.abort(); // no-op — signal disconnected
```

`apiGet()` wraps `api()` which uses `fetch()` internally but doesn't accept an abort signal parameter.

## Proposed Solutions

### Option A: Thread signal through apiGet (Recommended)
**Pros:** Proper cleanup, cancels in-flight requests
**Cons:** Requires adding optional signal param to api.ts
**Effort:** Medium
**Risk:** Low

### Option B: Remove dead AbortController
**Pros:** No dead code
**Cons:** Doesn't fix the underlying issue (no cancellation)
**Effort:** Small
**Risk:** Low — Preact signals don't throw on unmounted updates

## Recommended Action

Option B for now (remove dead code). Option A as a follow-up if needed.

## Acceptance Criteria

- [ ] AbortController either connected to fetch or removed
- [ ] No dead variables in usePoll

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | apiGet doesn't accept abort signal |
