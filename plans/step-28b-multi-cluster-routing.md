# Step 28B: Multi-Cluster Backend Routing (Post-Review)

## Overview

Wire up the backend to route API requests to the correct cluster based on `X-Cluster-ID` header. Core multi-cluster plumbing — all requests currently go to local cluster regardless.

## Review Feedback Applied

- Always construct ClusterRouter (nil store → local fallback), no nil-guards in handler (DHH)
- Extract `auth.IsAdmin()` helper, use in both RequireAdmin and ClusterContext (Security, DHH)
- DNS re-resolution at connection time to prevent rebinding SSRF (Security)
- Add CGNAT `100.64.0.0/10` to SSRF blocklist (Security)
- Add `len(clusterID) > 64` length cap (Security)
- Don't store `rest.Config` in cache — only clientset + expiry (Security)
- Add cache eviction on credential update, not just delete (Security)
- Add `StartCacheSweeper` for remote cache (DHH)
- Reuse `cacheKey` function, don't duplicate hashing (DHH)
- 63 call-site changes are unavoidable — all reviewers agree, proceed mechanically

## Implementation Plan (7 items)

### 1. Extract `auth.IsAdmin()` Helper

**File: `backend/internal/auth/provider.go`**

```go
// IsAdmin returns true if the user has the "admin" role.
func IsAdmin(u *User) bool {
    for _, r := range u.Roles {
        if r == "admin" {
            return true
        }
    }
    return false
}
```

Update `middleware.RequireAdmin` to use `auth.IsAdmin(user)` instead of inline loop.

### 2. ClusterContext Middleware

**New file: `backend/internal/server/middleware/cluster.go`**

Pure header-to-context extractor + admin gate for non-local clusters:

```go
func ClusterContext(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        clusterID := r.Header.Get("X-Cluster-ID")
        if clusterID == "" || clusterID == "local" {
            clusterID = "local"
        } else {
            // Validate header length
            if len(clusterID) > 64 {
                writeError(w, 400, "invalid X-Cluster-ID")
                return
            }
            // Non-local cluster access requires admin role
            user, ok := auth.UserFromContext(r.Context())
            if !ok || !auth.IsAdmin(user) {
                writeError(w, 403, "admin role required for remote cluster access")
                return
            }
        }
        ctx := context.WithValue(r.Context(), clusterIDKey, clusterID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

Insert in `routes.go` after `middleware.CSRF` on the authenticated group.

### 3. ClusterRouter

**New file: `backend/internal/k8s/cluster_router.go`**

```go
type ClusterRouter struct {
    localFactory  *ClientFactory
    clusterStore  *store.ClusterStore  // nil for local-only deployments
    encryptionKey string
    remoteCache   sync.Map             // map[string]cachedClient
    remoteDynCache sync.Map
    logger        *slog.Logger
}

func NewClusterRouter(local *ClientFactory, cs *store.ClusterStore, encKey string, logger *slog.Logger) *ClusterRouter
```

**Always constructed** in main.go. If `clusterStore` is nil, all `ClientForCluster` calls fall through to `localFactory`.

**ClientForCluster:**
```go
func (cr *ClusterRouter) ClientForCluster(ctx context.Context, clusterID, username string, groups []string) (*kubernetes.Clientset, error) {
    if clusterID == "" || clusterID == "local" || cr.clusterStore == nil {
        return cr.localFactory.ClientForUser(username, groups)
    }
    return cr.remoteClient(ctx, clusterID, username, groups)
}
```

**remoteClient:**
1. Cache key: reuse existing `cacheKey` pattern with clusterID prefix: `clusterID + "\x00" + cacheKey(username, groups)`
2. Check `remoteCache` — return if not expired (5 min TTL)
3. Fetch `ClusterRecord` from store
4. Decrypt `AuthData` and `CAData` using `store.Decrypt`
5. **DNS re-resolution + IP blocklist check** on `APIServerURL` hostname (prevent SSRF rebinding)
6. Build `rest.Config{Host, BearerToken, TLSClientConfig{CAData}}`
7. Set impersonation: `config.Impersonate = rest.ImpersonationConfig{UserName, Groups}`
8. Create clientset, store **only clientset + expiry** in cache (NOT rest.Config)

**SSRF check at connection time:**
```go
func validateRemoteURL(apiServerURL string) error {
    u, _ := url.Parse(apiServerURL)
    ips, err := net.LookupHost(u.Hostname())
    for _, ipStr := range ips {
        ip := net.ParseIP(ipStr)
        if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
            return fmt.Errorf("remote cluster URL resolves to private IP")
        }
        // CGNAT range (Tailscale, WireGuard)
        if cgnatNet.Contains(ip) {
            return fmt.Errorf("remote cluster URL resolves to CGNAT range")
        }
    }
    return nil
}
var cgnatNet = &net.IPNet{IP: net.ParseIP("100.64.0.0"), Mask: net.CIDRMask(10, 32)}
```

**EvictCluster(clusterID):** Iterate `remoteCache` and `remoteDynCache`, delete entries with matching clusterID prefix.

**StartCacheSweeper(ctx):** Same pattern as `ClientFactory.StartCacheSweeper` — goroutine every 60s, evict expired entries.

### 4. Wire Resource Handlers

**File: `backend/internal/k8s/resources/handler.go`**

Update Handler struct — replace `K8sClient *k8s.ClientFactory` usage with `ClusterRouter`:

```go
type Handler struct {
    K8sClient       *k8s.ClientFactory   // still needed for informer access
    ClusterRouter   *k8s.ClusterRouter   // used for all impersonated client creation
    // ... rest unchanged
}
```

Update `impersonatingClient` and `impersonatingDynamic` — **no nil-guard**:

```go
func (h *Handler) impersonatingClient(r *http.Request, user *auth.User) (*kubernetes.Clientset, error) {
    clusterID := middleware.ClusterIDFromContext(r.Context())
    return h.ClusterRouter.ClientForCluster(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
}

func (h *Handler) impersonatingDynamic(r *http.Request, user *auth.User) (dynamic.Interface, error) {
    clusterID := middleware.ClusterIDFromContext(r.Context())
    return h.ClusterRouter.DynamicClientForCluster(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
}
```

**Mechanical find-and-replace (63 call sites):**
- `h.impersonatingClient(user)` → `h.impersonatingClient(r, user)` (60 sites)
- `h.impersonatingDynamic(user)` → `h.impersonatingDynamic(r, user)` (3 sites in cilium.go)

Before executing: grep for ALL private Handler methods that call `impersonatingClient` to catch helpers like `restartWorkload`.

### 5. SSRF Protection on Registration

**File: `backend/internal/server/handle_clusters.go`**

Same `validateRemoteURL` check as item 3, plus:
- Check runs BEFORE connection test
- Connection test error messages sanitized: return only "connection failed", "TLS certificate error", "authentication failed" — never raw Go errors

### 6. Cache Eviction on Delete + Credential Update

**File: `backend/internal/server/handle_clusters.go`**

After `clusterStore.Delete()`:
```go
s.ClusterRouter.EvictCluster(clusterID)
```

Also add eviction when credentials are updated (if `UpdateCredentials` is exposed via an API endpoint — check if it is called from any handler).

### 7. Wiring in main.go + routes.go

**main.go:**
```go
clusterRouter := k8s.NewClusterRouter(clientFactory, clusterStore, cfg.Database.EncryptionKey, logger)
go clusterRouter.StartCacheSweeper(ctx)

// Pass to resource handler
resourceHandler.ClusterRouter = clusterRouter
// Pass to server for delete handler eviction
srv.ClusterRouter = clusterRouter
```

**routes.go:**
Insert `middleware.ClusterContext` after `middleware.CSRF` on the authenticated group.

---

## Acceptance Criteria

- [ ] `auth.IsAdmin()` helper extracted and used by RequireAdmin + ClusterContext
- [ ] ClusterContext middleware extracts cluster ID, gates non-local on admin role
- [ ] ClusterRouter always constructed (nil store → local fallback)
- [ ] No nil-guards on ClusterRouter in handler code
- [ ] Remote client cache: separate sync.Map, cluster-prefixed key, stores only clientset+expiry
- [ ] SSRF: DNS re-resolved at connection time, CGNAT range blocked, header length capped
- [ ] All 63 call sites updated from `impersonatingClient(user)` to `impersonatingClient(r, user)`
- [ ] Cache evicted on cluster delete AND credential update
- [ ] Cache sweeper goroutine for remote cache
- [ ] Connection test errors sanitized (no raw Go errors)
- [ ] `go vet ./...` passes
- [ ] `go test -race ./...` passes

## Implementation Order

1. `auth.IsAdmin()` helper (tiny, enables later work)
2. ClusterContext middleware (new file)
3. ClusterRouter (new file, core logic)
4. Update Handler — new signatures for impersonatingClient/impersonatingDynamic
5. Mechanical 63-site find-and-replace
6. SSRF protection on registration
7. Cache eviction on delete + credential update
8. Wire in main.go + routes.go
9. Test: go vet, go test -race, helm lint

## References

- ClientFactory: `backend/internal/k8s/client.go:40-52, 129, 166, 258`
- Handler: `backend/internal/k8s/resources/handler.go:22-33, 71, 77`
- Auth User: `backend/internal/auth/provider.go:9-16, 57-71`
- RequireAdmin: `backend/internal/server/middleware/auth.go:69`
- Routes: `backend/internal/server/routes.go:73`
- ClusterStore: `backend/internal/store/clusters.go:32-41, 69`
- Decrypt: `backend/internal/store/encrypt.go:44`
- handleDeleteCluster: `backend/internal/server/handle_clusters.go`
