---
name: ESO write handlers ignore X-Cluster-ID — audit/job-storage records header value while patching local cluster
status: pending
priority: p1
issue_id: 339
tags: [code-review, eso, phase-e, security, audit-integrity, multi-cluster]
dependencies: []
---

## Problem Statement

ESO Phase E write handlers (`HandleForceSyncExternalSecret`, `handleBulkRefresh`) read `clusterID` from `middleware.ClusterIDFromContext(ctx)` (the `X-Cluster-ID` header) and write that value into:
- `audit.Entry.ClusterID` (audit log row)
- `eso_bulk_refresh_jobs.cluster_id` (DB row)
- `FindActive` uniqueness key

But the underlying k8s client always uses `K8sClient.BaseDynamicClient()` / `K8sClient.DynamicClientForUser()` which is bound to the **LOCAL cluster** `ClientFactory`. Neither `actions.go` nor `bulk_worker.go` routes through `ClusterRouter`.

**Effect:** A user submitting `X-Cluster-ID: prod-cluster` on `POST /externalsecrets/.../force-sync` (or any bulk endpoint) triggers a real PATCH against the **local cluster** ExternalSecret, while the audit row + DB row claim the operation occurred on `prod-cluster`. This is:

1. **Audit log misattribution** that defeats forensic trace (compliance / incident response).
2. **`FindActive` uniqueness bypass** — a malicious admin can spawn multiple concurrent identical-scope bulk jobs against the local cluster by varying `X-Cluster-ID`.

CLAUDE.md Multi-Cluster Architecture explicitly states: "Remote clusters use direct API calls only — no informers, no WebSocket events. Local cluster uses informers." Phase E plan §694 acknowledges remote-cluster bulk refresh is out of scope for v1, but the handlers don't enforce that scope.

## Findings

- security reviewer (severity high, conf 0.85)
- learnings-researcher recommendation: "Don't accidentally let an X-Cluster-ID header trigger a remote impersonation path"

**Affected files:**
- `backend/internal/externalsecrets/actions.go` — `HandleForceSyncExternalSecret`
- `backend/internal/externalsecrets/bulk.go` — `handleBulkRefresh` (covers store / cluster-store / namespace variants)
- `backend/internal/externalsecrets/handler.go` — cache + dynForUser layer

## Proposed Solutions

### Option A — reject non-local cluster IDs (recommended for v1)

At each ESO write handler entry:

```go
clusterID := middleware.ClusterIDFromContext(r.Context())
if clusterID != "" && clusterID != "local" {
    httputil.WriteError(w, http.StatusNotImplemented,
        "ESO write actions are local-cluster only in v1", "")
    return
}
```

Apply at: `HandleForceSyncExternalSecret`, `HandleBulkRefreshStore`, `HandleBulkRefreshClusterStore`, `HandleBulkRefreshNamespace`.

The bulk worker also needs the guard at the entry of `processJob` for defense-in-depth (in case a job row was created before the guard landed).

### Option B — route through ClusterRouter

Switch `dynForUser` and the cache layer to `ClusterRouter.DynamicClientForCluster(clusterID, user)` so the executed cluster matches the recorded cluster. This is the long-term path but expands scope significantly: remote clusters have no informer cache, so the read path also needs rework.

**Recommendation:** Ship Option A for this PR; track Option B as a follow-up under the existing multi-cluster ESO scope item.

## Acceptance Criteria

- [ ] Force-sync POST with `X-Cluster-ID: <non-local>` returns 501 Not Implemented before any patch occurs.
- [ ] All three bulk-refresh POSTs return 501 Not Implemented for non-local cluster IDs.
- [ ] Bulk worker `processJob` rejects messages whose `ClusterID` is non-local (defensive).
- [ ] Test case: submitting `X-Cluster-ID: prod` on each endpoint produces 501 with no audit row, no DB row, no patch.
- [ ] Test case: `FindActive` uniqueness cannot be bypassed by varying `X-Cluster-ID` once the guard lands.
