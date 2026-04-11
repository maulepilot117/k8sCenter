---
status: pending
priority: p2
issue_id: "296"
tags: [code-review, scanning, performance, caching, pr-167]
dependencies: []
---

# Add singleflight coalescing + 30s TTL cache for detail endpoint

## Problem Statement

The new `HandleVulnerabilityDetail` endpoint has no caching or request coalescing. Each request does an RBAC SSAR (network round-trip) + a CRD list (network round-trip, potentially large response). Rapid-click scenarios (user bouncing between workloads in a namespace) will hammer the apiserver, and concurrent identical requests (e.g., double-click) each spawn separate fetches.

The project already standardizes on `singleflight + 30s cache` for read-heavy endpoints (`internal/policy`, `internal/scanning`'s existing `fetchVulns`). The new detail endpoint deviates from this pattern.

**Why it matters:** Consistency with project conventions; protects apiserver from rapid-click scenarios; matches existing scanning handler state.

## Findings

### Performance Oracle

**File:** `backend/internal/scanning/handler.go:263-321`

The handler calls `GetTrivyWorkloadVulnDetails` directly with no coalescing or cache. Compare with `fetchVulns` in the same file (lines 68-86) which uses `singleflight.Group` + 30s TTL cache.

Vulnerability reports update on the order of minutes to hours (tied to image pull events or Trivy rescan schedules), so 30s staleness is very safe.

## Proposed Solutions

### Option A: Add singleflight + detail cache (Recommended)

Extend `Handler` struct with a detail cache, add a wrapper similar to `fetchVulns`:

```go
type cachedDetail struct {
    detail    *WorkloadVulnDetail
    fetchedAt time.Time
}

// Handler struct additions
detailCache   map[string]*cachedDetail
detailCacheMu sync.RWMutex

func (h *Handler) fetchVulnDetail(ctx context.Context, namespace, kind, name string) (*WorkloadVulnDetail, error) {
    cacheKey := namespace + "/" + kind + "/" + name
    
    h.detailCacheMu.RLock()
    if entry := h.detailCache[cacheKey]; entry != nil && time.Since(entry.fetchedAt) < cacheTTL {
        detail := entry.detail
        h.detailCacheMu.RUnlock()
        return detail, nil
    }
    h.detailCacheMu.RUnlock()
    
    key := "vuln-detail:" + cacheKey
    result, err, _ := h.fetchGroup.Do(key, func() (any, error) {
        dynClient := h.K8sClient.BaseDynamicClient()
        return GetTrivyWorkloadVulnDetails(ctx, dynClient, namespace, kind, name)
    })
    if err != nil {
        return nil, err
    }
    
    detail := result.(*WorkloadVulnDetail)
    h.detailCacheMu.Lock()
    h.detailCache[cacheKey] = &cachedDetail{detail: detail, fetchedAt: time.Now()}
    h.detailCacheMu.Unlock()
    
    return detail, nil
}
```

Also extend `InvalidateCache` to clear `detailCache` so webhook-driven invalidation still works.

**Pros:** Consistent with existing pattern; protects apiserver; coalesces concurrent requests
**Cons:** ~30 lines of boilerplate; cache eviction logic needed (same pattern as existing `evictOldestLocked`)
**Effort:** Small
**Risk:** Low

### Option B: Singleflight only (no TTL cache)

Just wrap in `h.fetchGroup.Do` without TTL caching. Coalesces concurrent identical requests but doesn't reduce repeat-fetch load over time.

**Pros:** 5 lines of code; zero staleness
**Cons:** Partial fix
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/handler.go` (Handler struct + new `fetchVulnDetail` + update `HandleVulnerabilityDetail` to use it + extend `InvalidateCache`)

## Acceptance Criteria

- [ ] Concurrent requests for same workload coalesce via singleflight
- [ ] Repeat requests within 30s serve from cache
- [ ] `InvalidateCache` clears both namespace and detail caches
- [ ] Unit test for coalescing behavior

## Work Log

<!-- Dated record -->

## Resources

- PR #167
- Existing pattern: `backend/internal/scanning/handler.go:68-86` (`fetchVulns`)
