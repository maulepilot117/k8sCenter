---
status: pending
priority: p1
issue_id: 258
tags: [code-review, data-integrity, networking, pr-157]
dependencies: []
---

# IPAM Computation Double-Counts Allocated IPs

## Problem Statement

The IPAM response computation in `HandleCiliumIPAM` incorrectly calculates `totalAvailable` and `total`, causing the progress bar, utilization percentage, and exhaustion risk to display wrong values. This undermines the exhaustion alerting feature — users could miss IP exhaustion until it's too late.

## Findings

**Location:** `backend/internal/networking/handler.go:364-390`

The bug is in the aggregate computation loop:

```go
for _, n := range nodes {
    totalAllocated += n.UsedCount
    totalAvailable += n.PoolCount  // BUG: should be n.PoolCount - n.UsedCount
    // ...
}
total := totalAllocated + totalAvailable  // double-counts allocated IPs
```

- `n.PoolCount` = `len(spec.ipam.pool)` — total pool size (includes both used AND free IPs)
- `n.UsedCount` = `len(status.ipam.used)` — currently allocated IPs
- Used IPs are a subset of pool IPs

**Example:** Node with 100 pool IPs, 80 used:
- Current: `totalAvailable=100`, `total=180`, risk = 80/180 = 44% → "none"
- Correct: `totalAvailable=20`, `total=100`, risk = 80/100 = 80% → "medium"

The per-node computation at line 374 is correct (`nodeAvail = n.PoolCount - n.UsedCount`), but the aggregate uses the wrong formula.

**Impact:** Exhaustion risk thresholds (>75% medium, >90% high) are undermined. A cluster at 80% utilization would show "none" instead of "medium".

## Proposed Solutions

### Option A: Fix aggregate to match per-node logic (Recommended)
**Pros:** Minimal change, matches the correct per-node logic already in the code
**Cons:** None
**Effort:** Small
**Risk:** Low

```go
totalAvailable += (n.PoolCount - n.UsedCount)
```

### Option B: Compute total from PoolCount directly
**Pros:** Cleaner semantic — total IS the pool size
**Cons:** Slightly different variable naming
**Effort:** Small
**Risk:** Low

```go
var totalPool int
for _, n := range nodes {
    totalPool += n.PoolCount
    totalAllocated += n.UsedCount
}
totalAvailable := totalPool - totalAllocated
total := totalPool
```

## Recommended Action

Option B — cleaner semantics.

## Technical Details

- **Affected files:** `backend/internal/networking/handler.go`
- **Affected lines:** 364-390
- **Components:** HandleCiliumIPAM handler
- **Database changes:** None

## Acceptance Criteria

- [ ] `totalAvailable` equals `sum(PoolCount) - sum(UsedCount)` across all nodes
- [ ] `total` equals `sum(PoolCount)` across all nodes
- [ ] `computeExhaustionRisk` receives correct total
- [ ] Unit test covering IPAM computation with known pool/used values
- [ ] Existing tests still pass

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Per-node logic correct but aggregate wrong |

## Resources

- PR #157: feat: networking overview islands
- `computeExhaustionRisk()` at `types.go:98-111`
- Cilium IPAM docs: CiliumNode CRD `spec.ipam.pool` vs `status.ipam.used`
