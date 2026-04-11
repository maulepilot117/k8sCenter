---
status: pending
priority: p3
issue_id: "303"
tags: [code-review, scanning, refactor, pr-167]
dependencies: []
---

# Split scanning handler.go (now > 300 LOC)

## Problem Statement

`backend/internal/scanning/handler.go` is now 364 LOC after this PR. CLAUDE.md's Step 0 rule flags files > 300 LOC for structural review. The file mixes shared state/caching/RBAC/metadata helpers with three distinct endpoint handlers.

**Why it matters:** Cohesion; easier navigation; limits blast radius of future edits.

## Findings

### Architecture Strategist

**File:** `backend/internal/scanning/handler.go` (364 LOC)

Contains:
- Shared state (`Handler` struct, `cachedNSData`, constants)
- Cache plumbing (`InitCache`, `InvalidateCache`, `fetchVulns`, `doFetchNS`, `evictOldestLocked`)
- `HandleStatus`
- `HandleVulnerabilities`
- `HandleVulnerabilityDetail` (new) + regex validators
- RBAC helpers (`canAccessTrivy`, `canAccessKubescape`)
- Metadata helpers (`filterByScannerAccess`, `computeMetadata`)

## Proposed Solutions

### Option A: Split into 3 files (Recommended)

```
handler.go                         (state, cache, HandleStatus, RBAC, metadata — ~200 LOC)
handler_vulnerabilities.go         (HandleVulnerabilities — ~60 LOC)
handler_vulnerability_detail.go    (HandleVulnerabilityDetail + validators — ~90 LOC)
```

Matches how `gitops/handler.go` groups related actions.

**Pros:** Each file < 300 LOC; clear cohesion
**Cons:** 2 new files
**Effort:** Small
**Risk:** Low

### Option B: Accept the LOC growth

**Pros:** No refactor
**Cons:** Violates project convention
**Effort:** None
**Risk:** Low

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/handler.go` (split)
- Possibly new: `backend/internal/scanning/handler_vulnerabilities.go`
- Possibly new: `backend/internal/scanning/handler_vulnerability_detail.go`

## Acceptance Criteria

- [ ] handler.go < 300 LOC
- [ ] All tests still pass
- [ ] Package API unchanged

## Work Log

## Resources

- PR #167
- CLAUDE.md "Step 0 rule"
