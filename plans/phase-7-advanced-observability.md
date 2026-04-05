# Phase 7: Advanced Observability — Implementation Plan

## Overview

Phase 7 adds end-to-end troubleshooting capabilities: Loki log integration, resource dependency graphs, automated diagnostics, unified timeline, and navigation consolidation. Work is decomposed into five phases (7A–7E), each on its own branch.

**Spec:** `docs/superpowers/specs/2026-04-04-phase7-advanced-observability-design.md`

**Key research findings that shape implementation:**
- Monitoring package (`internal/monitoring/`) is the direct template for Loki integration: `Discoverer` + `Client` + `Handler` pattern
- WebSocket Pattern B (direct pipe, same as flows/logs) for `/ws/logs-search`
- `local_users.id` is `TEXT` (not UUID) — migration FK must match
- `DOMAIN_SECTIONS` in `constants.ts` already has an `"observability"` section at line 542 — update in place
- No official Loki Go client SDK — raw HTTP with custom client
- Dagre library for DAG layout + raw SVG rendering (matches existing ClusterTopology pattern)
- LogQL security: AST-level namespace injection via `github.com/grafana/loki/v3/pkg/logql/syntax`

---

## Phase 7A: Loki Integration (Backend + Frontend)

**Branch:** `feat/phase7a-loki-integration`

### Step 7A.1 — Loki Discovery & Client

**New package: `backend/internal/loki/`**

**`backend/internal/loki/discovery.go`**

Follow `monitoring/discovery.go` pattern exactly:

- `LokiDiscoverer` struct with `sync.RWMutex`, cached `*LokiStatus`, cached `*LokiClient`
- Constructor: `NewDiscoverer(k8sClient kubernetes.Interface, cfg config.LokiConfig, logger *slog.Logger)`
- `RunDiscoveryLoop(ctx)` — immediate discover, then 5-min ticker (same as monitoring)
- `Discover(ctx)` probes for Loki service:
  1. Label selector `app.kubernetes.io/name=loki` across all namespaces
  2. Fallback: label selector `app=loki` (older loki-stack chart)
  3. Fallback: well-known service names (`loki-gateway`, `loki`, `loki-read`) in `monitoring`, `loki`, `observability` namespaces
  4. Manual override: `cfg.URL` takes precedence over auto-discovery
  - Prefer gateway component (port 80) > read component (port 3100) > single-binary (port 3100)
- Health check: `GET /ready` — expects `ready` text response with 5s timeout
- `Status()` returns `LokiStatus{Detected bool, URL string, Version string, DetectedVia string}`
- Thread-safe accessors: `Client() *LokiClient`, `Status() LokiStatus`

**`backend/internal/loki/client.go`**

- `LokiClient` struct wrapping `*http.Client` + `baseURL string`
- Constructor: `NewClient(baseURL string)` with:
  - `http.Transport`: 10 max idle conns, 90s idle timeout
  - No default timeout on client (set per-request)
- Methods:
  - `Query(ctx, query string, time time.Time, limit int) (*QueryResponse, error)` — 15s timeout
  - `QueryRange(ctx, query string, start, end time.Time, limit int, direction string) (*QueryResponse, error)` — 30s timeout
  - `Labels(ctx, start, end time.Time) ([]string, error)` — 10s timeout
  - `LabelValues(ctx, name string, start, end time.Time, query string) ([]string, error)` — 10s timeout
  - `VolumeRange(ctx, query string, start, end time.Time, step string, targetLabels []string) (*VolumeResponse, error)` — 15s timeout
  - `Ready(ctx) error` — 5s timeout, checks `/ready`
  - `TailURL(query string, start int64, limit int) string` — returns WebSocket URL for tail endpoint
- All methods set `X-Scope-OrgID` header if configured (multi-tenant Loki)
- Response types (define locally to avoid heavy loki dependency):
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
  type VolumeResponse struct {
      Status string     `json:"status"`
      Data   VolumeData `json:"data"`
  }
  type VolumeData struct {
      Result []VolumeEntry `json:"result"`
  }
  type VolumeEntry struct {
      Metric map[string]string `json:"metric"`
      Values [][]any           `json:"values"` // [timestamp, "count"]
  }
  ```
- Error handling: parse Loki error responses (`{"status":"error","errorType":"...","error":"..."}`)

**`backend/internal/loki/security.go`**

LogQL namespace enforcement — **critical security boundary**:

- `EnforceNamespaces(query string, allowedNamespaces []string) (string, error)`
- Parse query with `github.com/grafana/loki/v3/pkg/logql/syntax.ParseExpr()`
- Walk AST, for each stream selector:
  - Remove any existing `namespace` matcher
  - If single allowed namespace: inject `{namespace="ns"}`
  - If multiple: inject `{namespace=~"ns1|ns2|ns3"}`
  - If user is admin (all namespaces): pass through unmodified
- If parse fails, reject query (don't attempt regex fallback)
- Query size limit: 4096 chars (reject before parsing)
- **Evaluate dependency cost**: `github.com/grafana/loki/v3/pkg/logql/syntax` pulls in a large tree. If too heavy, implement a simpler approach:
  - Parse stream selector `{...}` with regex
  - Validate it contains only allowed labels
  - Prepend/replace namespace matcher
  - This is less robust but avoids the dependency. Document the tradeoff in code comments.
- **Decision**: Try the AST approach first. If `go mod tidy` adds >50MB of dependencies, fall back to regex.

**Config additions (`backend/internal/config/config.go`):**

```go
type LokiConfig struct {
    URL       string `koanf:"url"`       // Manual override: KUBECENTER_LOKI_URL
    OrgID     string `koanf:"orgid"`     // Multi-tenant: KUBECENTER_LOKI_ORGID
    Namespace string `koanf:"namespace"` // Hint namespace: KUBECENTER_LOKI_NAMESPACE
}
```

Add `Loki LokiConfig `koanf:"loki"`` to the root `Config` struct.

**Files to create:**
- `backend/internal/loki/discovery.go`
- `backend/internal/loki/client.go`
- `backend/internal/loki/security.go`
- `backend/internal/loki/types.go` (response types)

**Files to modify:**
- `backend/internal/config/config.go` (add LokiConfig)
- `backend/go.mod` (add `github.com/grafana/loki/v3` if AST approach works; evaluate dependency weight)

### Step 7A.2 — Loki HTTP Handlers

**`backend/internal/loki/handler.go`**

Follow `monitoring/handler.go` pattern:

- `Handler` struct: `Discoverer *LokiDiscoverer`, `Logger *slog.Logger`, `RBACChecker auth.RBACChecker`
- Each handler checks `h.Discoverer.Client() == nil` → return 503 with `{"error":{"code":503,"message":"Loki not detected","detail":"..."}}`
- RBAC enforcement: extract user from context, get allowed namespaces, call `EnforceNamespaces()` on every query

**Handlers:**

1. `HandleStatus(w, r)` — `GET /api/v1/logs/status`
   - Returns `LokiStatus` (detected, URL, version)
   - No RBAC needed (info-only)

2. `HandleQuery(w, r)` — `GET /api/v1/logs/query`
   - Query params: `query` (required), `start`, `end` (RFC3339, default last 1h), `limit` (default 100, max 5000), `direction` (default `backward`)
   - Enforce namespace scoping on query
   - Proxy to `client.QueryRange()`
   - Re-wrap response in k8sCenter format: `httputil.WriteData(w, result)`

3. `HandleLabels(w, r)` — `GET /api/v1/logs/labels`
   - Query params: `start`, `end` (optional)
   - Proxy to `client.Labels()`
   - Filter out internal labels if desired

4. `HandleLabelValues(w, r)` — `GET /api/v1/logs/labels/{name}/values`
   - URL param: `name` (label name)
   - Query params: `start`, `end`, `query` (optional label matcher for scoping)
   - Enforce namespace in the optional query param
   - Proxy to `client.LabelValues()`

5. `HandleVolume(w, r)` — `GET /api/v1/logs/volume`
   - Query params: `query` (required), `start`, `end`, `step` (default `1m`)
   - Enforce namespace scoping
   - Proxy to `client.VolumeRange()`

**Route registration (`backend/internal/server/routes.go`):**

Add `registerLogRoutes(ar chi.Router)` method:
```go
func (s *Server) registerLogRoutes(ar chi.Router) {
    ar.Route("/logs", func(lr chi.Router) {
        lr.Use(middleware.RateLimit(s.WriteLimiter)) // 30 req/min
        lr.Get("/status", s.LokiHandler.HandleStatus)
        lr.Get("/query", s.LokiHandler.HandleQuery)
        lr.Get("/labels", s.LokiHandler.HandleLabels)
        lr.Get("/labels/{name}/values", s.LokiHandler.HandleLabelValues)
        lr.Get("/volume", s.LokiHandler.HandleVolume)
    })
}
```

Add nil-guard: `if s.LokiHandler != nil { s.registerLogRoutes(ar) }` in the authenticated group.

**Files to create:**
- `backend/internal/loki/handler.go`

**Files to modify:**
- `backend/internal/server/server.go` (add `LokiHandler *loki.Handler` to Server + Deps)
- `backend/internal/server/routes.go` (add registerLogRoutes, nil-guard)
- `backend/cmd/kubecenter/main.go` (create LokiDiscoverer, start goroutine, wire into Deps)

### Step 7A.3 — Loki WebSocket Tail

**`backend/internal/server/handle_ws_logs_search.go`**

Follow Pattern B (direct pipe), same as `handle_ws_flows.go`:

- Route: `WS /api/v1/ws/logs-search` (registered in WebSocket section of routes.go)
- Auth: in-band JWT via `wsAuthAndUpgrade(w, r)` helper
- First message after auth: `{"type":"subscribe","query":"{namespace=\"default\"}","start":"nanosecond-ts"}`
- RBAC: extract namespaces from query, check user has `get` on `pods/log` for each namespace
- Enforce namespace scoping on the LogQL query
- Open upstream WebSocket to Loki: `loki.TailURL(enforcedQuery, start, limit)`
  - Use `gorilla/websocket.Dialer` with 10s handshake timeout
- Bridge: goroutine reads from Loki WS, writes to client WS
  - Transform Loki message format to k8sCenter format: `{"type":"log","streams":[...]}`
  - Forward `dropped_entries` as `{"type":"dropped","count":N}`
  - Rate limit: if Loki sends >100 messages/sec, aggregate into 100ms batches
- Keepalive: `wsStartKeepalive(conn, cancel)` — 30s ping, 60s pong timeout
- Connection limit: `atomic.Int64` counter, max 50 concurrent tail streams
- On client disconnect: close upstream Loki connection
- On Loki disconnect: send `{"type":"error","message":"Log stream disconnected"}`, close client WS

**Register in routes.go** (WebSocket section, around line 22):
```go
if s.LokiHandler != nil {
    r.Get("/api/v1/ws/logs-search", s.handleWSLogsSearch)
}
```

**Files to create:**
- `backend/internal/server/handle_ws_logs_search.go`

**Files to modify:**
- `backend/internal/server/routes.go` (add WS route in WebSocket section)

### Step 7A.4 — Log Explorer Frontend

**New island: `frontend/islands/LogExplorer.tsx`**

Large island (~600-800 LOC). Structure:

- **State signals:**
  - `lokiStatus: useSignal<LokiStatus | null>(null)` — Loki availability
  - `query: useSignal("")` — LogQL or search text
  - `mode: useSignal<"search" | "logql">("search")` — toggle between simple and advanced
  - `namespace: useSignal("")` — filter
  - `pod: useSignal("")` — filter
  - `container: useSignal("")` — filter
  - `severity: useSignal("")` — filter
  - `timeRange: useSignal({start, end, preset: "1h"})` — time range
  - `results: useSignal<LogResult[] | null>(null)` — query results
  - `volume: useSignal<VolumeData | null>(null)` — histogram data
  - `loading: useSignal(false)`
  - `error: useSignal<string | null>(null)`
  - `isLiveTail: useSignal(false)` — live tail mode active
  - `liveTailLines: useSignal<LogLine[]>([])` — live tail buffer

- **Filter bar:** namespace/pod/container/severity dropdowns. Populate from `apiGet("/v1/logs/labels/namespace/values")`, etc. Namespace dropdown respects RBAC (only shows permitted).

- **Search input:** Text field. In "search" mode, auto-generates LogQL: `{namespace="X",pod=~"Y"} |= "user text"`. In "logql" mode, passes raw input. Toggle button between modes.

- **Time range selector:** Presets (15m, 1h, 6h, 24h, 7d) + custom range picker. Updates `start`/`end` query params.

- **Run button:** Calls `apiGet("/v1/logs/query?query=...&start=...&end=...&limit=1000")`. Parse response, display in log table.

- **Live Tail button:** Opens WebSocket to `/ws/logs-search`. Sends subscribe message with current filters as LogQL. Incoming lines appended to `liveTailLines` signal. Buffer capped at 5000 lines (drop oldest). Auto-scroll with pause-on-scroll-up (same pattern as LogViewer). Stop button closes WS.

- **Volume histogram:** `LogVolumeHistogram` sub-component. Calls `apiGet("/v1/logs/volume?query=...&start=...&end=...&step=...")`. Renders bar chart using `<div>` bars (not SVG — simple enough). Click a bar to zoom time range.

- **Log lines display:** Monospace `<pre>` with columns: timestamp, severity (color-coded), pod name (link to pod detail), log text. Error lines get subtle red background. Click timestamp → zoom timeline. Click pod → navigate to `/workloads/pods/{ns}/{name}`.

- **Context links:** "Investigate" button on each log line's pod → navigates to `/observability/investigate?namespace={ns}&kind=Pod&name={pod}`.

**New component: `frontend/islands/LogVolumeHistogram.tsx`**

Lightweight island (~150 LOC):
- Takes `volume` signal as prop
- Renders horizontal bar chart using CSS flex
- Error-rate bars colored with `var(--status-error-dim)`
- Click handler emits time range selection

**New route: `frontend/routes/observability/logs.tsx`**

```tsx
import { define } from "@/utils.ts";
import LogExplorer from "@/islands/LogExplorer.tsx";

export default define.page(function LogsPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">Log Explorer</h1>
        <p class="mt-1 text-sm text-text-secondary">Search and stream logs from Loki</p>
      </div>
      <LogExplorer />
    </div>
  );
});
```

**Frontend WebSocket proxy:** Add `/ws/logs-search` to the WebSocket proxy allowlist in `frontend/routes/ws/[...path].ts`.

**Files to create:**
- `frontend/islands/LogExplorer.tsx`
- `frontend/islands/LogVolumeHistogram.tsx`
- `frontend/routes/observability/logs.tsx`

**Files to modify:**
- `frontend/routes/ws/[...path].ts` (add logs-search to WS proxy)
- `frontend/lib/constants.ts` (will be updated in 7E — navigation step)

### Step 7A.5 — Contextual Logs Tab on Resource Detail Pages

Add a "Logs" tab to existing resource detail pages when Loki is detected.

**Approach:** Add a `LogsTab` component that can be embedded in any resource detail page. It renders a scoped LogExplorer pre-filtered to the resource.

**`frontend/components/k8s/detail/LogsTab.tsx`** (SSR component, wraps island)

- Props: `namespace: string`, `kind: string`, `name: string`
- Renders `<LogExplorer namespace={namespace} pod={name} />` (for pods) or `<LogExplorer namespace={namespace} labelSelector={...} />` (for deployments/services)
- Only renders when Loki is available (check via `apiGet("/v1/logs/status")` in the parent island)

**Modify existing detail overview islands** to include the LogsTab:
- `PodOverview` — pre-filter to pod name
- `DeploymentOverview` — pre-filter to `app` label matching deployment name
- `StatefulSetOverview`, `DaemonSetOverview`, `JobOverview`, `CronJobOverview` — same pattern

**Files to create:**
- `frontend/components/k8s/detail/LogsTab.tsx`

**Files to modify:**
- `frontend/islands/ResourceDetail.tsx` (add "Logs" tab conditionally)
- Or: modify each detail overview island to include LogsTab (evaluate which is cleaner)

---

## Phase 7B: Resource Dependency Graph

**Branch:** `feat/phase7b-dependency-graph`

### Step 7B.1 — Topology Graph Builder (Backend)

**New package: `backend/internal/topology/`**

**`backend/internal/topology/graph.go`**

Core types:
```go
type Graph struct {
    Nodes []Node `json:"nodes"`
    Edges []Edge `json:"edges"`
}
type Node struct {
    ID        string            `json:"id"`        // uid
    Kind      string            `json:"kind"`
    Name      string            `json:"name"`
    Namespace string            `json:"namespace"`
    Health    Health            `json:"health"`     // healthy, degraded, failing, unknown
    Summary   string            `json:"summary"`    // e.g., "3/3 ready"
    Labels    map[string]string `json:"labels,omitempty"`
}
type Edge struct {
    Source string   `json:"source"` // node ID
    Target string   `json:"target"` // node ID
    Type   EdgeType `json:"type"`   // owner, selector, mount, ingress, binding, sa
}
type Health string
const (
    HealthHealthy  Health = "healthy"
    HealthDegraded Health = "degraded"
    HealthFailing  Health = "failing"
    HealthUnknown  Health = "unknown"
)
type EdgeType string
const (
    EdgeOwner    EdgeType = "owner"
    EdgeSelector EdgeType = "selector"
    EdgeMount    EdgeType = "mount"
    EdgeIngress  EdgeType = "ingress"
    EdgeBinding  EdgeType = "binding"
    EdgeSA       EdgeType = "sa"
)
```

**`backend/internal/topology/builder.go`**

- `Builder` struct holding `*k8s.InformerManager` and `*slog.Logger`
- `BuildNamespaceGraph(ctx, namespace string, userNamespaces []string) (*Graph, error)`:
  1. **Collect resources** from informer cache listers: `Pods(ns)`, `Services(ns)`, `Deployments(ns)`, `ReplicaSets(ns)`, `StatefulSets(ns)`, `DaemonSets(ns)`, `Jobs(ns)`, `CronJobs(ns)`, `Ingresses(ns)`, `ConfigMaps(ns)`, `Secrets(ns)`, `PVCs(ns)`, `HPAs(ns)`, `PDBs(ns)`, `NetworkPolicies(ns)`, `ServiceAccounts(ns)`
  2. **Build nodes** — create `Node` for each resource with UID as ID
  3. **Owner reference edges** — single pass over all resources, for each ownerRef create `EdgeOwner` edge from owner UID to child UID
  4. **Service→Pod edges** — for each Service with non-nil selector, match against pod labels. Use `labels.Set(svc.Spec.Selector).AsSelector().Matches(labels.Set(pod.Labels))`
  5. **Ingress→Service edges** — walk `spec.rules[].http.paths[].backend.service.name`, find matching Service node by name
  6. **Pod→ConfigMap/Secret/PVC edges** — walk `pod.Spec.Volumes[]`, create `EdgeMount` for configMap, secret, persistentVolumeClaim references
  7. **Pod→ServiceAccount edges** — `pod.Spec.ServiceAccountName` → find SA node
  8. **HPA→target edges** — `hpa.Spec.ScaleTargetRef` → find target Deployment/StatefulSet
  9. **PVC→PV edges** — `pvc.Spec.VolumeName` → PV (cluster-scoped, cross-namespace). Only include PV if user has cluster-admin or PV is referenced by an accessible PVC.
  - RBAC filtering: omit nodes for resource kinds the user can't `list`

- `BuildFocusedGraph(ctx, namespace, kind, name string, userNamespaces []string) (*Graph, error)`:
  - Build full namespace graph, then BFS/DFS from target node to collect only upstream (owners, services routing to it) and downstream (children, pods it routes to) nodes. Prune everything else.

**`backend/internal/topology/health.go`**

- `ComputeHealth(node *Node, obj runtime.Object) Health`:
  - Pods: check `status.phase` (Running=healthy, Pending=degraded, Failed/Unknown=failing), check container statuses for CrashLoopBackOff/ImagePullBackOff
  - Deployments: compare `status.readyReplicas` vs `spec.replicas` (equal=healthy, partial=degraded, 0=failing)
  - StatefulSets, DaemonSets: same pattern
  - Jobs: `status.succeeded > 0`=healthy, `status.failed > 0`=failing, else unknown
  - Services: based on matching pod health
  - Nodes: conditions (Ready=healthy, else degraded/failing)
  - Default: unknown

- `PropagateHealth(graph *Graph)`:
  - Walk edges in reverse (children → parents). If any child is failing, parent is at least degraded. If all children failing, parent is failing.
  - Only propagate through `EdgeOwner` and `EdgeSelector` edges (not mounts)

**`backend/internal/topology/cache.go`**

- Per-namespace graph cache: `map[string]*cachedGraph` with `sync.RWMutex`
- `cachedGraph` holds `*Graph` + `time.Time` (last computed)
- Invalidation: register event handler on informer (via callback). On any event in a namespace, mark that namespace's cache as dirty.
- Debounce: don't recompute immediately on event. Lazy — recompute on next API request if cache is dirty or older than 2s.
- Max cache entries: 100 namespaces. LRU eviction.

**Files to create:**
- `backend/internal/topology/graph.go` (types)
- `backend/internal/topology/builder.go` (graph construction)
- `backend/internal/topology/health.go` (health computation + propagation)
- `backend/internal/topology/cache.go` (caching layer)
- `backend/internal/topology/handler.go` (HTTP handlers)

### Step 7B.2 — Topology HTTP Handlers

**`backend/internal/topology/handler.go`**

- `Handler` struct: `Builder *Builder`, `Cache *Cache`, `Logger *slog.Logger`, `RBACChecker auth.RBACChecker`

**Handlers:**

1. `HandleNamespaceGraph(w, r)` — `GET /api/v1/topology/{namespace}`
   - Extract namespace from URL
   - Get user's allowed namespaces for RBAC filtering
   - Check cache → if fresh, return. If stale, rebuild.
   - Return `Graph` via `httputil.WriteData()`

2. `HandleFocusedGraph(w, r)` — `GET /api/v1/topology/{namespace}/{kind}/{name}`
   - Build focused graph (target + upstream + downstream only)
   - RBAC-filtered

3. `HandleHealthSummary(w, r)` — `GET /api/v1/topology/{namespace}?summary=true`
   - Query param `summary=true` triggers summary mode
   - Returns counts: `{healthy: N, degraded: N, failing: N, total: N}`
   - Lightweight — uses cached graph without full serialization

**Route registration:**
```go
func (s *Server) registerTopologyRoutes(ar chi.Router) {
    ar.Route("/topology", func(tr chi.Router) {
        tr.Get("/{namespace}", s.TopologyHandler.HandleNamespaceGraph) // summary=true variant
        tr.Get("/{namespace}/{kind}/{name}", s.TopologyHandler.HandleFocusedGraph)
    })
}
```

**Files to create:**
- `backend/internal/topology/handler.go`

**Files to modify:**
- `backend/internal/server/server.go` (add TopologyHandler to Server + Deps)
- `backend/internal/server/routes.go` (add registerTopologyRoutes, nil-guard)
- `backend/cmd/kubecenter/main.go` (create Builder, Cache, Handler, wire)

### Step 7B.3 — Dependency Graph Frontend

**New island: `frontend/islands/DependencyGraph.tsx`**

Large island (~800-1000 LOC). Follow ClusterTopology.tsx patterns.

- **Dependencies:** Add `dagre` to `frontend/deno.json`: `"dagre": "npm:dagre@^0.8.5"` and `"@types/dagre": "npm:@types/dagre@^0.7.52"` (devDependency)
- **State signals:**
  - `graph: useSignal<Graph | null>(null)` — topology data from API
  - `selectedNode: useSignal<string | null>(null)` — clicked node ID
  - `hoveredNode: useSignal<string | null>(null)` — hovered node ID
  - `focusMode: useSignal(false)` — show only selected node's tree
  - `filterKind: useSignal("")` — filter by resource kind
  - `zoom: useSignal(1)`, `panX/panY: useSignal(0)` — viewport state
  - `loading: useSignal(true)`

- **Data fetching:** `useEffect` calls `apiGet<Graph>("/v1/topology/{namespace}")`. Re-fetch when `selectedNamespace` signal changes. Subscribe to WebSocket resource events for live updates (on ADDED/MODIFIED/DELETED, mark cache dirty and re-fetch with debounce).

- **Layout:** Use dagre for node positioning:
  ```ts
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "LR", ranksep: 100, nodesep: 50, marginx: 40, marginy: 40 });
  graph.nodes.forEach(n => g.setNode(n.id, { label: n.name, width: 180, height: 56 }));
  graph.edges.forEach(e => g.setEdge(e.source, e.target));
  dagre.layout(g);
  ```

- **SVG rendering:** viewBox-based zoom/pan (same pattern as ClusterTopology):
  - Nodes: `<g>` containing `<rect>` (rounded, themed stroke color based on health) + `<text>` (kind label + name)
  - Edges: `<path>` with dagre control points, styled by edge type (solid/dashed/dotted)
  - Health colors: `var(--status-success)` / `var(--status-warning)` / `var(--status-error)` / `var(--text-muted)`
  - Failing nodes: pulsing animation via CSS `@keyframes`
  - Hover: dim non-connected nodes (opacity 0.2), highlight connected edges

- **Progressive disclosure:** Initially collapse ReplicaSets and Pods. Deployments show "3 pods ✓" badge. Click to expand. Collapsed groups render as single node with count.

- **Slide-out panel:** When `selectedNode` is set, render an absolutely-positioned panel on the right (300px width) showing:
  - Resource kind + name + namespace
  - Health status badge
  - Conditions (if any)
  - Recent events (last 5, from existing events API)
  - Quick actions: "View Detail", "View Logs" (if Loki detected), "Investigate"

- **Zoom/Pan:** viewBox manipulation. Wheel zoom toward cursor. Click-drag pan. Fit-to-view button. Minimap (small SVG in bottom-right showing full graph outline with viewport rectangle).

**New route: `frontend/routes/observability/topology.tsx`**

**Files to create:**
- `frontend/islands/DependencyGraph.tsx`
- `frontend/routes/observability/topology.tsx`

**Files to modify:**
- `frontend/deno.json` (add dagre dependency)

---

## Phase 7C: Diagnostic Workspace

**Branch:** `feat/phase7c-diagnostics`

### Step 7C.1 — Diagnostic Rule Engine (Backend)

**New package: `backend/internal/diagnostics/`**

**`backend/internal/diagnostics/rule.go`**

```go
type Severity string
const (
    SeverityCritical Severity = "critical"
    SeverityWarning  Severity = "warning"
    SeverityInfo     Severity = "info"
)

type Result struct {
    RuleName    string   `json:"ruleName"`
    Status      string   `json:"status"` // "pass", "warn", "fail"
    Severity    Severity `json:"severity"`
    Message     string   `json:"message"`
    Detail      string   `json:"detail,omitempty"`
    Remediation string   `json:"remediation,omitempty"`
    Links       []Link   `json:"links,omitempty"` // related resource links
}

type Link struct {
    Label string `json:"label"`
    Kind  string `json:"kind"`
    Name  string `json:"name"`
}

type Rule interface {
    Name() string
    Severity() Severity
    AppliesTo() []string // resource kinds this rule checks
    Check(ctx context.Context, target *DiagnosticTarget) Result
}

type DiagnosticTarget struct {
    Kind       string
    Name       string
    Namespace  string
    Object     runtime.Object
    Pods       []corev1.Pod       // related pods (for workloads)
    Events     []corev1.Event     // recent events
    Conditions []metav1.Condition // resource conditions
    // Populated by the resolver before rules run
}
```

**Individual rule files** (one per file):

- `rule_crashloop.go` — Check pod container statuses for `CrashLoopBackOff` waiting reason. Report restart count, last exit code, last termination message.
- `rule_imagepull.go` — Check for `ImagePullBackOff` or `ErrImagePull` in container statuses. Report image name.
- `rule_oomkilled.go` — Check last termination reason for `OOMKilled`. Report memory limit vs actual usage if Prometheus available.
- `rule_pending.go` — Check for unschedulable pods. Parse scheduler events for reason (insufficient CPU/memory, node affinity, taints).
- `rule_probes.go` — Check for probe failure events. Distinguish liveness vs readiness. Report endpoint and failure count.
- `rule_resources.go` — Check CPU/memory usage vs limits (requires Prometheus). Warn if >90%. Requires `UtilizationProvider` interface (already exists).
- `rule_pvc_pending.go` — Check PVC phase != Bound. Report StorageClass and requested size.
- `rule_node_pressure.go` — Check node conditions for DiskPressure, MemoryPressure, PIDPressure. Report condition details.
- `rule_replica_mismatch.go` — Compare `spec.replicas` vs `status.readyReplicas`. Report delta.
- `rule_stale_rs.go` — For Deployments, check if old ReplicaSets still have replicas. Indicates stuck rollout.
- `rule_network_policy.go` — Check if NetworkPolicies exist that match the pod. Verify egress rules don't block expected traffic (best-effort based on policy spec).
- `rule_endpoints.go` — For Services, check endpoint count. Warn if zero endpoints.
- `rule_configmap_secret.go` — For pods, verify all referenced ConfigMaps and Secrets exist in the namespace.
- `rule_hpa.go` — Check HPA status conditions (ScalingActive, AbleToScale). Warn if at max replicas or metrics unavailable.

**`backend/internal/diagnostics/runner.go`**

- `Runner` struct: `rules []Rule`, `logger *slog.Logger`
- `NewRunner()` — registers all 14 rules
- `Run(ctx, target *DiagnosticTarget) []Result`:
  - Filter rules by `AppliesTo()` matching target kind
  - Run applicable rules in parallel (bounded goroutine pool, max 8)
  - Collect results, sort by severity (critical first)
  - Return all results (both pass and fail)

**`backend/internal/diagnostics/resolver.go`**

- `Resolver` struct: `informers *k8s.InformerManager`, `logger *slog.Logger`
- `Resolve(ctx, namespace, kind, name string) (*DiagnosticTarget, error)`:
  - Fetch target resource from informer cache
  - In parallel: fetch related pods (for workloads), events (filtered by involvedObject), conditions
  - For Deployments: also fetch ReplicaSets owned by the deployment
  - For Services: also fetch matching pods via selector
  - Populate `DiagnosticTarget` with all gathered data

**`backend/internal/diagnostics/blast.go`**

- `BlastRadius(graph *topology.Graph, targetID string) *BlastResult`:
  - BFS upstream from target (follow edges in reverse) → "potentially affected"
  - BFS downstream from target (follow edges forward) → "directly affected"
  - Annotate each affected node with impact description based on relationship type

```go
type BlastResult struct {
    DirectlyAffected   []AffectedResource `json:"directlyAffected"`
    PotentiallyAffected []AffectedResource `json:"potentiallyAffected"`
}
type AffectedResource struct {
    Kind      string `json:"kind"`
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
    Health    string `json:"health"`
    Impact    string `json:"impact"` // e.g., "1 of 3 endpoints healthy"
}
```

**Files to create:**
- `backend/internal/diagnostics/rule.go` (interfaces + types)
- `backend/internal/diagnostics/runner.go`
- `backend/internal/diagnostics/resolver.go`
- `backend/internal/diagnostics/blast.go`
- `backend/internal/diagnostics/rule_crashloop.go`
- `backend/internal/diagnostics/rule_imagepull.go`
- `backend/internal/diagnostics/rule_oomkilled.go`
- `backend/internal/diagnostics/rule_pending.go`
- `backend/internal/diagnostics/rule_probes.go`
- `backend/internal/diagnostics/rule_resources.go`
- `backend/internal/diagnostics/rule_pvc_pending.go`
- `backend/internal/diagnostics/rule_node_pressure.go`
- `backend/internal/diagnostics/rule_replica_mismatch.go`
- `backend/internal/diagnostics/rule_stale_rs.go`
- `backend/internal/diagnostics/rule_network_policy.go`
- `backend/internal/diagnostics/rule_endpoints.go`
- `backend/internal/diagnostics/rule_configmap_secret.go`
- `backend/internal/diagnostics/rule_hpa.go`

### Step 7C.2 — Diagnostics HTTP Handler

**`backend/internal/diagnostics/handler.go`**

- `Handler` struct: `Runner *Runner`, `Resolver *Resolver`, `TopologyBuilder *topology.Builder`, `Logger *slog.Logger`

**Handlers:**

1. `HandleDiagnostics(w, r)` — `GET /api/v1/diagnostics/{namespace}/{kind}/{name}`
   - Resolve target resource
   - Run diagnostic rules in parallel
   - Build topology graph (or use cache), compute blast radius
   - Fetch recent events for target
   - Return combined response:
     ```json
     {
       "data": {
         "target": { "kind": "Deployment", "name": "...", "namespace": "..." },
         "results": [ { "ruleName": "...", "status": "fail", ... } ],
         "blastRadius": { "directlyAffected": [...], "potentiallyAffected": [...] },
         "events": [ ... ]
       }
     }
     ```

2. `HandleNamespaceSummary(w, r)` — `GET /api/v1/diagnostics/{namespace}/summary`
   - List all resources in namespace from informer cache
   - Quick-scan for failing resources (pod phase != Running, deployment ready != desired)
   - Return summary: `{ "failing": [...], "degraded": [...], "total": N }`
   - Lighter than full diagnostics — no rule execution, just status checks

**Route registration:**
```go
func (s *Server) registerDiagnosticsRoutes(ar chi.Router) {
    ar.Route("/diagnostics", func(dr chi.Router) {
        dr.Get("/{namespace}/summary", s.DiagnosticsHandler.HandleNamespaceSummary)
        dr.Get("/{namespace}/{kind}/{name}", s.DiagnosticsHandler.HandleDiagnostics)
    })
}
```

**Files to create:**
- `backend/internal/diagnostics/handler.go`

**Files to modify:**
- `backend/internal/server/server.go` (add DiagnosticsHandler)
- `backend/internal/server/routes.go` (add registerDiagnosticsRoutes)
- `backend/cmd/kubecenter/main.go` (wire diagnostics)

### Step 7C.3 — Investigation Persistence

**New package: `backend/internal/investigation/`**

**Database migration:**

`backend/internal/store/migrations/000005_create_investigations.up.sql`:
```sql
CREATE TABLE investigations (
    id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
    user_id     TEXT NOT NULL REFERENCES local_users(id) ON DELETE CASCADE,
    cluster_id  TEXT NOT NULL DEFAULT 'local',
    namespace   TEXT NOT NULL,
    kind        TEXT NOT NULL,
    name        TEXT NOT NULL,
    results     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_investigations_user ON investigations(user_id, created_at DESC);
CREATE INDEX idx_investigations_resource ON investigations(cluster_id, namespace, kind, name);
```

`backend/internal/store/migrations/000005_create_investigations.down.sql`:
```sql
DROP TABLE IF EXISTS investigations;
```

**Note:** `user_id` is `TEXT` referencing `local_users(id)` (not UUID — matches existing schema).

**`backend/internal/investigation/store.go`**

- `Store` struct with `*pgxpool.Pool`
- `NewStore(pool *pgxpool.Pool) *Store`
- Methods:
  - `Create(ctx, userID, clusterID, namespace, kind, name string, results json.RawMessage) (string, error)` — returns ID
  - `List(ctx, userID string, limit, offset int) ([]Investigation, int, error)` — paginated, returns total count
  - `Get(ctx, id string) (*Investigation, error)`
  - `Delete(ctx, id, userID string) error` — only owner or admin can delete

**`backend/internal/investigation/handler.go`**

- `Handler` struct: `Store *Store`, `Logger *slog.Logger`

**Handlers:**

1. `HandleCreate(w, r)` — `POST /api/v1/investigations`
   - Body: `{ "namespace": "...", "kind": "...", "name": "...", "results": {...} }`
   - Extract user ID from auth context
   - Insert via store
   - Return created investigation with ID

2. `HandleList(w, r)` — `GET /api/v1/investigations`
   - Query params: `limit` (default 20), `offset` (default 0)
   - List user's investigations (or all if admin)
   - Return with pagination metadata

3. `HandleGet(w, r)` — `GET /api/v1/investigations/{id}`
   - Return investigation by ID
   - 404 if not found, 403 if not owner and not admin

4. `HandleDelete(w, r)` — `DELETE /api/v1/investigations/{id}`
   - Only owner or admin can delete
   - Audit log the deletion

**Route registration:**
```go
func (s *Server) registerInvestigationRoutes(ar chi.Router) {
    ar.Route("/investigations", func(ir chi.Router) {
        ir.Use(middleware.RateLimit(s.WriteLimiter))
        ir.Post("/", s.InvestigationHandler.HandleCreate)
        ir.Get("/", s.InvestigationHandler.HandleList)
        ir.Get("/{id}", s.InvestigationHandler.HandleGet)
        ir.Delete("/{id}", s.InvestigationHandler.HandleDelete)
    })
}
```

**Files to create:**
- `backend/internal/store/migrations/000005_create_investigations.up.sql`
- `backend/internal/store/migrations/000005_create_investigations.down.sql`
- `backend/internal/investigation/store.go`
- `backend/internal/investigation/handler.go`

**Files to modify:**
- `backend/internal/server/server.go` (add InvestigationHandler)
- `backend/internal/server/routes.go` (add registerInvestigationRoutes)
- `backend/cmd/kubecenter/main.go` (wire investigation store + handler)

### Step 7C.4 — Diagnostic Workspace Frontend

**New island: `frontend/islands/DiagnosticWorkspace.tsx`**

~600-800 LOC island.

- **State signals:**
  - `target: useSignal<{namespace, kind, name} | null>(null)` — from URL params or props
  - `diagnostics: useSignal<DiagnosticResponse | null>(null)`
  - `loading: useSignal(false)`
  - `error: useSignal<string | null>(null)`
  - `investigations: useSignal<Investigation[]>([])` — saved investigation list

- **URL-driven:** Read `?namespace=X&kind=Y&name=Z` from URL params. If present, auto-run diagnostics on mount. If not, show a resource picker (namespace dropdown + kind dropdown + name search).

- **Diagnostic results display:**
  - Status banner: "{N} Critical / {M} Warning Issues Detected" with colored background
  - Two-column layout using existing SplitPane:
    - **Left: Checklist** — failed checks expanded (card with icon, title, severity badge, message, detail, remediation, action links). Passed checks collapsed (single row with checkmark, name, summary).
    - **Right: Blast Radius** — grouped by impact tier ("Directly Affected" with red border, "Potentially Affected" with amber border). Each resource is a clickable row showing kind, name, health, impact description.
  - Below: Recent events table (reuse existing event display patterns)

- **Actions:**
  - "Re-scan" button — re-fetches diagnostics
  - "Save Investigation" button — `apiPost("/v1/investigations", {namespace, kind, name, results: diagnostics})`. Show toast on success.
  - "View Logs" link on check results — opens Log Explorer pre-filtered
  - "Pod Detail" link — navigates to pod detail page
  - Each blast radius resource is clickable → navigate to its detail page

- **Saved Investigations tab:** Toggle between "Diagnose" and "History". History tab shows `apiGet("/v1/investigations")` with list of saved investigations. Click to view snapshot. Delete button with confirmation.

**New route: `frontend/routes/observability/investigate.tsx`**

**Files to create:**
- `frontend/islands/DiagnosticWorkspace.tsx`
- `frontend/routes/observability/investigate.tsx`

### Step 7C.5 — Integrate "Investigate" Entry Points

Add "Investigate" buttons/links across the UI:

- **Resource detail pages:** Add "Investigate" button in the header area of resource detail islands (PodOverview, DeploymentOverview, etc.). Only show when resource has non-healthy status. Links to `/observability/investigate?namespace=X&kind=Y&name=Z`.

- **Dependency graph:** The slide-out panel (from 7B.3) includes "Investigate" as a quick action. Clicking navigates to the investigate page.

- **Dashboard:** The health score widget links to `/observability/investigate?namespace=...` for degraded namespaces.

- **Command palette:** Add "Investigate" action to command palette actions in `frontend/lib/constants.ts` or the CommandPalette island. Search format: "Investigate {resource}".

**Files to modify:**
- `frontend/islands/ResourceDetail.tsx` or individual overview islands (add Investigate button)
- `frontend/islands/DependencyGraph.tsx` (add Investigate to slide-out panel actions)
- `frontend/islands/DashboardV2.tsx` (add Investigate link to health score)
- `frontend/islands/CommandPalette.tsx` (add Investigate action)

---

## Phase 7D: Unified Timeline

**Branch:** `feat/phase7d-timeline`

### Step 7D.1 — Timeline Merger (Backend)

**New package: `backend/internal/timeline/`**

**`backend/internal/timeline/handler.go`**

- `Handler` struct with references to: `InformerManager`, `LokiClient` (optional), `AlertStore` (optional), `AuditStore`, `Logger`, `RBACChecker`

- `HandleTimeline(w, r)` — `GET /api/v1/timeline/{namespace}[/{kind}/{name}]`
  - Query params: `sources` (comma-separated: `events,logs,alerts,audit`, default all), `start`, `end` (RFC3339, default last 1h), `limit` (default 200, max 1000)
  - In parallel (using `sync.WaitGroup`):
    1. **Events** — list from informer cache filtered by namespace (and optionally involvedObject kind/name). Map to timeline entries.
    2. **Logs** (if Loki detected and source enabled) — `client.QueryRange()` with namespace filter, sampled (limit to 50 log entries to avoid overwhelming). Extract timestamps and first line.
    3. **Alerts** (if alert store available) — query alert history for time range and namespace.
    4. **Audit** — query audit log for namespace and time range.
  - Merge all entries into single list, sort by timestamp descending
  - Deduplicate (events can appear in both informer cache and audit log)
  - Respect `limit` parameter

- Timeline entry format:
  ```go
  type Entry struct {
      Timestamp time.Time         `json:"timestamp"`
      Source    string            `json:"source"` // "event", "log", "alert", "audit"
      Severity  string           `json:"severity"` // "info", "warning", "error"
      Message   string           `json:"message"`
      Resource  *ResourceRef     `json:"resource,omitempty"`
      Metadata  map[string]string `json:"metadata,omitempty"` // source-specific extras
  }
  ```

**Route registration:**
```go
func (s *Server) registerTimelineRoutes(ar chi.Router) {
    ar.Route("/timeline", func(tr chi.Router) {
        tr.Get("/{namespace}", s.TimelineHandler.HandleTimeline)
        tr.Get("/{namespace}/{kind}/{name}", s.TimelineHandler.HandleTimeline)
    })
}
```

**Files to create:**
- `backend/internal/timeline/handler.go`
- `backend/internal/timeline/types.go`

**Files to modify:**
- `backend/internal/server/server.go` (add TimelineHandler)
- `backend/internal/server/routes.go` (add registerTimelineRoutes)
- `backend/cmd/kubecenter/main.go` (wire timeline handler)

### Step 7D.2 — Timeline Frontend

**New island: `frontend/islands/Timeline.tsx`**

~400 LOC island.

- **State signals:**
  - `entries: useSignal<TimelineEntry[]>([])` — timeline data
  - `sources: useSignal({events: true, logs: true, alerts: true, audit: true})` — source toggles
  - `timeRange: useSignal({start, end, preset: "1h"})`
  - `loading: useSignal(true)`

- **Filter bar:** Source toggles (chip-style buttons for events/logs/alerts/audit). Time range selector (same component as Log Explorer).

- **Timeline display:** Vertical chronological list. Each entry:
  - Left gutter: timestamp (relative, e.g., "2m ago") + absolute on hover
  - Source icon/badge: colored by source type
  - Severity indicator: colored dot
  - Message text
  - Resource link (if applicable) → navigate to resource detail
  - Click log entry → open Log Explorer pre-filtered to that timestamp
  - Click alert entry → navigate to alert detail
  - Click event → expand to show full event details inline

- **Auto-refresh:** Poll every 30s if viewing recent data (last 1h). No polling for historical ranges.

**New route: `frontend/routes/observability/timeline.tsx`**

**Files to create:**
- `frontend/islands/Timeline.tsx`
- `frontend/routes/observability/timeline.tsx`

---

## Phase 7E: Navigation Restructure

**Branch:** `feat/phase7e-navigation`

### Step 7E.1 — Consolidate Observability Navigation

**`frontend/lib/constants.ts`**

Update the existing `observability` section in `DOMAIN_SECTIONS` (currently at line ~542):

```ts
{
  id: "observability",
  label: "Observability",
  icon: "activity",
  href: "/observability",
  tabs: [
    { label: "Overview", href: "/observability" },
    { label: "Log Explorer", href: "/observability/logs" },
    { label: "Topology", href: "/observability/topology" },
    { label: "Investigate", href: "/observability/investigate" },
    { label: "Timeline", href: "/observability/timeline" },
    { label: "Dashboards", href: "/observability/dashboards" },
    { label: "Alerts", href: "/observability/alerts" },
    { label: "Prometheus", href: "/observability/prometheus" },
  ],
},
```

Remove the separate "alerting" section from DOMAIN_SECTIONS (if it exists as a separate entry).

**Route migration:** Create new routes under `/observability/` that wrap existing islands:

- `frontend/routes/observability/index.tsx` — Enhanced overview (current monitoring overview + health summary from topology)
- `frontend/routes/observability/dashboards.tsx` — Wraps existing MonitoringDashboards island
- `frontend/routes/observability/prometheus.tsx` — Wraps existing PromQLQuery island
- `frontend/routes/observability/alerts.tsx` — Wraps existing AlertsPage island
- `frontend/routes/observability/alert-rules.tsx` — Wraps existing AlertRulesPage island (if separate from alerts)
- `frontend/routes/observability/alert-settings.tsx` — Wraps existing AlertSettings island (admin-only)

**Old routes:** Keep `/monitoring/*` and `/alerting/*` routes temporarily as redirects to `/observability/*`. Remove them in a follow-up cleanup after verifying all internal links are updated.

**Update internal navigation links:**
- Search codebase for `/monitoring` and `/alerting` href references
- Update command palette navigation items
- Update breadcrumbs
- Update dashboard links

**Files to create:**
- `frontend/routes/observability/index.tsx`
- `frontend/routes/observability/dashboards.tsx`
- `frontend/routes/observability/prometheus.tsx`
- `frontend/routes/observability/alerts.tsx`
- `frontend/routes/observability/alert-rules.tsx`
- `frontend/routes/observability/alert-settings.tsx`

**Files to modify:**
- `frontend/lib/constants.ts` (update DOMAIN_SECTIONS, update RESOURCE_DETAIL_PATHS)
- `frontend/islands/CommandPalette.tsx` (update navigation items)
- `frontend/islands/TopBarV2.tsx` (update any hardcoded monitoring/alerting references)
- Various islands/components that link to `/monitoring/*` or `/alerting/*`

### Step 7E.2 — E2E Tests

Add Playwright E2E tests for the new observability features.

**New test suite: `e2e/observability.spec.ts`**

Tests:
- Navigate to `/observability` — verify overview renders
- Navigate to `/observability/logs` — verify Log Explorer renders (may show "Loki not detected" in CI)
- Navigate to `/observability/topology` — verify dependency graph renders with some resources
- Navigate to `/observability/investigate` — verify diagnostic workspace renders
- Navigate to `/observability/timeline` — verify timeline renders with events
- Navigate to `/observability/dashboards` — verify dashboards page renders
- Navigate to `/observability/alerts` — verify alerts page renders
- Navigate to `/observability/prometheus` — verify Prometheus query page renders
- Old routes `/monitoring` and `/alerting` redirect to `/observability/*`

**Note:** Loki won't be available in CI (kind cluster). Tests for Loki-specific features should verify graceful degradation (correct empty states, not errors).

**Files to create:**
- `e2e/observability.spec.ts`

**Files to modify:**
- `e2e/navigation.spec.ts` (update any monitoring/alerting navigation tests)

### Step 7E.3 — Lint, Type-Check, Verify

Final verification pass:
- `npx tsc --noEmit` (if applicable) or `deno check`
- `make lint` (go vet + deno lint)
- `make test` (unit tests)
- `make test-e2e` (E2E tests)
- Verify all new routes render without errors
- Verify graceful degradation when Loki is not available
- Check for any hardcoded `/monitoring` or `/alerting` references remaining

---

## Dependency Graph

```
Phase 7A (Loki) ──────────────────────────────┐
                                               ├── Phase 7E (Navigation + E2E)
Phase 7B (Topology) ──┬── Phase 7C (Diagnostics)│
                      │                        ├──┘
Phase 7D (Timeline) ──┘   (uses topology for   │
  (uses Loki for log      blast radius)         │
   entries)                                     │
```

- **7A and 7B can run in parallel** — no dependencies between Loki and Topology
- **7C depends on 7B** — blast radius requires topology graph
- **7D depends on 7A (partially)** — timeline log source needs Loki client, but can work without it
- **7E depends on all** — navigation restructure pulls everything together

## File Count Summary

| Phase | New Files | Modified Files |
|-------|-----------|---------------|
| 7A (Loki) | ~10 | ~8 |
| 7B (Topology) | ~7 | ~4 |
| 7C (Diagnostics) | ~22 | ~8 |
| 7D (Timeline) | ~4 | ~4 |
| 7E (Navigation) | ~8 | ~8 |
| **Total** | **~51** | **~32** |

## Risk Mitigation

- **Loki LogQL dependency size:** Evaluate `github.com/grafana/loki/v3/pkg/logql/syntax` dependency weight before committing. Fall back to regex-based namespace injection if too heavy.
- **Dagre in Deno/Fresh:** Verify dagre works with Deno's npm compatibility layer before committing to it. If issues, use a custom topological sort layout (acceptable for tree-shaped graphs).
- **Performance:** Topology graph for large namespaces (100+ resources) could be slow. The 2s debounce cache prevents repeated computation. Add pagination or resource count limits if needed.
- **Loki unavailable:** Every Loki-dependent feature must gracefully degrade. Test with Loki disabled in development.

## Spec Flow Gaps (from analysis — addressed in implementation)

These gaps were identified by spec-flow analysis and are handled in the steps above:

1. **Loki query errors:** Forward Loki error messages (400 bad LogQL, 429 rate limit, 500) as `httputil.WriteError()` with Loki's error text as detail. Frontend shows in ErrorBanner.
2. **WS tail reconnection:** Follow existing `lib/ws.ts` reconnect pattern — exponential backoff (1s to 30s), max 15 attempts. Show "Reconnecting..." indicator.
3. **Topology graph build errors:** Graceful — skip malformed selectors, log warning. Return partial graph rather than erroring. Add `warnings: []string` field to Graph response.
4. **Diagnostic rule panics:** Wrap each `rule.Check()` in `recover()`. Panicked rules return `{status: "error", message: "internal error"}` rather than crashing the runner. Per-rule timeout: 5s.
5. **Save investigation failures:** Frontend shows toast error on failure. No retry — user clicks "Save" again. Standard pattern for all POST endpoints.
6. **Audit log query performance:** Timeline handler sets 5s timeout per source. Audit queries use `LIMIT` clause matching the global limit param. Existing `audit_logs` table has `created_at` index.
7. **Blast radius cycle detection:** BFS uses `visited` set (by UID). Cycles are impossible for owner refs (DAG by definition) but possible for selector edges — visited set prevents infinite loops.
8. **LogQL namespace injection precedence:** `EnforceNamespaces()` **replaces** any existing namespace matcher (not AND). User cannot override. If user's query references a namespace they lack access to, it's silently replaced with their permitted set. This is the same behavior as Grafana's multi-tenant proxy.
9. **Volume endpoint params:** Uses Loki's `/loki/api/v1/index/volume_range` (label-based). Accepts same filter params as query (namespace, pod, etc.) but constructs a label matcher internally rather than forwarding raw LogQL.
10. **Topology namespace validation:** Use existing `resources.ValidateURLParams` middleware (already applied in resource route group). Apply same to topology routes.
11. **Investigation RBAC:** `GET /api/v1/investigations` returns only current user's investigations. Admins see all. `GET /api/v1/investigations/{id}` returns 403 if not owner and not admin.
12. **Topology real-time updates:** Frontend subscribes to key resource kinds (pods, deployments, services) via existing WS hub. On any event in current namespace, debounce 2s then re-fetch `/api/v1/topology/{namespace}`. Full re-fetch, not incremental patching — simpler and the response is small (JSON, not raw objects).
13. **Graph cache freshness:** Add `computedAt: ISO8601` field to Graph response. Frontend can display "Updated 3s ago" indicator. Re-fetch on user action (click "Refresh") or on WS event debounce.
14. **Old route redirects:** Keep `/monitoring/*` and `/alerting/*` routes as HTTP 302 redirects to `/observability/*` equivalents. Remove after one release cycle.
