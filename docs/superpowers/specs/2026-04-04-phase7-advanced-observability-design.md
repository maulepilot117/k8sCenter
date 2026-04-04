# Phase 7: Advanced Observability

**Date:** 2026-04-04
**Status:** Approved
**Author:** Chris White + Claude

## Vision

Transform k8sCenter from "I can see metrics and dashboards" into "I can diagnose any problem end-to-end without leaving the platform." Leverages existing Loki + Prometheus infrastructure. Adds no new external dependencies.

## Current State

The homelab cluster runs:

| Component | Version | Status |
|-----------|---------|--------|
| Prometheus | kube-prometheus-stack v82.10.1 | Running |
| Grafana | Bundled with kube-prometheus-stack | Running |
| Alertmanager | Bundled | Running |
| Loki | v3.5.3 | Running |
| Promtail | v3.5.1 (3 nodes + syslog) | Running |
| Blackbox Exporter | v0.28.0 | Running |
| Distributed Tracing | — | Not installed |

k8sCenter currently integrates with Prometheus (PromQL proxy, query templates) and Grafana (dashboard provisioning, iframe embedding). It has no Loki integration, no resource dependency visualization, and no automated diagnostics.

## Components

### 1. Log Explorer

Loki-powered log search, filter, and live tail integrated into k8sCenter.

#### Features

- **Structured filters** — namespace, pod, container, severity dropdowns populated from Loki label values. All filters compose into LogQL.
- **Dual mode** — simple text search (auto-generates LogQL) for most users. Raw LogQL input for power users. Toggle between modes. Query history saved per-user.
- **Live tail** — WebSocket-backed real-time log streaming via Loki's tail API. Auto-scroll with pause-on-scroll-up. Rate limiting (100 lines/sec cap) to prevent browser overload.
- **Log volume histogram** — sparkline bar chart showing log volume over selected time range. Error spikes highlighted. Click a bar to zoom into that time window.
- **Context links** — click pod name to jump to pod detail. Click timestamp to see events around that time. Click "Investigate" to open diagnostic workspace pre-scoped to that pod.
- **Contextual entry points** — every resource detail page gets a "Logs" tab (when Loki is available) that opens the Log Explorer pre-filtered to that resource.

#### Backend

**Package:** `backend/internal/loki/`

- **Discovery** — same pattern as `monitoring/discovery.go`: scan for Loki service in monitoring namespace by known labels (`app.kubernetes.io/name=loki`), with manual URL override in settings. Re-check interval: 5 minutes.
- **Client** — HTTP client to Loki's `/loki/api/v1/{query_range,query,labels,label/.../values,tail}` endpoints. 30s default timeout, configurable.
- **Proxy handler** — validates and forwards LogQL queries. Enforces namespace scoping based on RBAC (users can only query logs for namespaces they have access to). Query size limit: 4096 chars (matching Prometheus).
- **WebSocket tail** — `/ws/logs-search` endpoint. Connects to Loki tail API server-side, fans out to client. Same auth pattern as existing `/ws/logs/*` but with LogQL instead of k8s API.
- **RBAC integration** — log queries are namespace-scoped. Backend injects namespace filter into LogQL based on user's permitted namespaces. Cluster-scoped logs (e.g., node-level) require admin role.

#### API Endpoints

```
GET  /api/v1/logs/status                — Loki discovery status (detected, URL, version)
GET  /api/v1/logs/query                 — Historical log search (LogQL, time range, limit)
GET  /api/v1/logs/labels                — Available label names
GET  /api/v1/logs/labels/{name}/values  — Label values (for filter dropdowns)
GET  /api/v1/logs/volume                — Log volume over time (for histogram)
WS   /ws/logs-search                    — Live tail via WebSocket (LogQL filter)
```

---

### 2. Resource Dependency Graph

Interactive DAG showing how resources relate and where failures propagate.

#### Features

- **Click a node** — opens slide-out panel with resource summary, conditions, recent events, and quick actions (view detail, view logs, investigate). No page navigation.
- **Hover highlights** — hovering a node highlights upstream and downstream dependencies. Dims unrelated nodes. Shows edge labels.
- **Health propagation** — failing pods turn their ReplicaSet red, Deployment red, Service red, Ingress amber. Visual cascade shows impact at a glance. Pulsing animation on actively failing nodes.
- **Scope controls** — filter by namespace, filter by resource kind. "Focus mode": select a resource to see only its dependency tree.
- **Zoom and pan** — SVG viewBox-based zoom (not CSS transform). Mouse wheel zoom, click-drag pan. Minimap for large graphs. Fit-to-view button.
- **Real-time updates** — WebSocket events update node status live. New/deleted resources animate in/out. Incremental DOM patches via existing resource subscription system.
- **Progressive disclosure** — start collapsed (Ingress, Service, Deployment level). Expand deeper levels on click. Deployments show pod count as badge; click to expand individual pods.

#### Edge Types

| Type | Example | Line Style |
|------|---------|-----------|
| Owner Reference | Deployment→RS, RS→Pod, Job→Pod, CronJob→Job | Solid |
| Selector Match | Service→Pods, NetworkPolicy→Pods, HPA→target | Dashed |
| Volume/Config Mount | Pod→PVC, Pod→ConfigMap, Pod→Secret | Dotted |
| Ingress Rule | Ingress→Service | Dashed |
| Service Account | Pod→ServiceAccount→RoleBinding→Role | Dotted (optional layer) |
| PVC Binding | PVC→PersistentVolume | Solid (cross-namespace) |

#### Backend

**Package:** `backend/internal/topology/`

- **Graph builder** — walks informer cache, builds adjacency list from: owner references, service selector→pods, ingress rules→services, volume mounts→PVC/ConfigMap/Secret, HPA→scale target.
- **Health aggregator** — computes per-node health from conditions, phase, ready containers, restart count. Propagates upstream: any failing child degrades parent.
- **Namespace scoping** — graph is always namespace-scoped (with optional cross-namespace for cluster-scoped resources like PVs). RBAC-filtered: nodes the user can't access are omitted.
- **Caching** — graph recomputed on informer events (debounced 2s). Cached per-namespace. Invalidated on resource add/update/delete.
- **Response format** — JSON with `nodes[]` (kind, name, namespace, health, summary) and `edges[]` (source, target, type). Frontend handles layout with left-to-right DAG algorithm.

#### API Endpoints

```
GET  /api/v1/topology/{namespace}                    — Full dependency graph for namespace
GET  /api/v1/topology/{namespace}/{kind}/{name}      — Focused graph (single resource + its tree)
GET  /api/v1/topology/{namespace}?summary=true         — Health summary only (for dashboard widgets)
```

---

### 3. Diagnostic Workspace

Start from a symptom. The system helps you find the cause.

#### Features

- **Diagnostic checklist** — automated checks run against a target resource. Each check reports Pass, Warn, or Fail with a human-readable message and optional remediation suggestion. Failed checks at top with details and action links. Passed checks collapsed by default.
- **Blast radius** — uses topology graph to trace upstream and downstream affected resources. Annotates each with impact description. Two tiers: "directly affected" and "potentially affected (upstream)."
- **Recent events** — contextual event stream for the target resource and its dependencies.
- **Save investigation** — snapshot diagnostic results, blast radius, and events to PostgreSQL. Lightweight history for review and sharing.
- **Multiple entry points:**
  - Dedicated page: `/observability/investigate`
  - Resource detail pages: "Investigate" button appears when resource has warnings/errors
  - Dependency graph: click failing node, then "Investigate" in slide-out
  - Dashboard: health score widget links to investigation for degraded resources
  - Command palette: "Investigate [resource]" action

#### Diagnostic Rules (Initial Set — 14 Rules)

**Critical:**
- CrashLoopBackOff — pod restarting repeatedly, exit code analysis
- ImagePullBackOff — image doesn't exist or no pull secret
- OOMKilled — container exceeded memory limit
- Pending Pod — unschedulable (insufficient resources, taints, affinity)

**Warning:**
- Probe Failures — liveness/readiness probe failing
- Resource Pressure — CPU/memory near limits (>90%)
- Pending PVC — volume claim not bound
- Node Pressure — target node has DiskPressure/MemoryPressure
- Replica Mismatch — desired != available replicas
- Stale ReplicaSet — rollout stuck, old RS not scaled down

**Info:**
- Network Policy — check egress/ingress rules for blocked traffic
- Service Endpoints — service has zero or reduced endpoints
- ConfigMap/Secret — referenced config exists, mounted correctly
- HPA Status — at max replicas, unable to scale, metrics unavailable

#### Backend

**Package:** `backend/internal/diagnostics/`

- **Rule interface** — `type Rule interface { Name() string; Check(ctx context.Context, resource Resource) Result }`. Each rule is a self-contained Go file. Results: Pass, Warn, Fail + human-readable message + optional remediation.
- **Rule runner** — executes all applicable rules for a resource kind in parallel (bounded goroutine pool). Aggregates results into a checklist response.
- **Blast radius** — uses topology graph to find upstream/downstream affected resources. Annotates each with impact description.
- **Resource resolver** — given a target resource, fetches it + all related resources (pods, events, conditions) in parallel. Single API call to frontend.

**Package:** `backend/internal/investigation/`

- **Investigation persistence** — PostgreSQL table: `id`, `user_id`, `cluster_id`, `namespace`, `kind`, `name`, `results_json`, `created_at`. Lightweight snapshot for history.

#### API Endpoints

```
GET    /api/v1/diagnostics/{namespace}/{kind}/{name}  — Run diagnostics + blast radius
GET    /api/v1/diagnostics/{namespace}/summary         — Namespace-wide health summary
POST   /api/v1/investigations                          — Save investigation snapshot
GET    /api/v1/investigations                          — List saved investigations
GET    /api/v1/investigations/{id}                     — Get saved investigation
DELETE /api/v1/investigations/{id}                     — Delete investigation
```

---

### 4. Unified Timeline

Chronological merge of events, logs, alerts, and audit entries for a resource or namespace.

#### Features

- **Multi-source merge** — Kubernetes events (informer cache), log entries (Loki, sampled), alert state changes (Alertmanager webhook history), resource mutations (audit log).
- **Filter by source type** — toggle events / logs / alerts / changes independently.
- **Time range selector** — with zoom. Click any entry to jump to its source (event detail, log explorer pre-filtered, alert detail).
- **Auto-scroll** — with "new entries" indicator when paused.

#### Backend

**Package:** `backend/internal/timeline/`

- **Multi-source merger** — queries events, logs, alerts, and audit entries in parallel. Deduplicates, sorts chronologically, returns unified response.
- **Read-only aggregation** — no new data store. Queries existing sources on demand. No background jobs, no new tables.

#### API Endpoint

```
GET  /api/v1/timeline/{namespace}[/{kind}/{name}]  — Merged timeline (sources, start, end, limit params)
```

---

### 5. Navigation Integration

The current "Monitoring" and "Alerting" sections consolidate into a single "Observability" domain section.

#### New Sub-tabs

| Tab | Route | Source |
|-----|-------|--------|
| Overview | `/observability` | Enhanced monitoring overview with health summary |
| Log Explorer | `/observability/logs` | New |
| Topology | `/observability/topology` | New |
| Investigate | `/observability/investigate` | New |
| Timeline | `/observability/timeline` | New |
| Dashboards | `/observability/dashboards` | Moved from Monitoring |
| Alerts | `/observability/alerts` | Moved from Alerting section |
| Prometheus | `/observability/prometheus` | Moved from Monitoring |

This frees one icon rail slot (Alerting) for future phases.

---

## New Backend Packages

| Package | Purpose |
|---------|---------|
| `internal/loki/` | Loki client, discovery, LogQL proxy, RBAC namespace injection |
| `internal/topology/` | Graph builder from informer cache, health aggregation, caching |
| `internal/diagnostics/` | Rule interface, rule runner, blast radius, resource resolver |
| `internal/timeline/` | Multi-source merge and deduplication |
| `internal/investigation/` | PostgreSQL CRUD for saved investigations |

## New API Surface Summary

| Group | Endpoints | Type |
|-------|-----------|------|
| `/api/v1/logs/*` | 5 | HTTP |
| `/ws/logs-search` | 1 | WebSocket |
| `/api/v1/topology/*` | 3 | HTTP |
| `/api/v1/diagnostics/*` | 2 | HTTP |
| `/api/v1/investigations/*` | 4 | HTTP |
| `/api/v1/timeline/*` | 1 | HTTP |
| **Total** | **16** | |

## New Frontend Pages and Islands

**Pages:**
- `/observability/logs` — Log Explorer
- `/observability/topology` — Dependency Graph
- `/observability/investigate` — Diagnostic Workspace
- `/observability/timeline` — Timeline
- `/observability/alerts` — Alerts (moved)
- `/observability/dashboards` — Dashboards (moved)
- `/observability/prometheus` — Prometheus (moved)

**New Islands:**
- `LogExplorer` — search, filter, results, live tail
- `LogVolumeHistogram` — sparkline volume chart
- `DependencyGraph` — interactive SVG DAG with zoom/pan
- `DiagnosticWorkspace` — checklist + blast radius + events
- `Timeline` — multi-source chronological view
- `InvestigationList` — saved investigations browser

**Enhanced Existing:**
- Resource detail pages — "Logs" tab, "Investigate" button
- Dashboard — health score links to investigation
- Command palette — "Investigate [resource]" action
- Icon rail — "Observability" replaces "Monitoring" + "Alerting"

## Graceful Degradation

- **Loki not detected** — Log Explorer shows "Loki not detected" with setup instructions. Timeline omits log entries. Diagnostics and topology work fully without Loki.
- **Prometheus not detected** — Timeline omits alert entries. Resource pressure diagnostic checks skip metrics. Everything else works.
- **Remote clusters** — topology and diagnostics use direct API calls (no informer cache). Log queries require Loki to be accessible from k8sCenter backend.

## Database Migrations

One new table:

```sql
CREATE TABLE investigations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    cluster_id  TEXT NOT NULL DEFAULT 'local',
    namespace   TEXT NOT NULL,
    kind        TEXT NOT NULL,
    name        TEXT NOT NULL,
    results     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_investigations_user ON investigations(user_id, created_at DESC);
CREATE INDEX idx_investigations_resource ON investigations(cluster_id, namespace, kind, name);
```

## Out of Scope

- Distributed tracing (Tempo/Jaeger) — candidate for Phase 7B after this foundation is solid
- AI/ML analysis — deferred to a future phase
- Service mesh integration
- Custom diagnostic rule editor UI — rules are Go code, added by developers
- Log-based alerting — Loki ruler is a separate concern

## Future Phase Roadmap

- **Phase 8: Policy and Governance** — OPA/Kyverno integration, compliance dashboards, admission controller visualization
- **Phase 9: GitOps** — Argo CD/Flux integration, sync status, drift detection, rollback history
