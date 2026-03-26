# Frontend Redesign Follow-Up: Deferred P2/P3 Items

## Overview

Address 4 deferred items from the Phase 6 frontend redesign code review. These were deferred because they require backend API work, CSS architecture changes, or design decisions that couldn't be resolved during the initial implementation pass.

## Problem Statement

The Phase 6 redesign introduced a dashboard-first UI that makes significantly more API calls than the old sidebar-driven UI. The overview dashboard alone fires **16 parallel API calls** on mount, with nodes/pods/services fetched 3x each by three independent islands. SubNav adds 5-7 count calls per domain page. Additionally, non-default theme users experience a flash of Nexus colors before JS hydration applies their chosen theme.

## Items

### 1. Backend Dashboard Summary Endpoint (P2 — High Impact)

**Problem:** DashboardV2, HealthScoreRing, and ClusterTopology each independently fetch nodes, pods, services from the API. On a 2,000-pod cluster, this means downloading the full pod list (1-3 MB) multiple times on every dashboard load, plus every 60 seconds from HealthScoreRing.

**Solution:** Single `GET /api/v1/cluster/dashboard-summary` endpoint that returns all dashboard data in one response.

**Backend — New Files:**
- `backend/internal/k8s/resources/dashboard.go` — Handler using informer cache

**Response shape:**
```go
// GET /api/v1/cluster/dashboard-summary
type DashboardSummary struct {
    Nodes   NodeSummary   `json:"nodes"`
    Pods    PodSummary    `json:"pods"`
    Services ServiceCount `json:"services"`
    Alerts  AlertSummary  `json:"alerts"`
    CPU     Utilization   `json:"cpu"`
    Memory  Utilization   `json:"memory"`
    Events  []EventItem   `json:"events"`
    // Topology data — limited sets for visualization
    Topology TopologyData `json:"topology"`
}

type NodeSummary struct {
    Total int `json:"total"`
    Ready int `json:"ready"`
    // List of node names + status for topology (max 10)
    Items []TopologyNode `json:"items"`
}

type PodSummary struct {
    Total   int `json:"total"`
    Running int `json:"running"`
    Pending int `json:"pending"`
    Failed  int `json:"failed"`
}

type ServiceCount struct {
    Total int `json:"total"`
}

type AlertSummary struct {
    Active   int `json:"active"`
    Critical int `json:"critical"`
}

type Utilization struct {
    Percentage float64 `json:"percentage"`
}

type TopologyData struct {
    Nodes    []TopologyNode    `json:"nodes"`    // max 5
    Services []TopologyService `json:"services"` // max 6, with selectors
    Pods     []TopologyPod     `json:"pods"`     // max 8, with labels + node
    Ingresses []TopologyIngress `json:"ingresses"` // max 3
}
```

**Implementation approach:**
- Use informer cache listers: `h.Informers.Nodes().List(labels.Everything())`, `.Pods()`, `.Services()`
- Count by iterating and checking `status.conditions` (nodes) and `status.phase` (pods) — same as HealthScoreRing does client-side today
- For CPU/memory: query Prometheus via `h.MonitoringHandler.Discoverer.PrometheusClient()` if available, return 0 if not
- For events: `h.Informers.Events().List()` sorted by `lastTimestamp` desc, limit 10
- For topology: return limited slices with only the fields needed for visualization (name, namespace, labels, selectors, nodeName)
- Register route in `routes.go` under the cluster group: `ar.Get("/cluster/dashboard-summary", h.HandleDashboardSummary)`

**Frontend changes:**
- `frontend/islands/DashboardV2.tsx` — Replace 8+ `apiGet` calls with single `apiGet<DashboardSummary>("/v1/cluster/dashboard-summary")`
- `frontend/islands/HealthScoreRing.tsx` — Accept summary data as props instead of fetching independently. Remove 60s polling (parent handles refresh).
- `frontend/islands/ClusterTopology.tsx` — Accept topology data as props instead of fetching
- `frontend/islands/MetricCard.tsx` — No changes (already receives data via props)

**Acceptance Criteria:**
- [ ] Dashboard page makes 1 API call instead of 16
- [ ] HealthScoreRing receives data via props, no independent fetching
- [ ] ClusterTopology receives data via props, no independent fetching
- [ ] Graceful fallback when Prometheus unavailable (CPU/memory return 0)
- [ ] Response under 50KB for a 100-node, 2000-pod cluster
- [ ] `go vet` and `go test` pass
- [ ] `deno fmt && deno lint` pass
- [ ] Backend unit test for the handler

---

### 2. Backend Batch Resource Counts Endpoint (P2 — Medium Impact)

**Problem:** SubNav fires 5-7 individual `?limit=1` API calls per domain page to get total resource counts. The Workloads page alone fires 7 count calls plus 2 summary calls from WorkloadsDashboard.

**Solution:** Single `GET /api/v1/resources/counts` endpoint.

**Backend — New File:**
- `backend/internal/k8s/resources/counts.go`

**Request/Response:**
```
GET /api/v1/resources/counts?kinds=deployments,pods,services,statefulsets&namespace=default

Response:
{
  "data": {
    "counts": {
      "deployments": 32,
      "pods": 127,
      "services": 45,
      "statefulsets": 8
    }
  }
}
```

**Implementation approach:**
- Parse `kinds` query param (comma-separated)
- Parse optional `namespace` param
- For each kind, use the informer lister to get `len(lister.List(selector))` — this is O(1) per kind from the informer cache
- Map kind strings to informer accessors (similar to how `resources/handler.go` maps URL params)
- Limit to max 20 kinds per request to prevent abuse
- Register: `ar.Get("/resources/counts", h.HandleResourceCounts)`

**Frontend changes:**
- `frontend/islands/SubNav.tsx` — Replace N individual `apiGet` calls with single `apiGet<{counts: Record<string, number>}>` call
- `frontend/islands/WorkloadsDashboard.tsx` — Use counts endpoint for summary strip instead of fetching full resource lists

**Acceptance Criteria:**
- [ ] SubNav makes 1 API call instead of 5-7 per domain page
- [ ] WorkloadsDashboard summary strip uses counts endpoint
- [ ] Namespace-aware (scoped counts when namespace selected)
- [ ] Backend unit test
- [ ] `go vet`, `go test`, `deno fmt`, `deno lint` all pass

---

### 3. Fix FOUC for Non-Default Themes (P3 — Low Impact)

**Problem:** Users who select e.g. Dracula theme see Nexus colors flash briefly on page load. The inline script in `_app.tsx` sets `data-theme` from localStorage, but `:root` CSS defaults are Nexus. CSS variables aren't applied until `initTheme()` runs during hydration.

**Solution:** Add CSS `[data-theme="..."]` rule blocks to `styles.css` for each theme, so the `data-theme` attribute alone is sufficient for correct colors — no JS needed.

**File:** `frontend/assets/styles.css`

**Implementation:**
Add after the `:root` block:
```css
[data-theme="dracula"] {
  --bg-base: #282A36;
  --bg-surface: #2D2F3D;
  --bg-elevated: #343746;
  /* ... all 20 variables ... */
}

[data-theme="tokyo-night"] { /* ... */ }
[data-theme="catppuccin"] { /* ... */ }
[data-theme="nord"] { /* ... */ }
[data-theme="one-dark"] { /* ... */ }
[data-theme="gruvbox"] { /* ... */ }
```

Generate these from `lib/themes.ts` THEMES array to ensure consistency.

**Trade-off:** Adds ~2KB to the CSS bundle (20 variables x 7 themes = 140 declarations). Acceptable for instant theme application.

**Acceptance Criteria:**
- [ ] Non-default theme users see correct colors immediately on page load (no flash)
- [ ] Theme switching still works dynamically (JS overrides CSS attribute selectors)
- [ ] CSS values match `lib/themes.ts` exactly
- [ ] `deno fmt && deno lint` pass

---

### 4. Health Score Services Sub-Score Redesign (P3 — Low Impact)

**Problem:** The services sub-score in `lib/health-score.ts` is binary — always 100% if any service exists (which is every cluster due to the default `kubernetes` service). This makes 15% of the overall health score a constant.

**Solution:** Replace the services sub-score with **endpoint readiness** — measure what percentage of services have healthy endpoints.

**File:** `frontend/lib/health-score.ts`

**New calculation:**
```typescript
// Services score: % of services with at least one ready endpoint
// Requires endpoint data (from dashboard-summary endpoint)
const servicesWithEndpoints = metrics.servicesWithReadyEndpoints ?? metrics.servicesTotal;
const services = metrics.servicesTotal > 0
  ? Math.round((servicesWithEndpoints / metrics.servicesTotal) * 100)
  : 100; // No services = not applicable = full score
```

**Dependencies:** This requires the dashboard-summary endpoint (Task 1) to include `servicesWithReadyEndpoints` count. The backend can calculate this by checking if each service's selector matches any ready pod.

**Acceptance Criteria:**
- [ ] Services sub-score reflects endpoint readiness, not mere existence
- [ ] Fresh clusters with no services score 100% (not penalized)
- [ ] Dashboard-summary endpoint includes `servicesWithReadyEndpoints` field
- [ ] `deno fmt && deno lint` pass

---

## Technical Considerations

**Dependency order:** Task 1 (dashboard summary) should be implemented first since Task 4 (health score) depends on it. Tasks 2 and 3 are independent.

**Testing:** Backend handlers should have unit tests using the existing test patterns in `backend/internal/`. Frontend changes should be verified with `deno fmt && deno lint && deno task build`.

**Performance target:** Dashboard page load should go from 16 API calls to 2 (1 summary + 1 for the active resource table if user drills down).

## References

- Phase 6 implementation plan: `docs/superpowers/plans/2026-03-26-frontend-redesign.md`
- Performance review findings: 16 duplicate API calls, MB-scale payload waste
- Backend route registration: `backend/internal/server/routes.go`
- Informer listers: `backend/internal/k8s/informers.go`
- Prometheus queries: `backend/internal/monitoring/prometheus.go`
- Response envelope: `backend/pkg/api/types.go`
