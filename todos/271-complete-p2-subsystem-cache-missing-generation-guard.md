---
status: pending
priority: p2
issue_id: 271
tags: [code-review, architecture, networking, pr-157]
dependencies: []
---

# Subsystem Cache Missing Generation Guard (TOCTOU Race)

## Problem Statement

The subsystem cache invalidation (`InvalidateSubsystemCaches`) sets cache to nil, but if `fetchSubsystems` is in-flight via singleflight, it will complete and re-write the cache with stale pre-invalidation data. The policy and gitops handlers solve this with a `cacheGen uint64` generation counter.

## Findings

**Location:** `backend/internal/networking/handler.go:51-57,499-502`

**Current pattern:**
```go
func (h *Handler) InvalidateSubsystemCaches() {
    h.subsystemMu.Lock()
    h.subsystemCache = nil  // just nils it
    h.subsystemMu.Unlock()
}
```

**Established pattern (policy/gitops):**
```go
// policy/handler.go:34
cacheGen uint64 // incremented on invalidation; prevents stale writes

// On invalidation: atomic.AddUint64(&h.cacheGen, 1)
// On fetch: capture gen before, check gen == captured before writing cache
```

**Race scenario:**
1. Config edit triggers `InvalidateSubsystemCaches()` → cache = nil
2. In-flight `fetchSubsystems()` completes with pre-edit data
3. `fetchSubsystems` writes stale data to cache at line 500-502
4. Cache now contains pre-edit data for up to 30s

## Proposed Solutions

### Option A: Add generation counter (Recommended)
**Pros:** Matches established policy/gitops pattern, prevents stale writes
**Cons:** Slightly more code
**Effort:** Small
**Risk:** None

## Acceptance Criteria

- [ ] Generation counter added to Handler struct
- [ ] Invalidation increments generation
- [ ] Cache write checks generation hasn't changed
- [ ] Pattern matches policy/handler.go implementation

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Policy and gitops handlers have this solved |
