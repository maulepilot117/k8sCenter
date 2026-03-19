---
status: pending
priority: p3
issue_id: "209"
tags: [code-review, simplicity, frontend]
dependencies: []
---

# ResourceTable.tsx is 662 lines — consider extracting dialogs

## Problem Statement

ResourceTable.tsx grew from 356 to 662 lines with the addition of inline Scale
dialog, Confirm dialog, and Toast notification. While functional, the component
handles too many concerns (data fetching, WS updates, search, sort, pagination,
kebab menus, three modal dialogs, toast notifications).

**Location:** `frontend/islands/ResourceTable.tsx`

## Findings

- Scale dialog: ~60 lines (lines 599-659)
- Confirm dialog: ~65 lines (lines 530-596)
- Toast: ~10 lines (lines 418-428)
- 11 Preact signals managing various state pieces
- The dialogs don't need to be islands themselves — they can be plain components
  imported by the island

## Proposed Solutions

### Option A: Extract ConfirmDialog and ScaleDialog components
- Move to `components/ui/ConfirmDialog.tsx` and `components/ui/ScaleDialog.tsx`
- Pass callbacks as props
- **Pros:** Cleaner separation, reusable for detail pages
- **Cons:** More files, prop threading
- **Effort:** Small
- **Risk:** Low

### Option B: Leave as-is
- 662 lines is manageable for a single island
- Extracting introduces prop-drilling complexity
- **Effort:** None
- **Risk:** None

## Recommended Action

Option A when these dialogs are needed elsewhere (e.g., detail page actions).

## Acceptance Criteria

- [ ] ResourceTable.tsx under 500 lines
- [ ] Dialogs are reusable components
- [ ] No behavior regression

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found during PR #46 simplicity review |
