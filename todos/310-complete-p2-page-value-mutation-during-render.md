---
status: pending
priority: p2
issue_id: "310"
tags: [code-review, bug, signals, pr-167]
dependencies: []
---

# Mutating page.value during render is a latent bug

## Problem Statement

`VulnerabilityDetail.tsx:115` contains `if (page.value > totalPages) page.value = totalPages;` inside the component's render body. Preact Signals tolerates this (no React-style render loop) but it's an unusual pattern — mutating a signal inside render can trigger a re-render in the same tick and will confuse any future reader.

**Why it matters:** Latent bug. Works today because of Signals' specific semantics; fragile under future refactors.

## Findings

### Code Simplicity + Pattern Recognition Reviewers

**File:** `frontend/islands/VulnerabilityDetail.tsx:115`

```tsx
const totalPages = Math.max(
  1,
  Math.ceil(filtered.value.length / PAGE_SIZE),
);
if (page.value > totalPages) page.value = totalPages;
const displayed = filtered.value.slice(
  (page.value - 1) * PAGE_SIZE,
  page.value * PAGE_SIZE,
);
```

## Proposed Solutions

### Option A: Compute clamped page locally (Recommended)

```tsx
const totalPages = Math.max(1, Math.ceil(filtered.value.length / PAGE_SIZE));
const currentPage = Math.min(page.value, totalPages);
const displayed = filtered.value.slice(
  (currentPage - 1) * PAGE_SIZE,
  currentPage * PAGE_SIZE,
);
```

Then reset `page.value = 1` in the filter-change handlers (already done for severity + fixable).

**Pros:** No signal mutation during render; obvious behavior
**Cons:** None
**Effort:** Trivial
**Risk:** None

### Option B: useEffect to clamp

```tsx
useEffect(() => {
  if (page.value > totalPages) page.value = totalPages;
}, [totalPages]);
```

**Pros:** Keeps page.value truthy
**Cons:** Extra render cycle; still has coupling
**Effort:** Trivial
**Risk:** None

## Recommended Action

Option A — use a local clamped variable.

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx:111-121`

## Acceptance Criteria

- [ ] No signal mutation inside render body
- [ ] Pagination still clamps correctly when filters reduce result count
- [ ] Existing tests still pass

## Work Log

## Resources

- PR #167
