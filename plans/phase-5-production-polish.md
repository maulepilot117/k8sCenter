# Phase 5: Production Polish & Testing

## Overview

With all core features complete (Phases 1-4), Phase 5 focuses on production readiness, automated testing, UX refinements, and advanced operational features. Each step produces a shippable increment.

---

## Build Order

| Step | Name | Depends On | Effort | Status |
|------|------|------------|--------|--------|
| 24 | E2E Tests (Playwright) | — | Large | Done (100 tests, 10 files) |
| 25 | Production Hardening | — | Medium | Done |
| 26 | UX Polish | — | Medium | Done |
| 27 | Grafana Dashboard Provisioning | — | Small | Done |
| 28 | Multi-Cluster UX | — | Large | Planned |
| 29 | RBAC Visualization | — | Medium | Planned |
| 30 | Cost Analysis & Resource Recommendations | 27 | Large | Planned |

---

## Step 24: E2E Tests (Playwright)

End-to-end browser tests using Playwright against a real kind cluster. Cover the full login → browse → create → verify → delete cycle for all major workflows. Run in CI via GitHub Actions.

**Key flows to test:**
- Setup wizard (first admin creation)
- Login/logout/session refresh
- Dashboard loads with real cluster data
- Resource table browsing (all resource types)
- Resource detail pages (click-through, tabs, YAML view)
- Wizard flows (Deployment, Service, ConfigMap, Secret, Ingress, etc.)
- YAML apply/validate/diff
- Namespace selector
- Search and sort
- Dark mode toggle
- WebSocket live updates (create resource, verify table updates)
- Alert banner
- User management (admin)
- Settings pages

---

## Step 25: Production Hardening

- TLS with cert-manager Certificate resources in Helm chart
- Pod Security Admission (restricted profile enforcement)
- Resource limits/requests tuning based on real usage data
- Readiness/liveness probe tuning
- Graceful shutdown improvements
- Rate limiting tuning for production traffic
- Database connection pool tuning
- Helm chart: PodDisruptionBudget for HA deployments (replicas > 1)
- Security scanning (Trivy in CI for container images)
- SBOM generation
- CI gating: make E2E a required check on PRs to main (after Step 24 Phase 2 adds full coverage); add `@smoke` E2E gate on image builds (fast ~15s subset)

---

## Step 26: UX Polish

- Breadcrumb navigation (Cluster > Namespace > Resource Type > Name)
- Resource relationship drill-down (Deployment → ReplicaSet → Pods)
- Global search across all resource types
- Keyboard navigation improvements (j/k for table rows, Enter to open)
- Loading state improvements (skeleton screens for all pages)
- Error boundary improvements (retry buttons, better error messages)
- Mobile responsive layout improvements
- Favorites/pinned resources
- Recent resources history

---

## Step 27: Grafana Dashboard Provisioning

- Expand Helm ConfigMap-based dashboard provisioning
- Add dashboards for: cluster overview, namespace overview, node fleet, storage health
- Dashboard JSON parameterized with Helm template variables
- Auto-provision dashboards into existing Grafana via API on startup
- Dashboard version management (update on upgrade)

---

## Step 28: Multi-Cluster UX

- Cross-cluster resource comparison views
- Cluster health dashboard (all clusters at a glance)
- Cross-cluster search
- Cluster group tags/labels
- Federated namespace view
- Cluster connectivity status monitoring
- Bulk operations across clusters

---

## Step 29: RBAC Visualization

- Who-can-access-what tree view per namespace
- Role → RoleBinding → Subject relationship graph
- Policy simulation ("Can user X do Y in namespace Z?")
- Effective permissions calculator
- RBAC audit report (overprivileged accounts, unused roles)
- Visual diff between roles

---

## Step 30: Cost Analysis & Resource Recommendations

- Resource utilization tracking over time (CPU/memory request vs actual)
- Right-sizing recommendations (over/under-provisioned workloads)
- Namespace cost allocation (based on resource consumption)
- Idle resource detection (deployments with 0 traffic, unused PVCs)
- Resource request/limit suggestions based on P95 usage
- Cost trends and forecasting
