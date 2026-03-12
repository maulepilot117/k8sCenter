---
status: pending
priority: p3
issue_id: "059"
tags: [code-review, frontend, ux]
dependencies: []
---

# TopBar User Menu Doesn't Close on Outside Click

## Problem Statement
The user dropdown menu in TopBar stays open when clicking outside of it. Standard dropdown behavior should close the menu when clicking anywhere else on the page.

## Findings
- `frontend/islands/TopBar.tsx` — Menu opens on click but no event listener for closing on outside click
- Standard pattern: add a `mousedown` event listener on `document` that closes the menu if the click target is outside the menu ref

Flagged by: TypeScript Reviewer (P2)

## Proposed Solutions

### Option A: Add useEffect with document click listener
- **Pros**: Standard pattern, good UX
- **Cons**: Slightly more code
- **Effort**: Small
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/islands/TopBar.tsx`

## Acceptance Criteria
- [ ] Clicking outside the user menu closes it
- [ ] Clicking inside the menu (on menu items) works normally
- [ ] Event listener is cleaned up on unmount
