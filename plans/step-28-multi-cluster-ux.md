# Step 28: Multi-Cluster UX (Post-Review, 3-PR Split)

## Overview

Wire up multi-cluster support end-to-end. Split into 3 PRs after review: bugfix, routing plumbing, health probing.

## Review Feedback Applied

- Split from 1 PR into 3 shippable increments (all 3 reviewers)
- Added: X-Cluster-ID must require admin role for non-local clusters (Security Critical)
- Added: SSRF blocklist for private IPs on cluster registration (Security Critical)
- Added: Remote client cache key must include cluster ID (Security High)
- Added: Cache eviction on cluster deletion (Security High)
- Added: Sanitize connection test error messages
- Added: Use interface (not full ClusterStore) for ClientFactory dependency
- Added: DynamicClientForCluster for CRD operations

## PR 28A: Bugfix + Frontend Persistence (ship immediately)

### 1. Fix UpdateCredentials Encryption Bug

**File: `backend/internal/store/clusters.go`**

`UpdateCredentials()` writes raw bytes. `Create()` encrypts first. Fix by encrypting before UPDATE.

### 2. Persist Cluster Selection to localStorage

**File: `frontend/lib/cluster.ts`**

```typescript
const stored = IS_BROWSER ? localStorage.getItem("k8scenter.selectedCluster") : null;
export const selectedCluster = signal(stored ?? "local");
effect(() => {
    if (IS_BROWSER) localStorage.setItem("k8scenter.selectedCluster", selectedCluster.value);
});
```

### 3. Page Reload on Cluster Switch

**File: `frontend/islands/ClusterSelector.tsx`**

On cluster change, set signal + reload:
```typescript
selectedCluster.value = newClusterId;
globalThis.location.reload();
```

---

## PR 28B: Backend Routing Plumbing (after 28A merged)

### 4. Cluster-Aware Middleware with Admin Gate

**New file: `backend/internal/server/middleware/cluster.go`**

```go
func ClusterContext(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        clusterID := r.Header.Get("X-Cluster-ID")
        if clusterID == "" || clusterID == "local" {
            clusterID = "local"
        } else {
            // Non-local cluster access requires admin role
            user := auth.UserFromContext(r.Context())
            if user == nil || !user.IsAdmin {
                writeJSON(w, 403, map[string]string{"error": "admin role required for remote cluster access"})
                return
            }
        }
        ctx := context.WithValue(r.Context(), clusterIDKey, clusterID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### 5. Multi-Cluster Client Factory

**File: `backend/internal/k8s/client.go`**

Add `ClusterCredentialGetter` interface (narrow dependency, no import cycle):
```go
type ClusterCredentialGetter interface {
    Get(ctx context.Context, id string) (*store.ClusterRecord, error)
}
```

Add `ClientForCluster(ctx, clusterID, username, groups)`:
- If local → delegate to existing `ClientForUser`
- If remote → look up cluster, decrypt creds, build rest.Config, impersonate
- Separate `sync.Map` for remote clients, cache key includes clusterID
- `DynamicClientForCluster` for CRD operations

Add `EvictClusterCache(clusterID)` for cache invalidation on cluster deletion.

### 6. Wire Resource Handlers

**File: `backend/internal/k8s/resources/handler.go`**

Add `getClusterClient(r, user)` that reads cluster ID from context and delegates:
```go
func (h *Handler) getClusterClient(r *http.Request, user string, groups []string) (*kubernetes.Clientset, error) {
    clusterID := middleware.ClusterIDFromContext(r.Context())
    return h.clientFactory.ClientForCluster(r.Context(), clusterID, user, groups)
}
```

Replace all `h.impersonatingClient(user, groups)` calls with `h.getClusterClient(r, user, groups)`.

### 7. SSRF Protection on Cluster Registration

**File: `backend/internal/server/handle_clusters.go`**

Before connection test, validate URL is not a private IP:
```go
func isPrivateIP(host string) bool {
    ip := net.ParseIP(host)
    if ip == nil { /* resolve hostname, check result */ }
    return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
```

Sanitize connection test error messages — return only "connection failed", "TLS error", or "auth failed".

### 8. Cache Eviction on Cluster Deletion

**File: `backend/internal/server/handle_clusters.go`**

After deleting a cluster from the store, call `clientFactory.EvictClusterCache(clusterID)`.

---

## PR 28C: Health Probing + UX (after 28B validated)

### 9. Cluster Health Probing

New background goroutine (started from main.go):
- Every 60 seconds, list all remote clusters
- `GET /version` with 10s timeout per cluster
- Update status, k8s_version, node_count, last_probed_at
- Use `context.WithTimeout` for each probe

### 10. Connection Test on Registration

**File: `backend/internal/server/handle_clusters.go`**

Before saving: build temp rest.Config, call `GET /version`, store result. Sanitize errors.

### 11. Cluster Health Dashboard

Update `ClusterManager.tsx` to show probing results: status dots, version, node count, last probed time.

---

## Acceptance Criteria

### PR 28A
- [ ] `UpdateCredentials` encrypts CA and auth data
- [ ] Cluster selection persists across page reload (localStorage)
- [ ] Page reloads when switching clusters

### PR 28B
- [ ] X-Cluster-ID middleware extracts cluster from header
- [ ] Non-local cluster access requires admin role
- [ ] Remote cluster client created from stored credentials
- [ ] All resource handlers route to correct cluster
- [ ] SSRF blocklist rejects private IPs on registration
- [ ] Remote client cache uses cluster-prefixed keys
- [ ] Cache evicted when cluster is deleted
- [ ] `go test -race` passes

### PR 28C
- [ ] Remote clusters probed every 60s with 10s timeout
- [ ] Status/version/node count updated in DB
- [ ] Connection tested before registration saves
- [ ] Cluster manager shows live health status

## References

- Cluster store: `backend/internal/store/clusters.go`
- Client factory: `backend/internal/k8s/client.go`
- Resource handler: `backend/internal/k8s/resources/handler.go`
- Cluster handler: `backend/internal/server/handle_clusters.go`
- Cluster selector: `frontend/islands/ClusterSelector.tsx`
- Cluster state: `frontend/lib/cluster.ts`
