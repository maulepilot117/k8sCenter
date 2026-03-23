---
status: complete
priority: p2
issue_id: 245
tags: [simplicity, frontend, code-review, phase4b]
---

# RoleBindingWizard code cleanup (3 issues)

## Problem Statement

The RoleBindingWizard contains three areas of unnecessary duplication and complexity that should be simplified for maintainability.

## Findings

### (a) Duplicate path variables
`cancelHref` and `detailBasePath` are identical variables (lines 235-240). One should be removed and the other reused.

### (b) Duplicated subject update logic
Subject row update logic is duplicated 4 times across different subject field handlers. This should be extracted into a shared `updateSubject(idx, patch)` helper function.

### (c) Split navigation button blocks
Navigation buttons (Back/Next/Submit) are split into two separate conditional blocks unnecessarily. These should be merged into a single block with conditional rendering.

## Technical Details

- **File:** `frontend/islands/RoleBindingWizard.tsx`
- (a) Lines 235-240: `cancelHref` and `detailBasePath` compute the same value
- (b) Subject field change handlers repeat the same array-copy-and-splice pattern 4 times
- (c) Navigation button rendering uses two conditional blocks that could be one

## Acceptance Criteria

- [ ] `cancelHref` and `detailBasePath` deduplicated into a single variable
- [ ] Subject update logic extracted into an `updateSubject(idx, patch)` helper
- [ ] Navigation button blocks merged into a single conditional rendering block
- [ ] No behavioral changes — all wizard functionality preserved
