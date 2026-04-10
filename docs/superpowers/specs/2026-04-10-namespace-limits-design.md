# Namespace Limits Design Spec

**Date:** 2026-04-10
**Revised:** 2026-04-10 (incorporated review findings)
**Status:** Approved
**Roadmap Position:** #4 (after Notification Center, Git commit display, Diff view)

---

## Summary

Admin-first feature for managing namespace resource limits. Combines ResourceQuota (aggregate caps) and LimitRange (per-object defaults/bounds) into a unified "Namespace Limits" surface with dashboard, tiered wizard, and Notification Center integration for overage warnings.

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Primary audience | Admin-first | Quota management is an operator concern |
| ResourceQuota + LimitRange | Unified surface | Admins think "namespace limits" not two objects |
| Package name | `limits` | Matches feature name, consistent with `policy/`, `gitops/` |
| Architecture | Handler-only (no Service) | Matches codebase patterns where business logic lives in Handler |
| Caching | Singleflight + 30s TTL | Prevents thundering herd on dashboard |
| Wizard complexity | Tiered (presets + advanced toggle) | 4 steps, "Show advanced" reveals power options |
| Detail view | Slide-out panel | No separate route, consistent UX pattern |
| Alerting approach | Hybrid (poll + dashboard) | 5-min polling for notifications; dashboard shows real-time |
| Thresholds | Global defaults (80/95) + annotation overrides | Simple default, flexible when needed |
| Multi-cluster | Dashboard via ClusterRouter; checker local-only | Remote clusters run their own k8sCenter |

---

## Architecture

New `internal/limits/` package with:
- **Handler:** HTTP endpoints + business logic + singleflight caching
- **Checker:** background goroutine (5-min interval) dispatching to Notification Center

No new database tables — thresholds stored as annotations on ResourceQuota objects:
- `k8scenter.io/warn-threshold`
- `k8scenter.io/critical-threshold`

---

## API Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/limits/status` | Discovery (are quotas present?) |
| GET | `/api/v1/limits/namespaces` | Dashboard data (all namespaces) |
| GET | `/api/v1/limits/namespaces/{ns}` | Detail data (one namespace) |

All endpoints RBAC-filtered to namespaces user can access.

---

## Types

**Backend uses normalized types** (not raw `corev1.LimitRange`):
- `NormalizedQuota` — name, utilization map, thresholds
- `NormalizedLimitRange` — name, limits array with type/default/min/max
- `ResourceUtilization` — used, hard, percentage, status

**Wizard input uses nested structs:**
- `NamespaceLimitsInput` → `QuotaConfig` + `LimitConfig`
- `QuotaConfig` — CPU/memory/pods + optional count limits, GPU, thresholds
- `LimitConfig` — container defaults + optional pod/PVC limits

---

## Frontend Components

**Dashboard (`/platform/namespace-limits`):**
- Summary cards (total, warning, critical, no quota)
- Filterable/sortable table with utilization bars
- Click row → slide-out panel with namespace detail
- Create button → wizard

**Wizard (4 steps):**
1. Namespace + preset selection
2. Quota values (CPU/memory/pods) + "Show advanced" toggle
3. LimitRange values (container defaults) + "Show advanced" toggle
4. YAML preview + apply

---

## Notification Integration

Event type: `limits.threshold_crossed`

Payload includes namespace, quota name, resource, status (warning/critical), used/hard values.

Deduplication: only dispatch when status changes (OK→Warning, Warning→Critical, or recovery).

State key format: `namespace:quotaName:resource` (colon delimiter per codebase convention).

---

## Implementation Summary

| Metric | Value |
|--------|-------|
| New files | 12 |
| Modified files | 6 |
| Steps | 7 |
| Wizard steps | 4 |
| Estimated PRs | 3-4 |

---

## References

- Implementation plan: `plans/namespace-limits.md`
- Related: Notification Center (PR #162), Cost Analysis (Step 30)
- Pattern references: `internal/policy/handler.go`, `internal/gitops/handler.go`
