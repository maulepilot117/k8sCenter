---
status: pending
priority: p3
issue_id: "304"
tags: [code-review, scanning, multi-cluster, architecture, pre-existing]
dependencies: []
---

# Thread X-Cluster-ID into scanning package

## Problem Statement

CLAUDE.md documents that multi-cluster operations route via `X-Cluster-ID` header through `ClusterRouter`. The entire scanning package ignores this — both the pre-existing `HandleVulnerabilities` and the new `HandleVulnerabilityDetail` call `h.K8sClient.BaseDynamicClient()`, which always hits the local cluster.

Users with multiple clusters registered will silently see local-cluster data only on the Security Vulnerabilities page, regardless of which cluster is selected.

**Why it matters:** Violates multi-cluster architecture; confuses users in multi-cluster deployments.

**Scope:** Pre-existing gap. PR #167 inherits but doesn't cause it. Not a blocker for that PR.

## Findings

### Architecture Strategist + Security Sentinel

**Files:**
- `backend/internal/scanning/handler.go:92` (pre-existing `HandleVulnerabilities`)
- `backend/internal/scanning/handler.go:305` (new `HandleVulnerabilityDetail`)

Both call `h.K8sClient.BaseDynamicClient()` unconditionally.

**Compare:** `backend/internal/k8s/cluster_router.go` routes client requests by `X-Cluster-ID` context.

## Proposed Solutions

### Option A: Add ClusterRouter support to scanning Handler

```go
type Handler struct {
    K8sClient     *k8s.ClientFactory
    ClusterRouter *k8s.ClusterRouter  // NEW
    // ...
}

// In each handler:
dynClient, err := h.ClusterRouter.DynamicClientForRequest(r)
if err != nil { ... }
```

**Pros:** Proper multi-cluster support
**Cons:** Cache keys need clusterID; same issue in policy/gitops/velero — better solved at project-wide level
**Effort:** Medium per-package, Large if rolled out consistently
**Risk:** Medium — cache invalidation semantics change

### Option B: Document limitation, defer to project-wide refactor

Add a comment and UI warning for non-local clusters.

**Pros:** Honest about current state
**Cons:** User confusion persists
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage. Likely Option B until project-wide approach is decided. -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/handler.go`
- `backend/internal/scanning/discovery.go` (discovery also needs per-cluster state)
- Possibly: `backend/internal/policy/handler.go`, `backend/internal/gitops/handler.go`, `backend/internal/velero/handler.go` (same pattern)

## Acceptance Criteria

- [ ] Decision made: fix in scanning only, project-wide, or document
- [ ] Non-local clusters either work or show clear message

## Work Log

### 2026-04-11 — PR #167 partial mitigation

Option B applied: added a doc comment to `scanning.Handler` type in `backend/internal/scanning/handler.go` acknowledging that multi-cluster routing is not threaded through the scanning package. Reads always hit the local cluster.

Full fix (adding `ClusterRouter` to the scanning Handler struct and cache keys) deferred to a project-wide refactor covering scanning/policy/gitops/velero consistently.

## Resources

- PR #167
- CLAUDE.md "Multi-Cluster Architecture"
