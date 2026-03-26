# Frontend Redesign Follow-Up: Deferred P2/P3 Items

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce the dashboard from 16 API calls to 3, batch SubNav counts into a single call, and fix the FOUC for non-default themes.

**Architecture:** Two new backend endpoints aggregate informer-cache data. CSS-only theme switching eliminates the FOUC. Health score services sub-score is simplified by removing the meaningless constant.

**Tech Stack:** Go 1.26 (backend), Deno 2.x / Fresh 2.x / Preact (frontend)

**Branch:** `feature/frontend-redesign-followup`

---

## Task 1: Backend Dashboard Summary Endpoint

**Problem:** DashboardV2, HealthScoreRing, and ClusterTopology independently fetch nodes, pods, services — 16 parallel API calls on mount, with MB-scale payloads discarded after counting.

**Solution:** `GET /api/v1/cluster/dashboard-summary` returns aggregated counts and utilization. Topology and events remain separate calls (they're already single calls each and have different data shapes).

### Backend

**Create:** `backend/internal/k8s/resources/dashboard.go`

```go
type DashboardSummary struct {
    Nodes    NodeSummary  `json:"nodes"`
    Pods     PodSummary   `json:"pods"`
    Services ServiceCount `json:"services"`
    Alerts   AlertSummary `json:"alerts"`
    CPU      *Utilization `json:"cpu"`      // null if Prometheus unavailable
    Memory   *Utilization `json:"memory"`   // null if Prometheus unavailable
}

type NodeSummary struct {
    Total int `json:"total"`
    Ready int `json:"ready"`
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
```

**Implementation notes:**
- Use informer listers: `h.Informers.Nodes().List(labels.Everything())`, `.Pods()`, `.Services()`
- Count by iterating: node `status.conditions[type=Ready].status=True`, pod `status.phase`
- **Prometheus queries via interface injection** — do NOT add a direct dependency from `k8s/resources` to `monitoring`. Define:
  ```go
  // In resources/handler.go
  type UtilizationProvider interface {
      CPUPercent(ctx context.Context) (float64, error)
      MemoryPercent(ctx context.Context) (float64, error)
  }
  ```
  Add `UtilizationProvider` field to `Handler` struct (can be nil). Wire in `server.go` with a monitoring adapter.
- **Prometheus queries MUST be async with 1s timeout.** Use `errgroup` with `context.WithTimeout(ctx, 1*time.Second)`. If queries don't return in time, return `null` for CPU/Memory. Never block the informer-based response on Prometheus.
- Alerts: query alerting store if available, return 0/0 if not
- Wrap response in standard `api.Response{Data: summary}` envelope
- Register: `ar.Get("/cluster/dashboard-summary", h.HandleDashboardSummary)`
- **Remote clusters:** Return error for non-local clusters (informer cache is local only). Document this in the handler comment.

**Modify:** `backend/internal/k8s/resources/handler.go` — add `UtilizationProvider` field
**Modify:** `backend/internal/server/server.go` — wire `UtilizationProvider` implementation
**Modify:** `backend/internal/server/routes.go` — register route

### Frontend

**Modify:** `frontend/islands/DashboardV2.tsx`
- Replace 8 `apiGet` calls with single `apiGet<DashboardSummary>("/v1/cluster/dashboard-summary")`
- **Add 60-second refresh polling** with `document.hidden` visibility check (replaces HealthScoreRing's old self-polling)
- Pass summary data to HealthScoreRing and MetricCards as props

**Modify:** `frontend/islands/HealthScoreRing.tsx`
- Accept health data as props: `{ nodes, pods, services, alerts }`
- Remove independent API fetching and 60s interval
- Keep health score calculation in `lib/health-score.ts` (pure function)

**Keep unchanged:** `ClusterTopology.tsx` (4 calls on mount is fine — different data shape, already optimized in P1 fix to not refetch on resize)

### Acceptance Criteria
- [ ] Dashboard page makes 3 API calls (summary + topology + events) instead of 16
- [ ] HealthScoreRing receives data via props, no independent fetching
- [ ] Dashboard auto-refreshes every 60s with visibility check
- [ ] Prometheus timeout at 1s, returns null on failure (no blocking)
- [ ] Remote clusters get a clear error, not empty data
- [ ] Response wrapped in standard `api.Response` envelope
- [ ] Backend unit test for the handler
- [ ] `go vet && go test ./...` pass
- [ ] `deno fmt && deno lint` pass

---

## Task 2: Backend Batch Resource Counts Endpoint

**Problem:** SubNav fires 5-7 individual `?limit=1` API calls per domain page to get resource counts.

**Solution:** `GET /api/v1/resources/counts` returns counts for all informer-tracked resource types in one call.

### Backend

**Create:** `backend/internal/k8s/resources/counts.go`

```go
// GET /api/v1/resources/counts[?namespace=default]
// Returns counts for all informer-tracked resource types.
// Response: {"data": {"deployments": 32, "pods": 127, "services": 45, ...}}
```

**Implementation notes:**
- No `?kinds=` query param — return all counts unconditionally. Informer reads are microseconds per kind.
- Optional `?namespace=` param for namespace-scoped counts
- **Whitelist of allowed kinds** — maintain an explicit `allowedKinds` map. Only return counts for known informer-cached types. Unknown kinds are silently omitted (not errored).
- For each kind: `len(lister.List(selector))` is O(n) from cache but fast (sub-millisecond for typical sizes)
- Register: `ar.Get("/resources/counts", h.HandleResourceCounts)`
- **Remote clusters:** Return error (informer cache is local only)

### Frontend

**Modify:** `frontend/islands/SubNav.tsx` — replace N individual `apiGet` calls with single `apiGet<{counts: Record<string, number>}>("/v1/resources/counts?namespace=...")` call

**Modify:** `frontend/islands/WorkloadsDashboard.tsx` — use counts endpoint for summary strip instead of `?limit=500` fetches

### Acceptance Criteria
- [ ] SubNav makes 1 API call instead of 5-7 per domain page
- [ ] Namespace-aware (scoped counts when namespace selected)
- [ ] Response includes all informer-tracked types
- [ ] Backend unit test
- [ ] `go vet && go test ./...`, `deno fmt && deno lint` pass

---

## Task 3: Fix FOUC for Non-Default Themes

**Problem:** Users with non-default themes see Nexus colors flash before JS hydration applies their chosen theme. The inline script sets `data-theme` but no CSS rules respond to it.

**Solution:** Add `[data-theme="..."]` CSS rule blocks for each theme in `styles.css`.

### Implementation

**Modify:** `frontend/assets/styles.css`

Add after the `:root` block — one `[data-theme="..."]` block per theme with all 20 CSS custom properties. Write by hand (no code generation), copy values from `lib/themes.ts`.

```css
/* Nexus is the :root default — no attribute selector needed */

[data-theme="dracula"] {
  --bg-base: #282A36;
  --bg-surface: #2D2F3D;
  --bg-elevated: #343746;
  --bg-hover: #3E4155;
  --border-primary: #44475A;
  --border-subtle: #383B4A;
  --text-primary: #F8F8F2;
  --text-secondary: #C0C0D0;
  --text-muted: #6272A4;
  --accent: #BD93F9;
  --accent-glow: rgba(189, 147, 249, 0.15);
  --accent-dim: rgba(189, 147, 249, 0.08);
  --accent-secondary: #FF79C6;
  --success: #50FA7B;
  --success-dim: rgba(80, 250, 123, 0.12);
  --warning: #F1FA8C;
  --warning-dim: rgba(241, 250, 140, 0.12);
  --error: #FF5555;
  --error-dim: rgba(255, 85, 85, 0.12);
  --info: #8BE9FD;
}

/* ... repeat for tokyo-night, catppuccin, nord, one-dark, gruvbox */
```

**Specificity note:** After hydration, `initTheme()` sets inline styles via `document.documentElement.style`, which override the CSS selectors. This is correct — CSS handles first paint, JS handles dynamic switching.

**CSS size:** ~2KB added (140 declarations). Compresses well with gzip.

### Acceptance Criteria
- [ ] Non-default theme users see correct colors immediately on page load
- [ ] Theme switching still works dynamically
- [ ] CSS values match `lib/themes.ts` exactly (manual verification)
- [ ] `deno fmt && deno lint` pass

---

## Task 4: Health Score — Remove Meaningless Services Sub-Score

**Problem:** Services sub-score is always 100% (every cluster has the `kubernetes` service). This makes 15% of the health score a constant.

**Solution:** Remove the services sub-score entirely. Redistribute weight to nodes, pods, alerts. This is a 5-line frontend change.

### Implementation

**Modify:** `frontend/lib/health-score.ts`

Change weights:
```typescript
const WEIGHTS = {
  nodes: 0.35,    // was 0.30
  pods: 0.35,     // was 0.30
  alerts: 0.30,   // was 0.25
  // services removed
};
```

Remove `services` from the `HealthScore` interface and `calculateHealthScore()` function.

**Modify:** `frontend/islands/HealthScoreRing.tsx` — remove services sub-score card (show 3 instead of 4)

### Acceptance Criteria
- [ ] Health score uses 3 dimensions: nodes, pods, alerts
- [ ] Sub-score display shows 3 cards instead of 4
- [ ] Score calculation is more meaningful (no constant 100% component)
- [ ] `deno fmt && deno lint` pass

---

## Execution Order

```
Task 3 (CSS FOUC) ─── can ship independently, zero risk
Task 4 (Health score) ─── frontend only, 5 lines
Task 2 (Batch counts) ─── independent of Task 1
Task 1 (Dashboard summary) ─── largest, depends on nothing
```

Tasks 3 and 4 can be done first as quick wins. Tasks 1 and 2 are independent backend+frontend work.

## References

- Phase 6 review findings: 16 duplicate API calls, Prometheus blocking risk
- Backend patterns: `backend/internal/k8s/resources/handler.go`, `routes.go`, `informers.go`
- Prometheus client: `backend/internal/monitoring/prometheus.go` (10s timeout)
- Response envelope: `backend/pkg/api/types.go`
