---
name: Clear issuer-type subform values when switching type
status: complete
priority: p2
issue_id: 331
tags: [code-review, frontend, ux, pr-180]
dependencies: []
---

## Problem Statement

`frontend/islands/IssuerWizard.tsx:129-132` (`selectType`) sets `form.type = t` but does not null out the sibling subform fields. Switching type from `acme` → `ca` → `acme` re-shows the old ACME field values (stale after intermediate changes). `fetchPreview` correctly ignores the inactive branches, so the wire is fine — this is a UX-only issue.

## Findings

- `IssuerWizard.tsx:129-132`
- Reviewer: kieran-typescript-reviewer (P1 UX, borderline)

## Proposed Solutions

### Option A — reset sibling subforms on type change (recommended)
Preserve the selected type's values; clear the others. Matches user expectation when toggling.

### Option B — accept stale state
Leave as-is. Users rarely switch types mid-form, and the wire is already clean.

## Acceptance Criteria
- [ ] Changing `type` does not leak field values from the previous branch into the rendered form.

## Work Log
- 2026-04-14: Filed from PR #180 review.
