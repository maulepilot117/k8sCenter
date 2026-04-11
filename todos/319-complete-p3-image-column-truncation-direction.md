---
status: pending
priority: p3
issue_id: "319"
tags: [code-review, ux, pr-167]
dependencies: []
---

# Image column truncates from right, losing the tag

## Problem Statement

`VulnerabilityDetail.tsx:368` truncates image references to 180px from the right, which loses the tag. A long reference like `registry.example.com/very/long/path:v1.2.3` becomes `registry.examp...` — cutting off the tag, which is the most important part for remediation.

**Why it matters:** The tag is the actionable signal (which version to upgrade to). Users need to hover to see it.

## Findings

### UX Reviewer — polish

**File:** `frontend/islands/VulnerabilityDetail.tsx:366-373`

## Proposed Solutions

### Option A: Truncate from the left (RTL trick)

Use `direction: rtl; text-align: left;` or the `truncate` CSS with a leading ellipsis pattern so the tag stays visible.

**Pros:** Tag always visible
**Cons:** RTL trick is a bit obscure
**Effort:** Trivial
**Risk:** None

### Option B: Strip the registry prefix

If image contains `/`, show everything after the first `/`. Registry prefix (e.g., `registry.example.com/`) is usually repeated noise.

**Pros:** Cleaner
**Cons:** Ambiguous for multi-registry setups
**Effort:** Trivial
**Risk:** None

### Option C: Two-line cell

Row 1: image path (truncated), Row 2: tag.

**Pros:** Tag always visible, no clever CSS
**Cons:** Doubles row height
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx:366-373`

## Acceptance Criteria

- [ ] Image tag visible without hover

## Work Log

## Resources

- PR #167
