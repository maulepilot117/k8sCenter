---
status: pending
priority: p2
issue_id: 264
tags: [code-review, consistency, frontend, pr-157]
dependencies: []
---

# CniOverview Manually Implements Polling Instead of Using usePoll

## Problem Statement

`CniOverview.tsx` manually implements polling with `useEffect` + `setInterval` + `useRef` (lines 60-75), duplicating the logic in the `usePoll` hook created in this same PR. The manual implementation lacks error backoff and visibility-aware resume that `usePoll` provides.

## Findings

**Location:** `frontend/islands/CniOverview.tsx:60-75`

CniOverview's manual polling:
- No error backoff (keeps polling after failures)
- No visibility-aware resume after pause
- Separate refresh button logic adds complexity

The `usePoll` hook at `frontend/lib/hooks/use-poll.ts` handles all of this.

Likely reason: CniOverview was extracted from the old `CniStatus.tsx` which predated `usePoll`, and wasn't migrated.

## Proposed Solutions

### Option A: Refactor CniOverview to use usePoll
**Pros:** Consistent, gains error backoff, less code
**Cons:** Need to handle the manual refresh button separately
**Effort:** Small
**Risk:** Low

## Acceptance Criteria

- [ ] CniOverview uses usePoll hook
- [ ] Manual refresh button still works
- [ ] Error backoff behavior matches other islands

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Extracted from legacy code without migration |
