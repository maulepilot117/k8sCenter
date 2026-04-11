---
status: pending
priority: p2
issue_id: "308"
tags: [code-review, ux, a11y, consistency, pr-167]
dependencies: []
---

# Row-click pattern in VulnerabilityDashboard is inconsistent with project convention

## Problem Statement

`VulnerabilityDashboard` introduces a new row-click pattern (absolute-positioned invisible `<a>` anchor inside the first `<td>`) that diverges from every other clickable-row table in the project. The stated rationale was improved accessibility (keyboard, middle-click, right-click), but:

1. Only the Name cell hosts the anchor — Tab focus lands only on Name, but clicking severity/scanner/timestamp cells still navigates (via `onClick` on the `<tr>`), creating a keyboard vs. mouse divergence.
2. No visible focus ring on the anchor (`absolute inset-0` with no `focus:` style).
3. The JSX for the `href` and non-href branches is duplicated verbatim (~25 LOC).
4. `e.target.closest("a")` guard exists for inner links that don't exist ("future-proof").
5. Clicking the name/kind text may not trigger middle-click-to-open because those divs have `relative` positioning and stack above the absolute anchor.

**Why it matters:** Two conventions for row-click navigation in the same codebase creates maintenance cost and inconsistent UX.

## Findings

### Pattern Recognition + UX Reviewers

**Existing pattern** — `frontend/islands/GitOpsApplications.tsx:314-321`:
```tsx
<tr
  class="hover:bg-hover/30 cursor-pointer"
  onClick={() => {
    globalThis.location.href = "/gitops/applications/" + encodeURIComponent(app.id);
  }}
>
```
Plain `tr` + `onClick`. Used across all detail-listing tables.

**New pattern** — `frontend/islands/VulnerabilityDashboard.tsx:427-458`: absolute-positioned invisible anchor + duplicated row JSX.

## Proposed Solutions

### Option A: Match GitOpsApplications convention (Recommended)

Revert to plain `tr` + `onClick`. Accept the a11y limitation (no middle-click, no right-click) for consistency with the rest of the project, matching what users already expect.

**Pros:** Consistent with GitOps/policy/etc; simpler code; ~40 LOC reduction
**Cons:** Loses keyboard/middle-click/right-click (same as existing tables — no regression)
**Effort:** Small
**Risk:** None

### Option B: Extract a shared `<ClickableRow>` component

Build a proper wrapper that handles all the a11y concerns once and migrate GitOps, policy, violations, and this table to it.

**Pros:** Best UX; single source of truth for a11y
**Cons:** Cross-cutting refactor (7+ tables); larger scope than this PR
**Effort:** Medium
**Risk:** Low

### Option C: Keep current pattern but fix the duplication

Collapse the duplicated JSX branches into a single render path with conditional anchor.

**Pros:** Keeps the a11y intent; removes the ~25 LOC duplication
**Cons:** Still two conventions in the codebase
**Effort:** Small
**Risk:** None

## Recommended Action

<!-- Filled during triage — probably Option A for consistency, Option B as follow-up -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDashboard.tsx:387-462` (WorkloadRow)
- Pattern reference: `frontend/islands/GitOpsApplications.tsx:314-321`

## Acceptance Criteria

- [ ] Row-click pattern matches project convention OR a shared wrapper exists
- [ ] JSX duplication removed
- [ ] Decision documented for future reviewers

## Work Log

## Resources

- PR #167
