# Phase 7: Advanced Observability — Implementation Plan (v2)

## Overview

Phase 7 adds end-to-end troubleshooting: Loki log integration, resource dependency graph, and automated diagnostics. Three sub-phases (7A–7C), each on its own branch.

**Spec:** `docs/superpowers/specs/2026-04-04-phase7-advanced-observability-design.md`

**Changes from v1 (reviewer feedback):**
- **Cut Phase 7D (Timeline)** — deferred; events/logs/alerts already have dedicated views
- **Cut Phase 7C.3 (Investigation Persistence)** — deferred; diagnostics are reproducible on demand
- **Cut `investigation/` and `timeline/` packages** — unnecessary decomposition
- **6 diagnostic rules** in V1 (not 14) — defer Prometheus-dependent and niche rules
- **Rules as functions in one file** (not 14 files behind an interface)
- **Extend ClusterTopology.tsx** instead of creating a new DependencyGraph island
- **Decompose LogExplorer** into composable sub-islands
- **Delete topology cache layer** — compute on demand from in-memory informer data
- **Strict tokenizer for LogQL security** — no Loki AST dependency, no regex
- **Simplified navigation** — add new tabs to existing section, don't move routes
- **Separate topology summary endpoint** (not `?summary=true` query param)
- **Topology builder accepts `ResourceLister` interface** (not concrete `*InformerManager`)
- **Dedicated log query rate limiter** (not shared write limiter)
- **Test files listed** for every new package

---

## Phase 7A: Loki Integration

**Branch:** `feat/phase7a-loki-integration`

### Step 7A.1 — Loki Discovery & Client

**New package: `backend/internal/loki/`**

**`backend/internal/loki/discovery.go`**

Follow `monitoring/discovery.go` pattern:

- `LokiDiscoverer` struct with `sync.RWMutex`, cached `*LokiStatus`, cached `*LokiClient`
- Constructor: `NewDiscoverer(k8sClient kubernetes.Interface, cfg config.LokiConfig, logger *slog.Logger)`
- `RunDiscoveryLoop(ctx)` — immediate discover, then 5-min ticker
- `Discover(ctx)` — probe for Loki service via label selectors (`app.kubernetes.io/name=loki`, fallback `app=loki`), well-known names in common namespaces, manual URL override from config
- Health check: `GET /ready` with 5s timeout
- Thread-safe accessors: `Client()`, `Status()`

**`backend/internal/loki/client.go`**

- `LokiClient` struct wrapping `*http.Client` + `baseURL string`
- Constructor with transport: 10 max idle conns, 90s idle timeout
- Methods (timeout per-request):
  - `QueryRange(ctx, query, start, end, limit, direction)` — 30s
  - `Labels(ctx, start, end)` — 10s
  - `LabelValues(ctx, name, start, end, query)` — 10s
  - `VolumeRange(ctx, query, start, end, step, targetLabels)` — 15s
  - `Ready(ctx)` — 5s
  - `TailURL(query, start, limit) string` — returns WebSocket URL
- Response types defined locally (no Loki dependency):
  ```go
  type QueryResponse struct {
      Status string    `json:"status"`
      Data   QueryData `json:"data"`
  }
  type QueryData struct {
      ResultType string   `json:"resultType"`
      Result     []Stream `json:"result"`
  }
  type Stream struct {
      Labels map[string]string `json:"stream"`
      Values [][]string        `json:"values"` // [nanosecond_ts, line]
  }
  ```
- Error handling: parse Loki error responses, surface error text to caller

**`backend/internal/loki/security.go`**

LogQL namespace enforcement — **critical security boundary**:

- `EnforceNamespaces(query string, allowedNamespaces []string) (string, error)`
- **Approach: strict stream selector tokenizer** (no Loki AST dependency, no regex)
  - Validate query starts with `{` (stream selector required)
  - Tokenize the `{...}` block into key-op-value triples using a simple state machine (the grammar is: `{key op "value", ...}` where op is `=`, `!=`, `=~`, `!~`)
  - Strip any existing `namespace` matcher
  - Inject enforced namespace: single ns → `namespace="X"`, multiple → `namespace=~"X|Y|Z"`
  - Reconstruct stream selector, preserve pipeline stages (everything after `}`)
  - If tokenization fails, reject query
- Admin users: pass through unmodified
- Query size limit: 4096 chars
- Cluster-scoped log queries (no namespace label, e.g., `{job="kubelet"}`) require admin role — reject for non-admin users

**Config additions (`backend/internal/config/config.go`):**

Add `Loki LokiConfig` to root Config:
```go
type LokiConfig struct {
    URL       string `koanf:"url"`       // KUBECENTER_LOKI_URL
    TenantID  string `koanf:"tenantid"`  // KUBECENTER_LOKI_TENANTID (X-Scope-OrgID)
}
```

**Files to create:**
- `backend/internal/loki/discovery.go`
- `backend/internal/loki/client.go`
- `backend/internal/loki/security.go`
- `backend/internal/loki/security_test.go` — **critical**: test namespace enforcement for injection attempts, multi-namespace regex, admin bypass, malformed queries, cluster-scoped rejection
- `backend/internal/loki/types.go`

**Files to modify:**
- `backend/internal/config/config.go` (add LokiConfig)

### Step 7A.2 — Loki HTTP Handlers

**`backend/internal/loki/handler.go`**

Follow `monitoring/handler.go` pattern:

- `Handler` struct: `Discoverer *LokiDiscoverer`, `Logger *slog.Logger`, `RBACChecker auth.RBACChecker`
- Each handler: check client nil → 503, enforce RBAC namespace scoping, proxy to Loki, re-wrap in k8sCenter response format
- Forward Loki error messages (400 bad LogQL, 429, 500) as `httputil.WriteError()` with Loki's error text as detail

**Handlers:**

1. `HandleStatus` — `GET /api/v1/logs/status` — Loki discovery status (no RBAC)
2. `HandleQuery` — `GET /api/v1/logs/query` — LogQL query with namespace enforcement
3. `HandleLabels` — `GET /api/v1/logs/labels` — label names
4. `HandleLabelValues` — `GET /api/v1/logs/labels/{name}/values` — label values for dropdowns
5. `HandleVolume` — `GET /api/v1/logs/volume` — uses Loki's `/loki/api/v1/index/volume_range` (label-based, not raw LogQL forwarding). Constructs label matcher from filter params.

**Route registration:**
```go
func (s *Server) registerLogRoutes(ar chi.Router) {
    ar.Route("/logs", func(lr chi.Router) {
        lr.Use(middleware.RateLimit(s.LogQueryLimiter)) // dedicated limiter, 30 req/min
        lr.Get("/status", s.LokiHandler.HandleStatus)
        lr.Get("/query", s.LokiHandler.HandleQuery)
        lr.Get("/labels", s.LokiHandler.HandleLabels)
        lr.Get("/labels/{name}/values", s.LokiHandler.HandleLabelValues)
        lr.Get("/volume", s.LokiHandler.HandleVolume)
    })
}
```

**Files to create:**
- `backend/internal/loki/handler.go`
- `backend/internal/loki/handler_test.go` — test 503 when Loki absent, RBAC enforcement, error forwarding

**Files to modify:**
- `backend/internal/server/server.go` (add `LokiHandler *loki.Handler` to Server + Deps, add `LogQueryLimiter`)
- `backend/internal/server/routes.go` (add registerLogRoutes, nil-guard)
- `backend/cmd/kubecenter/main.go` (create LokiDiscoverer, start goroutine, wire into Deps)

### Step 7A.3 — Loki WebSocket Tail

**`backend/internal/server/handle_ws_logs_search.go`**

Follow Pattern B (direct pipe), same as `handle_ws_flows.go`:

- Route: `WS /api/v1/ws/logs-search`
- Auth: in-band JWT via `wsAuthAndUpgrade(w, r)`
- Protocol: auth message → subscribe message `{"type":"subscribe","query":"...","start":"ns-ts"}` → server validates, enforces namespace RBAC, opens upstream Loki tail WS → bridges messages as `{"type":"log","streams":[...]}` and `{"type":"dropped","count":N}`
- Keepalive: 30s ping, 60s pong timeout
- Connection limit: `atomic.Int64`, max 50 concurrent (document as global, not per-user)
- On disconnect: close upstream Loki connection
- Reconnection: handled by frontend `lib/ws.ts` pattern (exponential backoff, max 15 attempts)

**Files to create:**
- `backend/internal/server/handle_ws_logs_search.go`

**Files to modify:**
- `backend/internal/server/routes.go` (add WS route in WebSocket section)

### Step 7A.4 — Log Explorer Frontend

**Decomposed into 4 sub-islands** (not one 800 LOC monolith):

**`frontend/islands/LogFilterBar.tsx`** (~150 LOC)
- Namespace/pod/container/severity dropdowns
- Populate from `apiGet("/v1/logs/labels/{name}/values")`
- Dual mode toggle (simple search vs raw LogQL)
- Time range selector (presets: 15m, 1h, 6h, 24h, 7d)
- Run button + Live Tail button
- Emits filter state via callback props to parent

**`frontend/islands/LogResults.tsx`** (~200 LOC)
- Monospace log line table: timestamp, severity (color-coded), pod name (link), log text
- Error lines: subtle red background via `var(--status-error-dim)`
- Click pod name → navigate to pod detail
- Click "Investigate" → navigate to `/observability/investigate?namespace=X&kind=Pod&name=Y`

**`frontend/islands/LogLiveTail.tsx`** (~200 LOC)
- WebSocket connection to `/ws/logs-search` using existing `lib/ws.ts` patterns
- Auto-scroll with pause-on-scroll-up (resume button when paused, same as LogViewer)
- Buffer: 5000 lines max, drop oldest
- Connection status indicator ("Connected", "Reconnecting...", "Disconnected")

**`frontend/islands/LogVolumeHistogram.tsx`** (~100 LOC)
- Bar chart using CSS flex (not SVG)
- Error-rate bars colored with `var(--status-error-dim)`
- Click bar → update time range to that bucket's window

**`frontend/islands/LogExplorer.tsx`** (~150 LOC orchestrator)
- Composes the 4 sub-islands above
- Manages shared state: passes filter state to query execution, switches between search results and live tail mode
- Checks Loki status on mount, shows "Loki not detected" banner with setup instructions if absent

**New route: `frontend/routes/observability/logs.tsx`** — thin page wrapper

**Files to create:**
- `frontend/islands/LogFilterBar.tsx`
- `frontend/islands/LogResults.tsx`
- `frontend/islands/LogLiveTail.tsx`
- `frontend/islands/LogVolumeHistogram.tsx`
- `frontend/islands/LogExplorer.tsx`
- `frontend/routes/observability/logs.tsx`

**Files to modify:**
- `frontend/routes/ws/[...path].ts` (add logs-search to WS proxy allowlist)
- `frontend/lib/constants.ts` (add Log Explorer tab — done in 7A.5)

### Step 7A.5 — Contextual Logs Tab + Navigation Update

**Logs tab on resource detail pages:**

Add a "Logs" tab to `ResourceDetail.tsx` (or individual overview islands). When Loki is detected, renders `LogExplorer` pre-filtered to the resource's namespace/pod/labels. Loki status checked via `apiGet("/v1/logs/status")` in the parent island — tab hidden if not detected.

**Navigation update** (`frontend/lib/constants.ts`):

Update the existing `observability` entry in `DOMAIN_SECTIONS` to add new tabs. Do NOT move existing `/monitoring/*` or `/alerting/*` routes — existing nav tabs can cross-reference paths from other sections:

```ts
{
  id: "observability",
  label: "Observability",
  icon: "activity",
  href: "/observability",
  tabs: [
    { label: "Overview", href: "/monitoring" },          // existing
    { label: "Log Explorer", href: "/observability/logs" }, // new
    { label: "Topology", href: "/observability/topology" }, // new (7B)
    { label: "Investigate", href: "/observability/investigate" }, // new (7C)
    { label: "Dashboards", href: "/monitoring/dashboards" }, // existing
    { label: "Alerts", href: "/alerting" },               // existing
    { label: "Prometheus", href: "/monitoring/prometheus" }, // existing
  ],
}
```

Remove the separate alerting section from DOMAIN_SECTIONS (if separate). This consolidates the icon rail without moving any existing routes or breaking bookmarks.

**Files to modify:**
- `frontend/islands/ResourceDetail.tsx` (add Logs tab conditionally)
- `frontend/lib/constants.ts` (update DOMAIN_SECTIONS)

---

## Phase 7B: Resource Dependency Graph

**Branch:** `feat/phase7b-dependency-graph`

### Step 7B.1 — Topology Graph Builder (Backend)

**New package: `backend/internal/topology/`**

**`backend/internal/topology/types.go`**

Shared types (imported by both `topology` and `diagnostics`):
```go
type Graph struct {
    Nodes      []Node `json:"nodes"`
    Edges      []Edge `json:"edges"`
    ComputedAt string `json:"computedAt"` // ISO8601
}
type Node struct {
    ID        string `json:"id"`      // uid
    Kind      string `json:"kind"`
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
    Health    Health `json:"health"`
    Summary   string `json:"summary"` // e.g., "3/3 ready"
}
type Edge struct {
    Source string   `json:"source"`
    Target string   `json:"target"`
    Type   EdgeType `json:"type"`
}
// Health, EdgeType enums as before
```

**`backend/internal/topology/builder.go`**

- `ResourceLister` interface (for testability + future remote cluster support):
  ```go
  type ResourceLister interface {
      ListPods(ctx context.Context, namespace string) ([]corev1.Pod, error)
      ListServices(ctx context.Context, namespace string) ([]corev1.Service, error)
      ListDeployments(ctx context.Context, namespace string) ([]appsv1.Deployment, error)
      // ... one method per resource kind needed for graph building
  }
  ```
- `InformerLister` struct implements `ResourceLister` by wrapping `*k8s.InformerManager` listers. Adapter pattern — keeps topology decoupled from informer internals.
- `Builder` struct: `lister ResourceLister`, `logger *slog.Logger`
- `BuildNamespaceGraph(ctx, namespace, userNamespaces []string) (*Graph, error)`:
  1. Collect resources from lister (pods, services, deployments, replicasets, statefulsets, daemonsets, jobs, cronjobs, ingresses, configmaps, secrets, PVCs, HPAs)
  2. Build nodes (UID as ID)
  3. Owner reference edges (single pass)
  4. Service→Pod selector edges
  5. Ingress→Service edges (including multi-path and defaultBackend)
  6. Pod→ConfigMap/Secret/PVC mount edges
  7. HPA→target edges
  8. RBAC filtering: omit nodes for kinds user can't list
  9. Health computation inline: pods (phase + container statuses), deployments (ready vs desired), etc.
  10. Health propagation: reverse walk through owner/selector edges
- `BuildFocusedGraph(ctx, namespace, kind, name, userNamespaces)` — build full graph then BFS with `visited` set to extract target's tree only

**No `cache.go`** — informer data is already in-memory. Graph build is fast. If performance becomes an issue later, add simple TTL caching with evidence.

**No separate `health.go`** — health computation and propagation are ~50 lines each, inline in builder.go.

**Files to create:**
- `backend/internal/topology/types.go`
- `backend/internal/topology/builder.go` (includes ResourceLister interface, InformerLister adapter, health logic)
- `backend/internal/topology/handler.go`
- `backend/internal/topology/builder_test.go` — test graph construction with fake ResourceLister: owner ref chains, selector matching, health propagation, BFS cycle safety, RBAC filtering

**Files to modify:**
- `backend/internal/server/server.go` (add TopologyHandler)
- `backend/internal/server/routes.go` (add registerTopologyRoutes)
- `backend/cmd/kubecenter/main.go` (wire topology)

### Step 7B.2 — Topology HTTP Handlers

**`backend/internal/topology/handler.go`**

- `Handler` struct: `Builder *Builder`, `Logger *slog.Logger`, `RBACChecker auth.RBACChecker`

**Handlers:**

1. `HandleNamespaceGraph` — `GET /api/v1/topology/{namespace}` — full dependency graph
2. `HandleFocusedGraph` — `GET /api/v1/topology/{namespace}/{kind}/{name}` — single resource tree
3. `HandleHealthSummary` — `GET /api/v1/topology/{namespace}/summary` — separate endpoint (not query param), returns `{healthy: N, degraded: N, failing: N, total: N}`

Namespace URL param validated via `resources.ValidateURLParams` middleware (same as resource routes).

**Route registration:**
```go
func (s *Server) registerTopologyRoutes(ar chi.Router) {
    ar.Route("/topology", func(tr chi.Router) {
        tr.Get("/{namespace}", s.TopologyHandler.HandleNamespaceGraph)
        tr.Get("/{namespace}/summary", s.TopologyHandler.HandleHealthSummary)
        tr.Get("/{namespace}/{kind}/{name}", s.TopologyHandler.HandleFocusedGraph)
    })
}
```

### Step 7B.3 — Extend ClusterTopology.tsx for Namespace Scope

**Do NOT create a new DependencyGraph.tsx.** Extend the existing `ClusterTopology.tsx` (1405 LOC) with a `scope` prop.

**Modifications to `frontend/islands/ClusterTopology.tsx`:**

- Add `scope` prop: `"cluster"` (existing behavior) or `"namespace"` (new)
- In namespace scope: fetch from `apiGet("/v1/topology/{namespace}")` instead of building client-side
- Use dagre for layout (add `dagre` dependency): when scope is namespace, use dagre's Sugiyama layout for proper LR DAG rendering. Cluster scope retains existing layout.
- Add slide-out panel: on node click, show resource summary + conditions + recent events (fetched via existing resource GET endpoint) + quick actions ("View Detail", "View Logs", "Investigate")
- Add hover highlight: dim non-connected nodes, highlight connected edges
- Health coloring: node border color from `var(--status-success/warning/error)`. Pulsing animation on failing nodes.
- Focus mode reflected in URL: `?kind=Deployment&name=my-app` → calls focused graph endpoint. Browser refresh preserves focus.
- RBAC-filtered nodes that create dangling edges: hide the edge. Show banner "Some resources hidden due to permissions" if any nodes were filtered.

**New route: `frontend/routes/observability/topology.tsx`** — renders `<ClusterTopology scope="namespace" />`

**Files to create:**
- `frontend/routes/observability/topology.tsx`

**Files to modify:**
- `frontend/islands/ClusterTopology.tsx` (add namespace scope, dagre layout, slide-out, hover, focus mode)
- `frontend/deno.json` (add `dagre` dependency)

---

## Phase 7C: Diagnostic Workspace

**Branch:** `feat/phase7c-diagnostics`

**Depends on 7B** (blast radius uses topology graph).

### Step 7C.1 — Diagnostic Engine (Backend)

**New package: `backend/internal/diagnostics/`**

**`backend/internal/diagnostics/diagnostics.go`** — types, runner, resolver (~300 LOC)

Types:
```go
type Severity string // "critical", "warning", "info"
type Result struct {
    RuleName    string   `json:"ruleName"`
    Status      string   `json:"status"` // "pass", "warn", "fail"
    Severity    Severity `json:"severity"`
    Message     string   `json:"message"`
    Detail      string   `json:"detail,omitempty"`
    Remediation string   `json:"remediation,omitempty"`
    Links       []Link   `json:"links,omitempty"`
}
type CheckFunc func(ctx context.Context, target *DiagnosticTarget) Result
type DiagnosticTarget struct {
    Kind, Name, Namespace string
    Object     runtime.Object
    Pods       []corev1.Pod
    Events     []corev1.Event
    Conditions []metav1.Condition
}
```

Runner:
- `type ruleEntry struct { name string; severity Severity; appliesTo []string; check CheckFunc }`
- `var rules = []ruleEntry{ ... }` — static slice of 6 rules (plain functions, not interface implementations)
- `Run(ctx, target) []Result` — filter by `appliesTo`, run in parallel (pool of 5, per-rule timeout 5s, recover panics), sort by severity

Resolver:
- `Resolve(ctx, lister topology.ResourceLister, namespace, kind, name) (*DiagnosticTarget, error)`
- Fetches target + related pods + events in parallel

**`backend/internal/diagnostics/rules.go`** — all 6 rules in one file (~400 LOC)

Ship these 6 core rules (cover 90% of debugging scenarios):

1. **CrashLoopBackOff** — pod container status `waiting.reason == "CrashLoopBackOff"`. Report restart count, exit code, termination message.
2. **ImagePullBackOff** — `waiting.reason` is `ImagePullBackOff` or `ErrImagePull`. Report image name.
3. **Pending Pod** — pod phase is Pending. Parse scheduler events for reason (insufficient resources, affinity, taints).
4. **Replica Mismatch** — `spec.replicas != status.readyReplicas`. Report delta.
5. **Zero Endpoints** — Service with selector but 0 matching ready pods. Report selector.
6. **Pending PVC** — PVC phase != Bound. Report StorageClass and size.

**Deferred rules** (add as single-file PRs later — zero structural changes needed):
- OOMKilled, Probe Failures, Resource Pressure (need Prometheus), Node Pressure, Stale RS, Network Policy, ConfigMap/Secret, HPA

**`backend/internal/diagnostics/blast.go`** (~100 LOC)

- `BlastRadius(graph *topology.Graph, targetID string) *BlastResult`
- BFS with `visited` set (prevents cycles). Downstream = "directly affected", upstream = "potentially affected"
- Returns `BlastResult` with two slices of `AffectedResource{Kind, Name, Health, Impact}`

**`backend/internal/diagnostics/handler.go`** (~150 LOC)

1. `HandleDiagnostics` — `GET /api/v1/diagnostics/{namespace}/{kind}/{name}`
   - Resolve target, run rules, build topology for blast radius, return combined response
2. `HandleNamespaceSummary` — `GET /api/v1/diagnostics/{namespace}/summary`
   - Quick-scan: list pods/deployments from informer, check status fields, return failing/degraded counts

**Files to create:**
- `backend/internal/diagnostics/diagnostics.go` (types + runner + resolver)
- `backend/internal/diagnostics/rules.go` (6 check functions)
- `backend/internal/diagnostics/blast.go`
- `backend/internal/diagnostics/handler.go`
- `backend/internal/diagnostics/rules_test.go` — test each rule with synthetic DiagnosticTarget
- `backend/internal/diagnostics/blast_test.go` — test BFS traversal, cycle safety

**Files to modify:**
- `backend/internal/server/server.go` (add DiagnosticsHandler)
- `backend/internal/server/routes.go` (add registerDiagnosticsRoutes)
- `backend/cmd/kubecenter/main.go` (wire diagnostics)

### Step 7C.2 — Diagnostic Workspace Frontend

**Decomposed into sub-islands:**

**`frontend/islands/DiagnosticChecklist.tsx`** (~200 LOC)
- Renders diagnostic results: failed checks expanded (card with severity badge, message, detail, remediation, action links). Passed checks collapsed by default (green checkmark + name).
- Action links: "View Logs" → Log Explorer, "Pod Detail" → detail page

**`frontend/islands/BlastRadiusPanel.tsx`** (~150 LOC)
- Two tiers: "Directly Affected" (red border) and "Potentially Affected" (amber border)
- Each resource: clickable row → navigate to detail page
- Shows kind, name, health badge, impact description

**`frontend/islands/DiagnosticWorkspace.tsx`** (~200 LOC orchestrator)
- URL-driven: reads `?namespace=X&kind=Y&name=Z`. If params present, auto-run on mount. If not, show resource picker.
- Composes DiagnosticChecklist + BlastRadiusPanel + recent events table
- Status banner: "{N} Critical / {M} Warning Issues Detected"
- "Re-scan" button re-fetches diagnostics
- Layout: two-column using existing SplitPane (checklist left, blast radius right)

**New route: `frontend/routes/observability/investigate.tsx`**

**Files to create:**
- `frontend/islands/DiagnosticChecklist.tsx`
- `frontend/islands/BlastRadiusPanel.tsx`
- `frontend/islands/DiagnosticWorkspace.tsx`
- `frontend/routes/observability/investigate.tsx`

### Step 7C.3 — Integrate "Investigate" Entry Points

- **Resource detail pages:** Add "Investigate" button in header when resource is unhealthy. Trigger condition: pod restartCount > 3, any condition Status=False, desired != available replicas, or pod phase is Failed/Pending. Links to `/observability/investigate?namespace=X&kind=Y&name=Z`.
- **ClusterTopology slide-out:** "Investigate" quick action (from 7B.3).
- **Command palette:** Add "Investigate" action to `CommandPalette.tsx`. Links to `/observability/investigate` (user types resource name there — no live resource fetching in palette).
- **Dashboard health score:** Link to `/observability/investigate` with no pre-scope (user picks resource). Dashboard summary doesn't return "most degraded resource" — keep it simple.

**Files to modify:**
- `frontend/islands/ResourceDetail.tsx` (add Investigate button)
- `frontend/islands/CommandPalette.tsx` (add Investigate action)
- `frontend/islands/DashboardV2.tsx` (add Investigate link from health score)

### Step 7C.4 — E2E Tests + Verification

**New test suite: `e2e/observability.spec.ts`**

- Navigate to `/observability/logs` — verify renders, shows "Loki not detected" in CI (no Loki in kind)
- Navigate to `/observability/topology` — verify graph renders with resources
- Navigate to `/observability/investigate` — verify workspace renders, can pick a resource
- Existing `/monitoring` and `/alerting` routes still work (no redirects, no breakage)
- Sub-nav tabs updated under Observability section

**Final verification:**
- `make lint` (go vet + deno lint)
- `make test` (unit tests including new test files)
- `make test-e2e`

**Files to create:**
- `e2e/observability.spec.ts`

---

## Dependency Graph

```
Phase 7A (Loki) ────────┐
                         ├── merge to main, then
Phase 7B (Topology) ─────┤
                         ├── Phase 7C (Diagnostics, depends on 7B for blast radius)
                         │
                         └── merge 7A first, then 7B, then 7C
                             (7C.3 modifies ResourceDetail.tsx which 7A.5 also touches)
```

7A and 7B are independent — can develop in parallel but merge 7A first to avoid conflicts on ResourceDetail.tsx.

## File Count Summary

| Phase | New Files | Modified Files |
|-------|-----------|---------------|
| 7A (Loki) | 10 | 7 |
| 7B (Topology) | 5 | 5 |
| 7C (Diagnostics) | 8 | 5 |
| **Total** | **23** | **17** |

Down from 51 new / 32 modified in v1 — 55% reduction.

## New API Surface

| Group | Endpoints | Type |
|-------|-----------|------|
| `/api/v1/logs/*` | 5 | HTTP |
| `/ws/logs-search` | 1 | WebSocket |
| `/api/v1/topology/*` | 3 | HTTP |
| `/api/v1/diagnostics/*` | 2 | HTTP |
| **Total** | **11** | |

Down from 16 in v1 (cut investigation CRUD + timeline).

## Deferred Work (Future PRs)

These can each be added as standalone PRs with zero structural changes:

- **8 additional diagnostic rules** (OOMKilled, Probes, Resources, NodePressure, StaleRS, NetworkPolicy, ConfigMap/Secret, HPA)
- **Investigation persistence** (PostgreSQL table + CRUD) — when there's evidence users want to save/replay diagnostics
- **Unified Timeline** — when the core observability tools are battle-tested
- **Route migration** (moving `/monitoring/*` and `/alerting/*` to `/observability/*`) — deferred to avoid bookmark breakage
- **DependencyGraph progressive disclosure** (collapse/expand nodes) — if graphs become too noisy
- **DependencyGraph minimap** — luxury feature, add if needed
- **Per-user WS tail connection limits** — document global limit as known limitation for now
- **Log query history** — localStorage keyed by user ID, 50-query limit (no server-side storage)
