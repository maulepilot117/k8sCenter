---
status: pending
priority: p1
issue_id: "283"
tags: [code-review, limits, rbac, performance, scalability]
dependencies: []
---

# Sequential RBAC Checks Without Caching (O(n) API calls)

## Problem Statement

The `filterByRBAC` function performs one RBAC check per namespace, sequentially and without caching the results. In a cluster with 500 namespaces, this generates 500 synchronous SubjectAccessReview calls to the Kubernetes API server on every dashboard load.

## Findings

**Source:** Performance Oracle agent

**Location:** `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/internal/limits/handler.go` (lines 239-252)

**Evidence:**
```go
func (h *Handler) filterByRBAC(ctx context.Context, user *auth.User, summaries []NamespaceSummary) []NamespaceSummary {
    filtered := make([]NamespaceSummary, 0, len(summaries))
    for _, s := range summaries {
        allowed, err := h.AccessChecker.CanAccess(ctx, user.Username, user.KubernetesGroups, "get", "resourcequotas", s.Namespace)
        // ... sequential, no caching
    }
    return filtered
}
```

**Impact:**
- At 500 namespaces: approximately 500 API calls, likely 2-5 seconds latency
- At 1000 namespaces: approximately 1000 API calls, 5-10 seconds latency
- Creates load on the Kubernetes API server proportional to namespace count
- First request after cache expiry is extremely slow

## Proposed Solutions

### Solution A: Cache RBAC Results by Namespace (Quick Fix)

The `notification/handler.go` file shows the correct pattern:

```go
type accessKey struct{ namespace string }
access := make(map[accessKey]bool)
for _, s := range summaries {
    key := accessKey{s.Namespace}
    allowed, checked := access[key]
    if !checked {
        can, err := h.AccessChecker.CanAccess(...)
        allowed = err == nil && can
        access[key] = allowed
    }
    if allowed {
        filtered = append(filtered, s)
    }
}
```

**Pros:** Simple, deduplicates for namespaces with multiple quotas  
**Cons:** Still O(unique namespaces) but eliminates duplicates  
**Effort:** Small  
**Risk:** Low

### Solution B: Parallel RBAC Checks with Semaphore

Use `errgroup` with a semaphore to check 10-20 namespaces concurrently:

```go
func (h *Handler) filterByRBAC(ctx context.Context, user *auth.User, summaries []NamespaceSummary) []NamespaceSummary {
    type result struct {
        idx     int
        allowed bool
    }
    results := make(chan result, len(summaries))
    sem := make(chan struct{}, 20) // limit concurrency
    
    var wg sync.WaitGroup
    for i, s := range summaries {
        wg.Add(1)
        go func(idx int, ns string) {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()
            
            allowed, _ := h.AccessChecker.CanAccess(ctx, user.Username, user.KubernetesGroups, "get", "resourcequotas", ns)
            results <- result{idx, allowed}
        }(i, s.Namespace)
    }
    go func() { wg.Wait(); close(results) }()
    
    allowed := make([]bool, len(summaries))
    for r := range results {
        allowed[r.idx] = r.allowed
    }
    // filter based on allowed slice
}
```

**Pros:** 20x speedup for large namespaces  
**Cons:** More complex, need to handle context cancellation  
**Effort:** Medium  
**Risk:** Medium

### Solution C: Pre-cached RBAC Map (Per-Request)

Build a namespace->allowed map once per request and share across handlers.

**Pros:** Single computation point  
**Cons:** Requires middleware changes  
**Effort:** Large  
**Risk:** Medium

## Recommended Action

_To be filled during triage_

## Technical Details

**Affected Files:**
- `backend/internal/limits/handler.go`

**Scalability Table:**
| Scale | Current Behavior | Risk Level |
|-------|------------------|------------|
| 50 namespaces | Acceptable | Low |
| 200 namespaces | RBAC checks add 1-2s latency | Medium |
| 500 namespaces | RBAC checks add 3-5s latency | High |
| 1000 namespaces | RBAC checks likely timeout | Critical |

## Acceptance Criteria

- [ ] Dashboard loads in <2 seconds for clusters with 500+ namespaces
- [ ] RBAC checks are deduplicated per namespace
- [ ] No N+1 API call pattern on dashboard load
- [ ] Test verifies caching behavior

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-10 | Created from code review | notification/handler.go has correct pattern |

## Resources

- PR #164 code review
- `notification/handler.go` lines 226-243 for reference implementation
