---
status: complete
priority: p1
issue_id: "024"
tags: [code-review, security, rbac]
dependencies: []
---

# RBAC Bypass on Cross-Namespace List Operations

## Problem Statement
When listing resources without a namespace parameter, all 12 namespaced List handlers skip the RBAC access check entirely. The informer cache returns ALL resources across ALL namespaces regardless of user permissions. Any authenticated user can see resources in namespaces they should not have access to, completely undermining namespace-scoped RBAC isolation.

## Findings
All 12 namespaced List handlers have an `else` branch (the all-namespace code path) that omits the `checkAccess` call present in the namespace-scoped branch.

- `deployments.go:27-36` — all-namespace list skips checkAccess
- `configmaps.go:22-30` — all-namespace list skips checkAccess
- `services.go` — same pattern
- `ingresses.go` — same pattern
- `daemonsets.go` — same pattern
- `statefulsets.go` — same pattern
- `pods.go` — same pattern
- `networkpolicies.go` — same pattern
- `pvcs.go` — same pattern
- `jobs.go` — Jobs and CronJobs both affected
- `rbac_viewer.go` — Roles and RoleBindings both affected

Flagged by: Security Sentinel (Finding 1), Performance Oracle (Finding 7), Architecture Strategist (Finding 2), Pattern Recognition (Finding 1).

## Proposed Solutions
### Option A: Cluster-scoped checkAccess with empty namespace
Add a single `checkAccess` call with an empty namespace string for the all-namespace path. This checks whether the user has cluster-wide list permission for the resource kind.
- **Pros:** Simple, 1 line per handler, consistent with Kubernetes RBAC semantics
- **Cons:** Coarse-grained; blocks users who have access to some namespaces but not cluster-wide list
- **Effort:** Small
- **Risk:** Low

### Option B: Per-namespace filtering with batched access checks
Enumerate unique namespaces from the informer results, batch-check access per namespace via the RBAC cache, and filter results before pagination.
- **Pros:** Fine-grained; users see exactly what they are allowed to see across namespaces
- **Cons:** More complex (~30 lines per handler or extracted into shared helper); depends on access cache for performance
- **Effort:** Medium
- **Risk:** Low (access cache mitigates API server load)

### Option C: Cluster-wide check with per-namespace fallback
Check cluster-wide access first. If granted, return all results. If denied, fall back to per-namespace filtering (Option B).
- **Pros:** Fast path for cluster-admins, correct for namespace-scoped users
- **Cons:** Slightly more complex than B alone
- **Effort:** Medium
- **Risk:** Low

## Recommended Action


## Technical Details
- **Affected files:** All 12 namespaced List handlers in `backend/internal/k8s/resources/` — `deployments.go`, `configmaps.go`, `services.go`, `ingresses.go`, `daemonsets.go`, `statefulsets.go`, `pods.go`, `networkpolicies.go`, `pvcs.go`, `jobs.go`, `rbac_viewer.go`
- **Components:** Resource listing, RBAC enforcement, informer cache

## Acceptance Criteria
- [ ] All-namespace list returns only resources from namespaces the user has list permission for
- [ ] Users without cluster-wide list permission can still list resources from their authorized namespaces
- [ ] Test verifies that resources from denied namespaces are filtered out of all-namespace responses
- [ ] No regression in namespace-scoped list behavior

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-12 | Created | From PR #3 code review |

## Resources
- PR: #3
