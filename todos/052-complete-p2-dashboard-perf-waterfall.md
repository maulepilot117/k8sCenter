---
status: complete
priority: p2
issue_id: "052"
tags: [code-review, frontend, performance]
dependencies: ["047"]
---

# Dashboard Blocking Auth Waterfall and Duplicate Fetches

## Problem Statement
The Dashboard's `useEffect` calls `await fetchCurrentUser()` sequentially before fetching cluster data, creating a waterfall. Additionally, both Dashboard and TopBar independently fetch namespaces, causing a duplicate request.

## Findings
- `frontend/islands/Dashboard.tsx:48` — `await fetchCurrentUser()` blocks all subsequent fetches
- `frontend/islands/Dashboard.tsx:57` — Fetches namespaces
- `frontend/islands/TopBar.tsx` — Also fetches namespaces independently
- The auth check should happen at the route/middleware level, not in each island

Flagged by: Performance Oracle (P1)

## Proposed Solutions

### Option A: Move auth check to middleware, share namespace data
- **Pros**: Eliminates waterfall and duplicate fetch
- **Cons**: Requires middleware changes
- **Effort**: Medium
- **Risk**: Low

### Option B: Fire auth and data fetches in parallel
- **Pros**: Simpler change, just remove the `await` before `Promise.allSettled`
- **Cons**: Still duplicates namespace fetch
- **Effort**: Small
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/islands/Dashboard.tsx`, `frontend/islands/TopBar.tsx`

## Acceptance Criteria
- [ ] Auth check doesn't block data loading
- [ ] Namespaces fetched only once, shared across components
