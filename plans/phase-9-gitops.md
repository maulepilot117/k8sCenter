# Phase 9: GitOps Integration — Implementation Plan (v2)

## Overview

Phase 9 adds GitOps tool integration (Argo CD + Flux CD), providing a unified applications dashboard showing sync status, health, managed resources, and revision history. Auto-detects which tool(s) are deployed, normalizes data across both. Two sub-phases: 9A (backend) + 9B (frontend).

**Architecture:** Follows the Phase 8 (Policy) pattern — CRD-based discovery, dual adapter normalization, singleflight+cache handler, per-user RBAC filtering via impersonation.

**Changes from v1 (plan review feedback):**
- **Merged 9A+9B into single backend phase** — can't test adapters without handler wired up
- **Killed history table** — Flux Kustomizations don't track history natively; polling via handler is unreliable. Argo + Flux HelmRelease have CRD-native history. Return empty for Kustomizations.
- **Killed Sources endpoint + page** — Flux-only concern, users can browse source CRDs via existing resource browser
- **Killed standalone Compliance page** — "which apps are broken?" answered by the applications table directly. Inline summary counts at top of Applications page.
- **Fixed composite ID delimiter** — changed from `/` (breaks chi routing) to `:` (e.g., `argo:argocd:my-app`)
- **Dropped `Labels`, `AttemptedRevision` from NormalizedApp** — YAGNI
- **Merged `HealthMissing` into `HealthDegraded`** — Argo-specific edge case, same user action
- **Use `sync.RWMutex` not `atomic.Value`** — match policy handler pattern

---

## Phase 9A: Backend — Discovery, Adapters, Handler

**Branch:** `feat/phase9a-gitops-backend`

### Step 9A.1 — Types

**New file:** `backend/internal/gitops/types.go`

```go
type Tool string
const (
    ToolNone    Tool = ""
    ToolArgoCD  Tool = "argocd"
    ToolFluxCD  Tool = "fluxcd"
    ToolBoth    Tool = "both"
)

type SyncStatus string
const (
    SyncSynced      SyncStatus = "synced"
    SyncOutOfSync   SyncStatus = "outofsync"
    SyncProgressing SyncStatus = "progressing"
    SyncStalled     SyncStatus = "stalled"      // Flux-specific
    SyncFailed      SyncStatus = "failed"
    SyncUnknown     SyncStatus = "unknown"
)

type HealthStatus string
const (
    HealthHealthy     HealthStatus = "healthy"
    HealthDegraded    HealthStatus = "degraded"    // includes Argo "Missing"
    HealthProgressing HealthStatus = "progressing"
    HealthSuspended   HealthStatus = "suspended"
    HealthUnknown     HealthStatus = "unknown"
)

type GitOpsStatus struct {
    Detected    Tool         `json:"detected"`
    ArgoCD      *ToolDetail  `json:"argocd,omitempty"`
    FluxCD      *ToolDetail  `json:"fluxcd,omitempty"`
    LastChecked string       `json:"lastChecked"`
}

type ToolDetail struct {
    Available   bool     `json:"available"`
    Namespace   string   `json:"namespace,omitempty"`
    Controllers []string `json:"controllers,omitempty"` // Flux: ["source","kustomize","helm"]
}

type NormalizedApp struct {
    ID                   string    `json:"id"`           // "argo:argocd:my-app" or "flux-ks:ns:name" or "flux-hr:ns:name"
    Name                 string    `json:"name"`
    Namespace            string    `json:"namespace"`
    Tool                 Tool      `json:"tool"`
    Kind                 string    `json:"kind"`         // Application, Kustomization, HelmRelease
    SyncStatus           SyncStatus   `json:"syncStatus"`
    HealthStatus         HealthStatus `json:"healthStatus"`
    Source               AppSource `json:"source"`
    CurrentRevision      string    `json:"currentRevision,omitempty"`
    LastSyncTime         string    `json:"lastSyncTime,omitempty"`
    Message              string    `json:"message,omitempty"`
    DestinationCluster   string    `json:"destinationCluster,omitempty"`
    DestinationNamespace string    `json:"destinationNamespace,omitempty"`
    ManagedResourceCount int       `json:"managedResourceCount"`
    Suspended            bool      `json:"suspended"`
}

type AppSource struct {
    RepoURL        string `json:"repoURL,omitempty"`
    Path           string `json:"path,omitempty"`
    TargetRevision string `json:"targetRevision,omitempty"`
    ChartName      string `json:"chartName,omitempty"`
    ChartVersion   string `json:"chartVersion,omitempty"`
}

type ManagedResource struct {
    Group     string `json:"group,omitempty"`
    Kind      string `json:"kind"`
    Namespace string `json:"namespace,omitempty"`
    Name      string `json:"name"`
    Status    string `json:"status"`    // Synced, OutOfSync, etc.
    Health    string `json:"health,omitempty"`
}

type RevisionEntry struct {
    Revision   string `json:"revision"`
    Status     string `json:"status"`
    Message    string `json:"message,omitempty"`
    DeployedAt string `json:"deployedAt"`
}
```

### Step 9A.2 — Discovery

**New file:** `backend/internal/gitops/discovery.go`

Follow the policy discoverer pattern (`sync.RWMutex`, cached status, 5-min ticker):

- Constructor: `NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger)`
  - Note: no `CRDDiscovery` dependency (intentional simplification vs policy — GitOps doesn't need dynamic constraint enumeration)
- `RunDiscoveryLoop(ctx)` — immediate + 5-min ticker
- `Discover(ctx)`:
  1. CRD check: `argoproj.io/v1alpha1` for `Application` kind → Argo CD present
  2. CRD check: `kustomize.toolkit.fluxcd.io/v1` for `Kustomization` kind → Flux present
  3. CRD check: `helm.toolkit.fluxcd.io/v2` for `HelmRelease` kind → Flux Helm support
  4. Namespace probe: running pods in `argocd` / `flux-system`
  5. For Flux: enumerate which controllers are installed (source, kustomize, helm)
- `Status()` accessor: returns copy of `GitOpsStatus`
- **Multi-cluster:** Discovery runs against local cluster only. Remote reads via ClusterRouter per-request.

**New file:** `backend/internal/gitops/discovery_test.go`

### Step 9A.3 — Argo CD Adapter

**New file:** `backend/internal/gitops/argocd.go`

Functions accept an impersonating `dynamic.Interface` (from ClusterRouter):

- `ListArgoApplications(ctx, dynClient) ([]NormalizedApp, error)`:
  - List `applications` (GVR: `argoproj.io/v1alpha1/applications`) in all namespaces
  - Handle `spec.source` first. Add `spec.sources` (multi-source, Argo 2.6+) only if encountered in testing.
  - Map sync: `status.sync.status` → SyncStatus
  - Map health: `status.health.status` → HealthStatus (map "Missing" → HealthDegraded)
  - Extract message from `status.operationState.message` or `status.conditions[0].message`
  - Count managed resources from `status.resources[]`
  - Composite ID: `argo:<namespace>:<name>` (colon delimiter, no slashes)

- `GetArgoAppDetail(ctx, dynClient, namespace, name) (*NormalizedApp, []ManagedResource, []RevisionEntry, error)`:
  - Full `status.resources[]` → `[]ManagedResource` with per-resource sync+health
  - Map `status.history[]` → `[]RevisionEntry`

**New file:** `backend/internal/gitops/argocd_test.go` — test status normalization mapping

### Step 9A.4 — Flux CD Adapter

**New file:** `backend/internal/gitops/flux.go`

- `ListFluxKustomizations(ctx, dynClient) ([]NormalizedApp, error)`:
  - List `kustomizations` (GVR: `kustomize.toolkit.fluxcd.io/v1/kustomizations`) in all namespaces
  - Map conditions: `Ready=True` + revisions match → Synced, `Ready=False` → OutOfSync/Failed, `Reconciling=True` → Progressing, `Stalled=True` → Stalled
  - Health: `HealthCheckFailed` → Degraded, `spec.suspend` → Suspended
  - Source: resolve `spec.sourceRef` to get repo URL (inline, bounded concurrency semaphore(5) + 5s timeout)
  - Count managed resources from `status.inventory.entries[]`
  - Composite ID: `flux-ks:<namespace>:<name>`

- `ListFluxHelmReleases(ctx, dynClient) ([]NormalizedApp, error)`:
  - List `helmreleases` (GVR: `helm.toolkit.fluxcd.io/v2/helmreleases`) in all namespaces
  - Same condition mapping as Kustomizations
  - Source: `spec.chart.spec.chart` + `spec.chart.spec.version`
  - Composite ID: `flux-hr:<namespace>:<name>`

- `GetFluxAppDetail(ctx, dynClient, kind, namespace, name) (*NormalizedApp, []ManagedResource, []RevisionEntry, error)`:
  - Kustomization: inventory → `[]ManagedResource`, history → empty (no CRD-native history)
  - HelmRelease: `status.history[]` → `[]RevisionEntry`

**New file:** `backend/internal/gitops/flux_test.go` — test condition-to-status mapping

### Step 9A.5 — Handler with Caching + RBAC

**New file:** `backend/internal/gitops/handler.go`

```go
type Handler struct {
    Discoverer    *GitOpsDiscoverer
    ClusterRouter *k8s.ClusterRouter
    AccessChecker *resources.AccessChecker
    Logger        *slog.Logger

    fetchGroup singleflight.Group
    cacheMu    sync.RWMutex
    cachedData *cachedApps
}

type cachedApps struct {
    apps      []NormalizedApp
    fetchedAt time.Time
}
```

**Cache:** 30s TTL, local cluster only. Remote cluster requests bypass cache (direct fetch via ClusterRouter). Document this as intentional limitation matching policy handler.

**RBAC filtering** (use `CanAccessGroupResource` explicitly):
- Argo: `CanAccessGroupResource(ctx, user, groups, "list", "argoproj.io", "applications", namespace)`
- Flux Kustomization: `CanAccessGroupResource(ctx, user, groups, "list", "kustomize.toolkit.fluxcd.io", "kustomizations", namespace)`
- Flux HelmRelease: `CanAccessGroupResource(ctx, user, groups, "list", "helm.toolkit.fluxcd.io", "helmreleases", namespace)`

**Known limitation:** Argo CD Application objects are RBAC-checked against the namespace where the Application CRD lives (typically `argocd`), not the destination cluster/namespace. Users with access to the `argocd` namespace can see all Application objects regardless of destination. This mirrors Argo CD's own UI behavior.

**Handlers:**

1. `HandleStatus` — `GET /api/v1/gitops/status`
   - Returns `GitOpsStatus`
   - Non-admin: strip namespace/controller details

2. `HandleListApplications` — `GET /api/v1/gitops/applications?tool=X&namespace=X&syncStatus=X&healthStatus=X`
   - Fetch via cached path, merge Argo + Flux results
   - RBAC filter per namespace
   - Sort: out-of-sync first, then name
   - Include inline summary counts in response metadata: `{synced, outOfSync, degraded, progressing, suspended, total}`

3. `HandleGetApplication` — `GET /api/v1/gitops/applications/{id}`
   - Parse composite ID (colon-delimited: `tool:namespace:name`)
   - Return full detail: managed resources + revision history
   - History: Argo from CRD `status.history[]`, Flux HelmRelease from CRD `status.history[]`, Flux Kustomization → empty array

### Step 9A.6 — Wiring

**Route registration:**
```go
func (s *Server) registerGitOpsRoutes(ar chi.Router) {
    h := s.GitOpsHandler
    ar.Route("/gitops", func(gr chi.Router) {
        gr.Use(middleware.RateLimit(s.YAMLRateLimiter))
        gr.Get("/status", h.HandleStatus)
        gr.Get("/applications", h.HandleListApplications)
        gr.Get("/applications/{id}", h.HandleGetApplication)
    })
}
```

**Files to create (Phase 9A):**
- `backend/internal/gitops/types.go`
- `backend/internal/gitops/discovery.go`
- `backend/internal/gitops/discovery_test.go`
- `backend/internal/gitops/argocd.go`
- `backend/internal/gitops/argocd_test.go`
- `backend/internal/gitops/flux.go`
- `backend/internal/gitops/flux_test.go`
- `backend/internal/gitops/handler.go`

**Files to modify:**
- `backend/cmd/kubecenter/main.go` — instantiate discoverer + handler, launch discovery loop
- `backend/internal/server/server.go` — add `GitOpsHandler *gitops.Handler` to Server + Deps
- `backend/internal/server/routes.go` — add nil-guarded `registerGitOpsRoutes`

---

## Phase 9B: Frontend

**Branch:** `feat/phase9b-gitops-frontend`

### Step 9B.1 — Navigation + Types

**`frontend/lib/constants.ts`**

Add "GitOps" domain section (between Observability and Tools):

```ts
{
  id: "gitops",
  label: "GitOps",
  icon: "git-branch",
  href: "/gitops",
  tabs: [
    { label: "Applications", href: "/gitops/applications" },
  ],
}
```

Single tab. The applications list IS the GitOps dashboard. Add Sources/Compliance tabs later if users request them.

**New file:** `frontend/lib/gitops-types.ts` — TypeScript interfaces matching backend types.

### Step 9B.2 — GitOps Applications Page

**`frontend/islands/GitOpsApplications.tsx`** (~350 LOC)

Follow PolicyDashboard pattern:

- Fetch `/v1/gitops/status` + `/v1/gitops/applications` in parallel
- **Inline summary counts** at top: synced/out-of-sync/degraded/progressing/suspended (from response metadata), replacing the cut Compliance page
- Tool status banner: which tool(s) detected, or "No GitOps tool" with install guidance
- Application table: name, tool badge (Argo orange / Flux blue), sync status badge, health status badge, source (repo/chart), current revision (truncated SHA), last sync time, destination namespace
- Search/filter by name, tool, sync status, health status, namespace
- Click row → navigate to `/gitops/applications/:id` detail page
- Pagination (PAGE_SIZE=100) + Refresh button
- If no tool detected: show install links for Argo CD and Flux

**`frontend/routes/gitops/applications.tsx`** — route with SubNav

### Step 9B.3 — Application Detail Page

**`frontend/islands/GitOpsAppDetail.tsx`** (~300 LOC)

- Fetch `/v1/gitops/applications/:id`
- Header: app name, tool badge, sync+health status badges, source info
- **Managed Resources table**: kind, name, namespace, sync status, health (click → resource detail page via RESOURCE_DETAIL_PATHS)
- **Revision History**: revision (truncated SHA), status badge, message, deployed timestamp. Empty state for Flux Kustomizations: "Revision history not available for Flux Kustomizations"
- **Source panel**: repo URL (link), path, target revision, chart info if Helm
- Back link to applications list

**`frontend/routes/gitops/applications/[id].tsx`** — dynamic route with SubNav

### Step 9B.4 — Entry Points + Shared Components

- **Command palette**: "View GitOps Applications" quick action
- **GitOps index redirect**: `/gitops/` → `/gitops/applications`
- **Shared badges**: Add `ToolBadge`, `SyncStatusBadge`, `HealthStatusBadge` to a new `components/ui/GitOpsBadges.tsx` (follow PolicyBadges.tsx pattern)

**Files to create (Phase 9B):**
- `frontend/lib/gitops-types.ts`
- `frontend/components/ui/GitOpsBadges.tsx`
- `frontend/islands/GitOpsApplications.tsx`
- `frontend/islands/GitOpsAppDetail.tsx`
- `frontend/routes/gitops/index.tsx`
- `frontend/routes/gitops/applications.tsx`
- `frontend/routes/gitops/applications/[id].tsx`

**Files to modify:**
- `frontend/lib/constants.ts` (add GitOps section)
- `frontend/islands/CommandPalette.tsx` (add quick action)

---

## File Count Summary

| Phase | New Files | Modified Files |
|-------|-----------|---------------|
| 9A (Backend) | 8 | 3 |
| 9B (Frontend) | 7 | 2 |
| **Total** | **15** | **5** |

## New API Surface

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/gitops/status` | GET | Tool detection status |
| `/gitops/applications` | GET | List all normalized apps + summary counts |
| `/gitops/applications/{id}` | GET | App detail + managed resources + history |
| **Total** | **3** | |

## Status Mapping Reference

| Unified Sync | Argo CD | Flux CD |
|-------------|---------|---------|
| synced | `sync.status == "Synced"` | `Ready=True` + revisions match |
| outofsync | `sync.status == "OutOfSync"` | `Ready=False` + revision mismatch |
| progressing | `health.status == "Progressing"` | `Reconciling=True` |
| stalled | — | `Stalled=True` |
| failed | `operationState.phase == "Failed"` | `Ready=False` + not reconciling |

| Unified Health | Argo CD | Flux CD |
|---------------|---------|---------|
| healthy | `health.status == "Healthy"` | `Ready=True` + no HealthCheckFailed |
| degraded | `health.status == "Degraded"` or `"Missing"` | `HealthCheckFailed=True` |
| progressing | `health.status == "Progressing"` | `Reconciling=True` |
| suspended | `health.status == "Suspended"` | `spec.suspend == true` |

## Graceful Degradation

- **No tool:** All endpoints return 200 with empty data + `detected: ""`. Frontend shows install guidance.
- **Argo only / Flux only:** Data from detected tool only.
- **Both tools:** Data merged, tool badge indicates source.
- **Tool unhealthy:** Status shows available=false. Cached data from last successful fetch returned.
- **Remote clusters:** Discovery local-only. Reads go through ClusterRouter with impersonation. Cache is local-cluster-only; remote requests bypass cache.

## Key Security Decisions

1. **All reads via user impersonation** — ClusterRouter, never service account
2. **Namespace-level RBAC filtering** — uses `CanAccessGroupResource` with explicit API groups
3. **Status endpoint** — non-admin sees tool type only (no namespace/controller details)
4. **Bounded Flux source resolution** — semaphore(5), 5s timeout, consistent with Gatekeeper pattern
5. **Argo RBAC limitation** — Application objects checked against their own namespace (typically `argocd`), not destination namespace. Mirrors Argo CD UI behavior. Cross-cluster RBAC deferred.

## Multi-Cluster Considerations

- **Argo CD:** Natively multi-cluster. `destinationCluster` field surfaces the target. One Argo instance may manage apps across clusters.
- **Flux CD:** Per-cluster. ClusterRouter handles reading from remote clusters.
- **Cache:** Local cluster only. Remote requests are direct (no caching).

## Deferred Work

- Flux Sources page (dedicated browser for GitRepository, HelmRepository, etc.)
- Standalone Compliance dashboard with GaugeRing (if users request it)
- Flux Kustomization revision history (requires background recorder or DB tracking)
- Sync/rollback actions (POST endpoints to trigger Argo sync or Flux reconciliation)
- ApplicationSet support (Argo) — list generators and outputs
- Real-time sync status via WebSocket (watch GitOps CRDs)
- Git commit message display (requires Git provider API integration)
- Diff view (what changed between revisions)
- Cross-cluster destination RBAC for Argo CD
