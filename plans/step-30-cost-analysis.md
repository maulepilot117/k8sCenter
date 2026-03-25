# Step 30: Cost Analysis & Resource Recommendations (Post-Review, 3 Items)

## Overview

Add resource utilization visibility: cluster-wide utilization on the dashboard, resource requests/limits on workload overviews, and request-vs-actual comparison in PerformancePanel. The final step of Phase 5.

## Review Feedback Applied

- Cut the full cost analysis page (DHH — "YAGNI for homelab, items 1-3 deliver the value")
- Dashboard utilization: fire queries unconditionally, graceful 503 handling (Kieran)
- Fix MiniChart linearGradient id collision before adding multi-line charts (Kieran)
- Watch pod selectors per workload type for PerformancePanel queries (DHH)

## Implementation Plan (3 items)

### 1. Dashboard Cluster Utilization Cards

**File: `frontend/islands/Dashboard.tsx`**

Add 2 utilization percentage cards below existing count cards. Fire PromQL queries unconditionally — if Prometheus is unavailable, the queries return errors and the cards don't render (same graceful degradation as PerformancePanel).

PromQL:
```promql
# CPU utilization %
100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)

# Memory utilization %
100 * (1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes))
```

Use instant queries via `/v1/monitoring/query`. Add to the existing `Promise.allSettled` batch. Render as small gauge-style cards (percentage + colored bar).

### 2. Resource Requests/Limits in Workload Overviews

**Files:**
- `frontend/components/k8s/detail/DeploymentOverview.tsx`
- `frontend/components/k8s/detail/StatefulSetOverview.tsx`
- `frontend/components/k8s/detail/DaemonSetOverview.tsx`
- `frontend/components/k8s/detail/PodOverview.tsx`

No Prometheus needed — read `spec.template.spec.containers[*].resources` from the existing k8s API response.

Add a "Resources" section with a per-container table:

| Container | CPU Request | CPU Limit | Memory Request | Memory Limit |
|---|---|---|---|---|
| nginx | 100m | 500m | 128Mi | 256Mi |

Extract a shared `ContainerResourcesTable` component in `frontend/components/k8s/detail/` to avoid duplication across the 4 overview files.

### 3. Request-vs-Actual Lines in PerformancePanel

**File: `frontend/islands/PerformancePanel.tsx`**

Add CPU/Memory request queries to workload query sets:

```typescript
// deployments, statefulsets, daemonsets:
{
  title: "CPU Request (cores)",
  query: 'sum(kube_pod_container_resource_requests{namespace="{namespace}",pod=~"{name}-.*",resource="cpu"})',
},
{
  title: "Memory Request (MB)",
  query: 'sum(kube_pod_container_resource_requests{namespace="{namespace}",pod=~"{name}-.*",resource="memory"}) / 1024 / 1024',
},
```

**Fix MiniChart linearGradient id collision:**
The `id="gradient"` on the SVG `<linearGradient>` is shared across all chart instances on the same page. Each chart's gradient overwrites the previous one. Fix by making the id unique per chart:

```tsx
// In MiniChart — use the chart index or title as a suffix
const gradientId = `gradient-${title.replace(/\s+/g, "-").toLowerCase()}`;
```

Update `fill="url(#gradient)"` references to use the dynamic id.

---

## Acceptance Criteria

- [ ] Dashboard shows CPU + Memory utilization % cards (graceful when Prometheus unavailable)
- [ ] DeploymentOverview shows resource requests/limits per container
- [ ] StatefulSetOverview shows resource requests/limits per container
- [ ] DaemonSetOverview shows resource requests/limits per container
- [ ] PodOverview shows resource requests/limits per container
- [ ] Shared `ContainerResourcesTable` component (no duplication)
- [ ] PerformancePanel shows CPU/Memory request lines for workloads
- [ ] MiniChart linearGradient id collision fixed
- [ ] `deno task build` passes

## Implementation Order

1. ContainerResourcesTable + wire into 4 overview components (pure data, no Prometheus)
2. Fix MiniChart gradient id collision (prerequisite for multi-line charts)
3. Request-vs-actual lines in PerformancePanel
4. Dashboard utilization cards

## References

- Dashboard: `frontend/islands/Dashboard.tsx`
- PerformancePanel: `frontend/islands/PerformancePanel.tsx` (QUERIES map, MiniChart)
- DeploymentOverview: `frontend/components/k8s/detail/DeploymentOverview.tsx`
- StatefulSetOverview: `frontend/components/k8s/detail/StatefulSetOverview.tsx`
- DaemonSetOverview: `frontend/components/k8s/detail/DaemonSetOverview.tsx`
- PodOverview: `frontend/components/k8s/detail/PodOverview.tsx`
