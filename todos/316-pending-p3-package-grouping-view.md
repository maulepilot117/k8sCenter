---
status: pending
priority: p3
issue_id: "316"
tags: [code-review, ux, feature, pr-167]
dependencies: []
---

# Add package-grouped view for remediation prioritization

## Problem Statement

A single vulnerable package (e.g., `openssl`) often produces dozens of CVEs per image. The current flat CVE table renders each as a separate row. An SRE's first question — "what packages should I upgrade to close the most CVEs?" — requires manual counting.

**Why it matters:** Primary remediation workflow isn't supported.

## Findings

### UX Reviewer — blocker (prioritization)

**File:** `frontend/islands/VulnerabilityDetail.tsx:252-291` — flat CVE table

## Proposed Solutions

### Option A: Toggle between CVE view and Package view

Add a view toggle; package view groups by `package` field with counts per severity + a unified "Fix Available" column showing the highest-version fix.

**Pros:** Best remediation UX
**Cons:** Extra UI; more state
**Effort:** Medium
**Risk:** Low

### Option B: Add a secondary sort option

Let users sort by package name as secondary, which puts same-package CVEs adjacent.

**Pros:** Minimal change
**Cons:** Still 40 rows for one package
**Effort:** Small
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx`
- Possibly `frontend/lib/scanning-types.ts` (helper for package grouping)

## Acceptance Criteria

- [ ] Users can answer "what packages should I upgrade?" without manual counting

## Work Log

### 2026-04-11 — Deferred

Not addressed in the review-fix batch because this is a feature addition (new view toggle + grouping logic) rather than a bug fix. The surrounding P1/P2/P3 fixes in review batch #2 landed in commit 1d9fccf; this remains open for a dedicated follow-up.

Mitigation in the interim: users can now filter by severity and "fixable only" and sort by CVE ID, which partially addresses prioritization. Package grouping would go further but needs its own design pass.

## Resources

- PR #167
