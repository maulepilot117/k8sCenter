---
status: pending
priority: p3
issue_id: "214"
tags: [code-review, accessibility, frontend]
dependencies: []
---

# Add ARIA attributes to ConfirmDialog and PasswordDialog

## Problem Statement

Neither dialog uses semantic dialog markup (`role="dialog"`, `aria-modal`,
`aria-labelledby`, `aria-describedby`). Screen readers won't announce these
as modal dialogs.

## Proposed Solutions

### Option A: Add ARIA attributes to existing divs
- Add `role="dialog"` and `aria-modal="true"` to the dialog container
- Add `id` to the title `<h3>` and reference via `aria-labelledby`

### Option B: Use native `<dialog>` element
- Get Escape key and focus trapping for free
- Removes manual useEffect handlers

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found during PR #48 review |
