---
status: complete
priority: p1
issue_id: "047"
tags: [code-review, frontend, data-integrity]
dependencies: []
---

# Dashboard Resource Counts Always Zero

## Problem Statement
The Dashboard island fetches full resource lists (deployments, pods, services, namespaces) and reads `.data.items?.length` to get counts. However, the Go backend returns resources as a flat array in `data` (e.g., `{data: [...], metadata: {total: 42}}`), NOT as `{data: {items: [...]}}`. The `.items` property will always be undefined, so all counts will be 0.

Additionally, fetching entire resource lists just to count them is extremely wasteful. The backend already returns `metadata.total` in list responses.

## Findings
- `frontend/islands/Dashboard.tsx:54-57` — Fetches full lists: `apiGet<{ items: unknown[] }>("/v1/deployments")`
- `frontend/islands/Dashboard.tsx:65-76` — Reads `deplRes.value.data.items?.length ?? 0` — always 0
- Backend `pkg/api/types.go` returns `{data: [...], metadata: {total: N}}` — no `.items` wrapper
- Should use `metadata.total` instead of fetching and counting client-side

Flagged by: Data Integrity Guardian (CRITICAL), Performance Oracle (P1)

## Proposed Solutions

### Option A: Use metadata.total from list responses with limit=1
- **Pros**: Minimal data transfer, correct counts, uses existing backend capability
- **Cons**: Still 4 API calls for counts
- **Effort**: Small
- **Risk**: Low

Change each fetch to `apiGet("/v1/deployments?limit=1")` and read `res.metadata.total`.

### Option B: Add a dedicated /cluster/summary endpoint
- **Pros**: Single API call for all counts
- **Cons**: Requires new backend endpoint
- **Effort**: Medium
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/islands/Dashboard.tsx`
- **Backend reference**: `backend/pkg/api/types.go` (response envelope)

## Acceptance Criteria
- [ ] Dashboard shows correct resource counts (not zero)
- [ ] Counts match actual resource numbers in cluster
- [ ] No full resource lists fetched just for counting
