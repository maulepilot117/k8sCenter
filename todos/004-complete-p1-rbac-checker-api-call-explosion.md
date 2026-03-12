---
status: complete
priority: p1
issue_id: "004"
tags: [code-review, performance]
dependencies: []
---

# RBAC Checker Makes O(N*M*5) Sequential API Calls

## Problem Statement
`RBACChecker.GetSummary` checks 5 verbs for 10 namespace-scoped resources across ALL namespaces, plus 5 verbs for 4 cluster-scoped resources. Each check is a separate `SelfSubjectAccessReview` API call issued sequentially.

- 10 namespaces: 520 API calls per `/auth/me` request
- 50 namespaces: 2,520 calls
- 200 namespaces: 10,020 calls (~50 seconds at 5ms/call)

The 60s cache only masks the problem — first call after expiry blocks for seconds/minutes.

## Findings
- **Source**: Performance agent (CRITICAL-1)
- **File**: `backend/internal/auth/rbac.go:68-91`
- **Also**: `routes.go:372` passes ALL namespaces including system ones

## Proposed Solutions

### Option A: Use SelfSubjectRulesReview (Recommended)
Replace per-verb checks with `SelfSubjectRulesReview`, which returns all permissions for a namespace in a single API call. Reduces calls from N*M*5 to N.
- **Effort**: Medium
- **Risk**: Low

### Option B: Concurrent Workers with errgroup
Issue checks concurrently with a bounded worker pool (10-20 goroutines).
- **Effort**: Small
- **Risk**: Medium — still hammers API server, just faster

### Option C: Filter Namespaces
Exclude system namespaces, limit to configurable max.
- **Effort**: Small
- **Risk**: Low — but doesn't fix the core issue

## Acceptance Criteria
- [ ] `/auth/me` completes in <2 seconds for clusters with 50+ namespaces
- [ ] API server load is proportional to namespace count, not namespace * resource * verb
- [ ] System namespaces filtered from RBAC summary
