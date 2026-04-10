# Namespace Limits Design Spec

**Date:** 2026-04-10
**Status:** Approved
**Roadmap Position:** #4 (after Notification Center, Git commit display, Diff view)

---

## Summary

Unified admin tool for managing ResourceQuota and LimitRange objects. Combines visualization (dashboard with utilization bars), authoring (tiered wizard with presets), and alerting (background checker with Notification Center integration).

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Primary audience | Admin-first | Quota management is an operator concern; developers see read-only info |
| ResourceQuota + LimitRange | Unified surface | Admins think "namespace limits" not two separate objects |
| Wizard complexity | Tiered (presets + advanced) | 80% of use cases covered by presets; power users get full access |
| Alerting approach | Hybrid (poll + dashboard) | Poll-based notifications catch drift; dashboard shows current state |
| Thresholds | Global defaults (80/95) + annotation overrides | Simple default, flexible when needed |
| Multi-cluster | Dashboard/detail via ClusterRouter; checker local-only | Remote clusters run their own k8sCenter |

---

## Architecture

New `internal/quota/` package with:
- **Service:** aggregation, utilization calculation, threshold checking
- **Handler:** HTTP endpoints for dashboard/detail data
- **Checker:** background goroutine (5-min interval) dispatching to Notification Center

No new database tables — thresholds stored as annotations on ResourceQuota objects:
- `k8scenter.io/warn-threshold`
- `k8scenter.io/critical-threshold`

---

## API Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/quota/summary` | Dashboard data (all namespaces) |
| GET | `/api/v1/quota/{namespace}` | Detail data (one namespace) |

Both endpoints RBAC-filtered to namespaces user can access.

---

## Frontend Components

**Dashboard (`/platform/namespace-limits`):**
- Summary cards (total, warning, critical, no quota)
- Filterable/sortable table with utilization bars
- Create button → wizard

**Detail (`/platform/namespace-limits/[namespace]`):**
- Quotas section with utilization bars and threshold markers
- LimitRanges section with defaults/bounds table
- Edit/Delete actions

**Wizard:**
- 6 steps: namespace → quota template → advanced quota → container limits → advanced limits → review
- Presets: Small (2 CPU/4Gi), Standard (8 CPU/16Gi), Large (32 CPU/64Gi)
- Creates both ResourceQuota and LimitRange via server-side apply

---

## Notification Integration

Event type: `quota.threshold_crossed`

Payload includes namespace, quota name, resource, status (warning/critical), used/hard values.

Deduplication: only dispatch when status changes (OK→Warning, Warning→Critical, or recovery).

---

## References

- Implementation plan: `plans/namespace-limits.md`
- Related: Notification Center (PR #162), Cost Analysis (Step 30)
