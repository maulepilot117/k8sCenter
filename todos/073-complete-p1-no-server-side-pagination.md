---
status: pending
priority: p1
issue_id: "073"
tags: [code-review, frontend, performance, scalability]
dependencies: []
---

# No Server-Side Pagination in ResourceTable

## Problem Statement
`ResourceTable.tsx` fetches resources via REST with no pagination parameters (the backend supports `?limit=` and `?continue=`). The frontend receives up to the backend's default limit (100 items) and shows `"X items"` in the header, which is misleading when there are more resources than the limit. There is no "load more" or pagination UI.

## Findings
- `ResourceTable.tsx:50-53` — API call has no `?limit=` or `?continue=` parameters
- Backend `paginate[T]` defaults to 100 items with cursor-based continue tokens
- Frontend shows `displayed.value.length` as "X items" — user thinks they see all resources
- In production clusters with hundreds of pods/events, most resources are invisible
- No UI affordance to load additional pages

## Proposed Solutions

### Option A: Add client-side pagination with "load more" button
- **Pros:** Simple UX, progressive loading, uses existing backend pagination
- **Cons:** Still loads data incrementally, total count may not be known upfront
- **Effort:** Medium
- **Risk:** Low

### Option B: Add server-side page navigation (page 1, 2, 3...)
- **Pros:** Clear navigation, predictable data loading
- **Cons:** k8s continue tokens don't support random page access natively
- **Effort:** Medium
- **Risk:** Medium — requires backend changes for offset-based pagination

## Technical Details
- **Affected files:** `frontend/islands/ResourceTable.tsx`, `frontend/components/ui/DataTable.tsx`
- **Components:** ResourceTable REST fetch, DataTable UI

## Acceptance Criteria
- [ ] ResourceTable passes pagination parameters to the API
- [ ] UI clearly indicates when more resources exist beyond the current page
- [ ] User can load additional resources (load more button or pagination controls)
- [ ] Item count accurately reflects total vs. displayed
