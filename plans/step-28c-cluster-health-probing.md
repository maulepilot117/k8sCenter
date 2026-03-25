# Step 28C: Cluster Health Probing + Connection Test (Post-Review)

## Overview

Add background health probing for remote clusters, connection testing on registration, and display probe results in the cluster manager UI. Final piece of multi-cluster (28A fixed encryption, 28B wired routing, 28C adds visibility).

## Review Feedback Applied

- `probeOne` uses `clusterStore.Get(id)` to fetch credentials (List() omits them) (DHH)
- `ValidateRemoteURL` called in prober before connecting (SSRF defense) (Security)
- Sequential probing, no goroutines per cluster (DHH — 2-3 clusters, not worth parallelism)
- Node count via `limit=500` (DHH — `limit=1` returns 1 for any cluster with nodes)
- `ProbeOne` exported method shared by prober + test endpoint — no duplication (DHH)
- `sanitizeError` strips all raw errors, used in all paths (Security)
- Test endpoint returns fresh record directly — no second list fetch (DHH)
- Insecure TLS: log on state transition only, never auto-downgrade when CA present (Security)
- Test endpoint rate-limited, admin-only, audit logged (Security)

## Implementation Plan (4 items)

### 1. Cluster Health Prober

**New file: `backend/internal/k8s/cluster_prober.go`**

```go
type ClusterProber struct {
    clusterStore *store.ClusterStore
    encKey       string
    logger       *slog.Logger
}

func NewClusterProber(cs *store.ClusterStore, encKey string, logger *slog.Logger) *ClusterProber

// Run starts the probing loop. Probes immediately, then every 60s.
func (p *ClusterProber) Run(ctx context.Context) {
    p.probeAll(ctx)
    ticker := time.NewTicker(60 * time.Second)
    defer ticker.Stop()
    for { select { case <-ctx.Done(): return; case <-ticker.C: p.probeAll(ctx) } }
}
```

**`probeAll`:** List clusters, probe each non-local **sequentially**:
```go
func (p *ClusterProber) probeAll(ctx context.Context) {
    clusters, _ := p.clusterStore.List(ctx)
    for _, c := range clusters {
        if c.IsLocal { continue }
        p.ProbeOne(ctx, c.ID) // sequential — no goroutines
    }
}
```

**`ProbeOne`** (exported — shared by prober + test endpoint):
```go
func (p *ClusterProber) ProbeOne(ctx context.Context, clusterID string) (*store.ClusterRecord, error) {
    // 1. Fetch full record with credentials
    cluster, err := p.clusterStore.Get(ctx, clusterID)
    if err != nil { return nil, err }

    // 2. SSRF check — re-resolve DNS at probe time
    if err := ValidateRemoteURL(cluster.APIServerURL); err != nil {
        p.clusterStore.UpdateStatus(ctx, clusterID, "blocked", "URL resolves to private address", "", 0)
        return p.clusterStore.Get(ctx, clusterID) // refetch with updated status
    }

    // 3. Decrypt credentials
    token, err := store.Decrypt(cluster.AuthData, p.encKey)
    // caData decrypt if present

    // 4. Build rest.Config
    cfg := &rest.Config{Host: cluster.APIServerURL, BearerToken: string(token)}
    if len(caData) > 0 {
        cfg.TLSClientConfig.CAData = caData
    } else if len(cluster.CAData) > 0 {
        // CA was stored but decryption failed — don't auto-downgrade to insecure
        p.clusterStore.UpdateStatus(ctx, clusterID, "error", "credential error", "", 0)
        return ...
    } else {
        cfg.TLSClientConfig.Insecure = true
        // Log only on state transition (not every cycle)
    }

    // 5. Probe with 10s timeout
    probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    cs, _ := kubernetes.NewForConfig(cfg)
    version, err := cs.Discovery().ServerVersion()
    if err != nil {
        p.clusterStore.UpdateStatus(ctx, clusterID, "disconnected", sanitizeError(err), "", 0)
        return ...
    }

    // 6. Node count (limit=500 — sufficient for any real cluster)
    nodes, _ := cs.CoreV1().Nodes().List(probeCtx, metav1.ListOptions{Limit: 500})
    nodeCount := len(nodes.Items)

    // 7. Update status
    p.clusterStore.UpdateStatus(ctx, clusterID, "connected", "", version.GitVersion, nodeCount)
    return p.clusterStore.Get(ctx, clusterID) // return fresh record
}
```

**`sanitizeError`** — pattern-match, never return raw `err.Error()`:
```go
func sanitizeError(err error) string {
    s := err.Error()
    switch {
    case strings.Contains(s, "connection refused"):
        return "connection refused"
    case strings.Contains(s, "i/o timeout") || strings.Contains(s, "deadline exceeded"):
        return "connection timeout"
    case strings.Contains(s, "certificate") || strings.Contains(s, "tls"):
        return "TLS certificate error"
    case strings.Contains(s, "401") || strings.Contains(s, "403") || strings.Contains(s, "Unauthorized"):
        return "authentication failed"
    case strings.Contains(s, "no such host"):
        return "DNS resolution failed"
    default:
        return "connection error"
    }
}
```

**Wire in main.go:**
```go
if clusterStore != nil {
    clusterProber := k8s.NewClusterProber(clusterStore, dbEncKey, logger)
    go clusterProber.Run(ctx)
}
```

### 2. Connection Test on Registration

**File: `backend/internal/server/handle_clusters.go`**

After SSRF check, before `Create()`:
- Build temp `rest.Config` from supplied URL + token + CA
- `context.WithTimeout(10s)`
- `Discovery().ServerVersion()` — on failure return 400 with sanitized error
- On success: carry version forward, call `UpdateStatus` after `Create()` with initial probe results

### 3. Test Connection Endpoint

**New endpoint: `POST /api/v1/clusters/:clusterID/test`**

```go
func (s *Server) handleTestCluster(w http.ResponseWriter, r *http.Request) {
    // Guards: ClusterStore != nil, clusterID valid
    id := chi.URLParam(r, "clusterID")

    // Call the shared prober method
    record, err := s.ClusterProber.ProbeOne(r.Context(), id)
    if err != nil {
        writeJSON(w, 400, api.Response{Error: ...})
        return
    }

    // Audit log
    user, _ := auth.UserFromContext(r.Context())
    // ... log test action

    // Return fresh record directly — no second list fetch
    writeJSON(w, 200, api.Response{Data: record})
}
```

**Route registration:** Inside the existing `cr.Use(middleware.RequireAdmin)` block in routes.go. Rate-limited (share YAML limiter or own bucket).

### 4. Frontend: Display Probe Results + Test Button

**File: `frontend/islands/ClusterManager.tsx`**

Add `lastProbedAt` and `statusMessage` to the existing display:

```tsx
// In cluster row subtitle:
{c.lastProbedAt && <span> · checked {age(c.lastProbedAt)}</span>}

// Show error when not connected:
{c.status !== "connected" && c.statusMessage && (
    <p class="text-xs text-danger mt-0.5">{c.statusMessage}</p>
)}
```

Add "Test" button per remote cluster:
```tsx
<Button size="sm" variant="secondary" onClick={async () => {
    const res = await apiPost(`/v1/clusters/${c.id}/test`);
    // Update the cluster in the list with the returned record
}}>
    Test
</Button>
```

---

## Acceptance Criteria

- [ ] Remote clusters probed every 60s sequentially with 10s timeout each
- [ ] `clusterStore.Get()` used per cluster (not List which omits credentials)
- [ ] `ValidateRemoteURL` called before every probe (SSRF defense)
- [ ] Connection tested before registration — fails with sanitized error
- [ ] `POST /clusters/:id/test` admin-only, rate-limited, returns fresh record
- [ ] `sanitizeError` strips all raw Go errors, used in all paths
- [ ] Node count via `limit=500`
- [ ] Insecure TLS logged on state transition only, never auto-downgraded when CA present
- [ ] ClusterManager shows lastProbedAt, statusMessage, Test button
- [ ] `go vet` + `go test -race` passes

## Implementation Order

1. `cluster_prober.go` — ProbeOne + sanitizeError + Run loop
2. Connection test in handleCreateCluster
3. `handleTestCluster` endpoint + route registration
4. Wire prober in main.go
5. Frontend: lastProbedAt + statusMessage + Test button

## References

- ClusterStore.UpdateStatus: `backend/internal/store/clusters.go:133`
- ClusterStore.Get: `backend/internal/store/clusters.go:69` (returns credentials)
- ClusterStore.List: `backend/internal/store/clusters.go:44` (no credentials)
- handleCreateCluster: `backend/internal/server/handle_clusters.go`
- ValidateRemoteURL: `backend/internal/k8s/cluster_router.go`
- ClusterManager: `frontend/islands/ClusterManager.tsx`
- Admin cluster routes: `backend/internal/server/routes.go:142-148`
