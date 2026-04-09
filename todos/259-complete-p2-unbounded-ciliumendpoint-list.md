---
status: pending
priority: p2
issue_id: 259
tags: [code-review, performance, networking, pr-157]
dependencies: []
---

# Unbounded CiliumEndpoint List Call

## Problem Statement

`aggregateEndpoints()` lists ALL CiliumEndpoints across ALL namespaces with no pagination (`metav1.ListOptions{}`). Production clusters can have 10,000+ endpoints. This creates an expensive, unbounded API server call that could cause timeouts and memory pressure.

## Findings

**Location:** `backend/internal/networking/cilium_crds.go:200`

```go
list, err := client.Resource(ciliumEndpointGVR).Namespace("").List(ctx, metav1.ListOptions{})
```

Since we only need aggregate counts (ready/not-ready/etc.), we don't need all endpoint data. However, the Kubernetes API doesn't support server-side aggregation for CRDs.

## Proposed Solutions

### Option A: Add pagination with continue token
**Pros:** Bounds memory per page, standard k8s pattern
**Cons:** Multiple API calls, still transfers all data eventually
**Effort:** Small
**Risk:** Low

### Option B: Use ResourceVersion 0 + chunked listing
**Pros:** Can serve from API server cache, reduces etcd load
**Cons:** Slightly stale data (acceptable for status display)
**Effort:** Small
**Risk:** Low

### Option C: Cap at 5000 endpoints with Limit
**Pros:** Simple, bounded resource usage
**Cons:** Undercounts if cluster has >5000 endpoints
**Effort:** Small
**Risk:** Medium — inaccurate counts

## Recommended Action

Option B with `Limit: 500` and continue token pagination. Use `ResourceVersion: "0"` to serve from cache.

## Technical Details

- **Affected files:** `backend/internal/networking/cilium_crds.go`
- **Affected lines:** 199-226

## Acceptance Criteria

- [ ] Endpoint listing uses pagination (Limit + Continue)
- [ ] No single API call returns unbounded results
- [ ] Aggregate counts remain accurate

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Same pattern exists for CiliumNode but less risky (typically <100 nodes) |

## Resources

- PR #157
- k8s API chunked listing: https://kubernetes.io/docs/reference/using-api/api-concepts/#retrieving-large-results-sets-in-chunks
