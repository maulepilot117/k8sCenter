---
status: pending
priority: p3
issue_id: "301"
tags: [code-review, scanning, performance, pr-167]
dependencies: []
---

# Drop redundant strings.ToUpper on severity

## Problem Statement

`extractCVEDetail` calls `strings.ToUpper` on every severity value. Trivy already emits severities in uppercase (`CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, `UNKNOWN`), so this is a 3000-allocation-per-request no-op for typical workloads.

**Why it matters:** Tiny perf win, removes redundant code.

## Findings

### Performance Oracle

**File:** `backend/internal/scanning/trivy.go:297`

```go
Severity: strings.ToUpper(getStr("severity")),
```

Trivy Operator emits uppercase. Verified in test fixtures and in Trivy source. The `severityRank` function already handles mixed case via its own `strings.ToUpper`, so the handler path is double-covered.

## Proposed Solutions

### Option A: Drop ToUpper, trust Trivy (Recommended)

```go
Severity: getStr("severity"),
```

Keep `strings.ToUpper` in `severityRank` as defense.

**Pros:** ~3000 fewer allocations per request for 600-CVE workloads
**Cons:** If Trivy ever emits mixed case, we rely on severityRank to normalize at sort time (already does)
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/trivy.go:297`

## Acceptance Criteria

- [ ] ToUpper removed from extractCVEDetail
- [ ] Existing tests still pass

## Work Log

## Resources

- PR #167
