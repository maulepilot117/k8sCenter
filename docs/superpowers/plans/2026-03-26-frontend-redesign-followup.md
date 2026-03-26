# Frontend Redesign Follow-Up: Deferred Items

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Address deferred P2/P3 review findings from the Phase 6 Frontend Redesign that require backend work, design decisions, or significant refactoring.

**Architecture:** These items fall into two categories: (1) backend API optimizations to reduce frontend API call volume, and (2) frontend improvements that need design decisions first.

**Tech Stack:** Go (backend), Deno/Fresh/Preact (frontend)

---

## Task 1: Backend Dashboard Summary Endpoint

**Problem:** The overview dashboard fires 16 parallel API calls on mount, with nodes/pods/services fetched 3x each by DashboardV2, HealthScoreRing, and ClusterTopology. HealthScoreRing also polls full pod/node lists every 60s.

**Files:**
- Create: `backend/internal/server/routes_dashboard.go`
- Modify: `backend/internal/server/routes.go` (register new routes)
- Modify: `frontend/islands/DashboardV2.tsx`
- Modify: `frontend/islands/HealthScoreRing.tsx`

- [ ] **Step 1: Design the summary endpoint response**

```go
// GET /api/v1/cluster/dashboard-summary
type DashboardSummary struct {
    Nodes      NodeSummary      `json:"nodes"`
    Pods       PodSummary       `json:"pods"`
    Services   ServiceSummary   `json:"services"`
    Alerts     AlertSummary     `json:"alerts"`
    CPU        UtilizationInfo  `json:"cpu"`
    Memory     UtilizationInfo  `json:"memory"`
    Events     []K8sEvent       `json:"events"`
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

type ServiceSummary struct {
    Total int `json:"total"`
}

type AlertSummary struct {
    Active   int `json:"active"`
    Critical int `json:"critical"`
}

type UtilizationInfo struct {
    Percentage float64 `json:"percentage"`
    Used       string  `json:"used"`
    Total      string  `json:"total"`
    Requests   string  `json:"requests,omitempty"`
    Limits     string  `json:"limits,omitempty"`
}
```

- [ ] **Step 2: Implement the endpoint handler**
- [ ] **Step 3: Register the route**
- [ ] **Step 4: Update DashboardV2 to use single endpoint**
- [ ] **Step 5: Update HealthScoreRing to use summary data from parent**
- [ ] **Step 6: Test and commit**

---

## Task 2: Backend Batch Resource Counts Endpoint

**Problem:** SubNav fires 5-7 individual `?limit=1` API calls per domain page to get resource counts. Workloads page fires 7 count calls.

**Files:**
- Create: `backend/internal/server/routes_counts.go`
- Modify: `backend/internal/server/routes.go`
- Modify: `frontend/islands/SubNav.tsx`

- [ ] **Step 1: Design the counts endpoint**

```go
// GET /api/v1/resources/counts?kinds=deployments,pods,services&namespace=default
type ResourceCounts struct {
    Counts map[string]int `json:"counts"` // kind -> count
}
```

- [ ] **Step 2: Implement the handler (use informer cache for local cluster)**
- [ ] **Step 3: Update SubNav to use single batch call**
- [ ] **Step 4: Test and commit**

---

## Task 3: Fix FOUC for Non-Default Themes

**Problem:** When a user has selected e.g. Dracula theme, page loads with Nexus colors from `:root` CSS defaults until JS hydration runs `initTheme()`. This causes a visible flash.

**Approach options:**

**Option A: CSS-only theme rules (recommended)**
Add `[data-theme="dracula"] { --bg-base: #282A36; ... }` rules to `styles.css` for each theme. The inline script in `_app.tsx` already sets `data-theme` from localStorage, so CSS would apply immediately without JS.

**Option B: Expanded inline script**
Include all 7 theme color maps in the inline `<script>` tag and apply CSS variables directly. Adds ~2KB to every page load.

**Files:**
- Modify: `frontend/assets/styles.css`

- [ ] **Step 1: Generate CSS rules for each theme**

For each of the 7 themes, add a `[data-theme="themeid"]` rule block with all 20 CSS custom property overrides.

- [ ] **Step 2: Remove the JS-based `applyTheme` CSS variable setting (optional — can keep for dynamic switching)**
- [ ] **Step 3: Test theme persistence across page reload**
- [ ] **Step 4: Commit**

---

## Task 4: Health Score Services Sub-Score Redesign

**Problem:** The services sub-score is binary (100% if any services exist, 0% otherwise). Every cluster with the default `kubernetes` service scores 100%. This makes 15% of the health score meaningless.

**Design options:**
1. **Remove services from health score** — redistribute weight to nodes (35%), pods (35%), alerts (30%)
2. **Make services meaningful** — score based on endpoints readiness (services with no ready endpoints = degraded)
3. **Replace with utilization** — use CPU/memory utilization as the 4th dimension instead of services

- [ ] **Step 1: Choose approach (needs user input)**
- [ ] **Step 2: Implement chosen approach in `frontend/lib/health-score.ts`**
- [ ] **Step 3: Update HealthScoreRing if sub-score labels change**
- [ ] **Step 4: Test and commit**

---

## Prioritization

| Task | Impact | Effort | Priority |
|---|---|---|---|
| 1. Dashboard Summary Endpoint | High (16→1 API calls) | Medium (backend + frontend) | P1 |
| 2. Batch Counts Endpoint | Medium (7→1 per section) | Small (backend + frontend) | P2 |
| 3. FOUC Fix | Low (cosmetic, only non-default themes) | Small (CSS only) | P3 |
| 4. Health Score Redesign | Low (correctness, not functionality) | Small (frontend only) | P3 |
