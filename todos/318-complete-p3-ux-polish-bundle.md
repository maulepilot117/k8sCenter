---
status: pending
priority: p3
issue_id: "318"
tags: [code-review, ux, polish, pr-167]
dependencies: []
---

# Bundle: UX polish items for vulnerability detail view

## Problem Statement

Cheap UX wins grouped together for batch fix:

1. **Pagination has only Prev/Next** — no page-number input or jump-to-last. With 500+ CVEs this is annoying.
2. **CVE title is only in tooltip** — users scanning for "openssl buffer overflow" have no visible hook. Add truncated title as a second line under the CVE ID.
3. **Summary severity cards look clickable but aren't filters.** Users will click them expecting to filter.
4. **Back link on detail page** loses the dashboard's search/scanner filter state. Consider query-string round-tripping.

**Why it matters:** Each item is small friction; together they degrade the feel.

## Findings

### UX Reviewer — friction/polish

**File:** `frontend/islands/VulnerabilityDetail.tsx`
- Pagination at lines 302-323 (Prev/Next only)
- CVE column at line 351-361 (title in tooltip only)
- Summary cards at lines 170-202 (not clickable)
- Back link at lines 125-131 (hardcoded)

## Proposed Solutions

### Option A: Fix all four items

1. Add a page input or "go to last page" button
2. Render truncated `cve.title` below the CVE ID (keep tooltip too)
3. Make severity cards clickable toggles that drive the severity filter
4. Preserve dashboard filter state via query-string

**Pros:** Fast small wins
**Cons:** Touches 4 code regions
**Effort:** Small total
**Risk:** None

### Option B: Cherry-pick #2 and #3 only

The most user-visible.

**Pros:** Minimal; biggest wins
**Cons:** Leaves pagination and back-link annoyances
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx`

## Acceptance Criteria

- [ ] CVE titles visible in rows
- [ ] Severity cards drive filter OR are visually clearly non-interactive
- [ ] Pagination improvements shipped
- [ ] Optional: filter state preserved on back-nav

## Work Log

## Resources

- PR #167
