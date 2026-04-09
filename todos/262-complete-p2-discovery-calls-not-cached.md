---
status: pending
priority: p2
issue_id: 262
tags: [code-review, performance, networking, pr-157]
dependencies: []
---

# Discovery Calls Not Cached for BGP/IPAM Handlers

## Problem Statement

`HandleCiliumBGP` and `HandleCiliumIPAM` call `hasCRD()` on every request, which calls `disc.ServerResourcesForGroupVersion()` — an API server round-trip. With 3 islands polling every 60s, that's 2 discovery calls per minute per browser tab, on top of the actual CRD reads.

## Findings

**Location:**
- `backend/internal/networking/handler.go:303-304` (BGP)
- `backend/internal/networking/handler.go:350-351` (IPAM)

Unlike the subsystems handler which has singleflight+30s cache, BGP and IPAM handlers hit discovery on every request. The Detector already caches CNI info including `HasCRDs`, but doesn't cache per-CRD availability.

Other packages (policy, gitops, scanning) do CRD discovery in background goroutines, not per-request.

## Proposed Solutions

### Option A: Cache hasCRD results in the Handler with TTL (Recommended)
**Pros:** Consistent with subsystems caching pattern
**Cons:** Slightly more state in Handler
**Effort:** Small
**Risk:** Low

### Option B: Use Detector's cached CRD info
**Pros:** Reuses existing cache
**Cons:** Detector may not track per-CRD granularity
**Effort:** Medium
**Risk:** Low

## Acceptance Criteria

- [ ] Discovery calls don't hit API server on every poll
- [ ] CRD availability cached with reasonable TTL (30-60s)

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Other packages use background discovery |
