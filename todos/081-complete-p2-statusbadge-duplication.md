---
status: pending
priority: p2
issue_id: "081"
tags: [code-review, frontend, duplication, simplicity]
dependencies: []
---

# StatusBadge Duplication — Two Diverging Implementations

## Problem Statement
`components/ui/StatusBadge.tsx` exists as a reusable component, but `lib/resource-columns.ts` has its own inline `badge()` and `statusColor()` functions that duplicate the same logic with slightly different styling. These will diverge over time.

## Findings
- `StatusBadge.tsx` — proper Preact component with variant prop and auto-detection
- `resource-columns.ts:32-75` — inline `badge()` function using `h()` directly
- The inline version exists because importing an island component in server context was avoided
- Both map status strings to green/amber/red colors but with different class lists

## Proposed Solutions

### Option A: Extract shared status-color logic into a pure utility, use in both places
- **Pros:** Single source of truth for status→color mapping
- **Cons:** StatusBadge component and resource-columns still render differently
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/components/ui/StatusBadge.tsx`, `frontend/lib/resource-columns.ts`

## Acceptance Criteria
- [ ] Status-to-color mapping is defined in one place
- [ ] Both StatusBadge and resource-columns use the same mapping
