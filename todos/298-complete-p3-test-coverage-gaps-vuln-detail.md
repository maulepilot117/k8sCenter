---
status: pending
priority: p3
issue_id: "298"
tags: [code-review, scanning, testing, pr-167]
dependencies: []
---

# Improve test coverage for vulnerability detail

## Problem Statement

Several test gaps in the new `trivy_test.go` tests:

1. `TestGetTrivyWorkloadVulnDetails_MultipleImages` only asserts `len(detail.Images) == 2`. A regression in grouping would still pass this test.
2. `selectCVSSScore` int64/int branches (trivy.go:181-186) are not exercised — only float64 values are tested.
3. `extractCVEDetail` top-level `"score"` fallback (trivy.go:312-316) is not exercised.
4. No negative-path test for `GetTrivyWorkloadVulnDetails` — malformed `vulnerabilities` entry (non-map) or missing `vulnerabilityID` (both should be skipped).
5. `selectCVSSScore` "falls back to any vendor" test has non-deterministic fallback (Go map iteration order). With two unknown vendors the test would flake.

**Why it matters:** Weak tests allow regressions through.

## Findings

### Pattern Recognition + Simplicity Reviewers

**File:** `backend/internal/scanning/trivy_test.go`

- Lines 466-498: `TestGetTrivyWorkloadVulnDetails_MultipleImages` — only counts images
- Lines 293-299: `TestSelectCVSSScore` "any vendor" case — relies on single-entry map

## Proposed Solutions

### Option A: Add missing assertions and edge cases (Recommended)

1. Strengthen `_MultipleImages` to assert per-image `Container`, image name, and CVE IDs
2. Add test cases for `extractCVEDetail` with top-level `"score"` (no nested CVSS)
3. Add test case for malformed vulnerabilities array entries
4. Either make `selectCVSSScore` fallback deterministic (sort vendor keys) or add a multi-vendor test that documents the guarantee

**Pros:** Catches real regressions; pins behavior
**Cons:** ~50 LOC of additional tests
**Effort:** Small
**Risk:** None

### Option B: Remove weak test, keep existing coverage

Drop `_MultipleImages` since it's redundant with `_SortsAndGroups` per simplicity reviewer.

**Pros:** Simpler; less code to maintain
**Cons:** Slightly reduced explicit coverage
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/trivy_test.go`
- Possibly `backend/internal/scanning/trivy.go` (if making fallback deterministic)

## Acceptance Criteria

- [ ] Tests catch grouping regressions (not just count)
- [ ] `"score"` fallback path covered
- [ ] Malformed input skip path covered
- [ ] No flaky tests due to map iteration order

## Work Log

<!-- Dated record -->

## Resources

- PR #167
