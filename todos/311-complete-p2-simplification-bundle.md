---
status: pending
priority: p2
issue_id: "311"
tags: [code-review, simplicity, yagni, pr-167]
dependencies: []
---

# Bundle: simplification cleanups in VulnerabilityDetail + scanning-types

## Problem Statement

Several small YAGNI/simplicity issues in the Phase 2A frontend that are cheap to fix together:

1. **`unfixable` counter is computed but never displayed.** The summary panel only renders `fixable`. Remove the field.
2. **`flattenCVEs` helper is a one-caller wrapper.** Inline it with `flatMap`.
3. **`useComputed` for trivial one-liner derivations.** `summary`, `allCVEs`, and `filtered` use `useComputed` where plain `const` would work — signals' `.value` reads already re-run the render.
4. **Redundant `IS_BROWSER` guards.** Both the effect (line 85) and the main return (line 109) guard the same condition. One is enough.
5. **WorkloadRow JSX duplicated across the two branches** — see todo 308 for the row-click pattern fix; the JSX dedup can be addressed there.

**Why it matters:** ~35 LOC of ceremony in a 360-LOC island. Makes future reviewers second-guess the intent.

## Findings

### Code Simplicity Reviewer

**File:** `frontend/islands/VulnerabilityDetail.tsx`
- Lines 30-47: FlatCVE interface + flattenCVEs helper (one caller)
- Lines 85, 109: duplicate IS_BROWSER guards
- Lines 89-104: useComputed for trivial derivations

**File:** `frontend/lib/scanning-types.ts`
- Line 91: `unfixable` field
- Lines 130-132: unfixable increment (else branch)

## Proposed Solutions

### Option A: Apply all cleanups (Recommended)

1. Drop `unfixable` from `VulnDetailSummary` type and the else branch in `computeVulnSummary`
2. Inline `flattenCVEs`:
   ```ts
   const allCVEs = detail.value
     ? detail.value.images.flatMap((img) =>
         img.vulnerabilities.map((v) => ({ ...v, image: img.name, container: img.container })))
     : [];
   ```
3. Replace `useComputed` with plain consts where derivations are trivial
4. Remove one of the two `IS_BROWSER` guards

**Pros:** ~35 LOC removed; clearer intent; no behavior change
**Cons:** None
**Effort:** Small
**Risk:** None

### Option B: Cherry-pick only the YAGNI items

Fix only `unfixable` removal and `flattenCVEs` inlining; leave `useComputed` as-is (harmless).

**Pros:** Minimal change
**Cons:** Partial fix
**Effort:** Trivial
**Risk:** None

## Recommended Action

Option A.

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx`
- `frontend/lib/scanning-types.ts`

## Acceptance Criteria

- [ ] `unfixable` removed from types and compute function
- [ ] `flattenCVEs` inlined (or kept only if it earns its keep)
- [ ] At most one `IS_BROWSER` guard
- [ ] Tests/lint/fmt/build clean

## Work Log

## Resources

- PR #167
