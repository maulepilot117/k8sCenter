---
status: pending
priority: p2
issue_id: "089"
tags: [code-review, frontend, simplicity]
dependencies: []
---

# Un-export Individual Column Arrays in resource-columns.ts

## Problem Statement
All 19 individual column arrays (`podColumns`, `deploymentColumns`, etc.) are exported from `resource-columns.ts`, but only `RESOURCE_COLUMNS` (the lookup map) is used by consumers. The individual exports add unnecessary public API surface.

## Findings
- `resource-columns.ts` exports 19 named column arrays + 1 RESOURCE_COLUMNS map
- Only `RESOURCE_COLUMNS` is imported in `ResourceTable.tsx`
- Unused exports increase API surface and could lead to import confusion

## Proposed Solutions

### Option A: Remove `export` from individual column arrays, keep only RESOURCE_COLUMNS export
- **Pros:** Cleaner public API, signals that RESOURCE_COLUMNS is the intended interface
- **Cons:** None — if tests need individual arrays, they can import RESOURCE_COLUMNS["pods"]
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/lib/resource-columns.ts`

## Acceptance Criteria
- [ ] Only `RESOURCE_COLUMNS` is exported from resource-columns.ts
- [ ] All consumers use RESOURCE_COLUMNS lookup, not individual arrays
