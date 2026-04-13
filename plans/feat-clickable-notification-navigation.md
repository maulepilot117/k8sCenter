# feat: Clickable Notification Navigation

> Click any notification to navigate to its source page (policy, GitOps, alerts, diagnostics, certificates, etc.)

## Overview

Notifications in the Notification Center currently lack reliable click-to-navigate behavior. Only 4 of 10 notification sources populate the resource fields needed for navigation, and the existing `resourceHref()` utility only maps standard Kubernetes kinds — not policy, GitOps, cert-manager, or other custom resource types. This feature adds a dedicated URL-resolution function on the frontend that maps every notification source to its correct destination page.

## Problem Statement

1. **Most notifications are not clickable.** Only `alert`, `diagnostic`, `limits`, and `certmanager/poller` populate `ResourceKind/NS/Name`. The other 6 sources emit notifications with no resource metadata, so `resourceHref()` returns `null` and nothing happens on click.
2. **Even populated notifications route wrong.** Diagnostic notifications go to the k8s resource detail page instead of `/observability/investigate`. Limits notifications go to `/config/resourcequotas/...` instead of `/governance/limits/namespaces/...`. CertManager uses sentinel kinds like `"certificate.expiring"` which `resourceHref()` can't resolve.
3. **Database CHECK constraint is broken.** The `nc_notifications.source` CHECK only allows 7 values — `limits`, `velero`, and `certmanager` notifications silently fail on INSERT. (Tracked in Phase B.)
4. **Frontend type drift.** `NotifSource` union type lists only 7 of 10 sources.

## Proposed Solution

**Frontend-computed action URLs** — no new database column. Add a `notifActionUrl(n: AppNotification): string | null` function that uses an exhaustive `switch` on `n.source` to compute the correct destination. No migration for the URL field, no staleness risk, routing logic lives alongside the frontend router.

## Navigation Map

| Source | Destination | URL Pattern | Resource fields needed |
|---|---|---|---|
| `alert` | Alerts page | `/alerting` | None (source-level link) |
| `policy` | Violations page | `/security/violations` | None (source-level link) |
| `gitops` | GitOps apps list | `/gitops/applications` | None (composite ID not available at emit time) |
| `diagnostic` | Investigate page | `/observability/investigate?namespace={ns}&kind={kind}&name={name}` | ResourceKind, ResourceNS, ResourceName |
| `scan` | Security scanning | `/security/scanning` | None |
| `cluster` | Cluster management | `/admin/clusters` | None (admin-only) |
| `audit` | Audit log | `/admin/audit` | None (admin-only) |
| `limits` | Limits dashboard | `/governance/limits/namespaces/{namespace}` | ResourceNS |
| `velero` | Backups page | `/governance/backups` | None |
| `certmanager` | Certificate detail | `/security/certificates/{ns}/{name}` | ResourceNS, ResourceName |

**Fallback:** If `notifActionUrl` returns `null` (missing required fields), the notification remains non-clickable (current behavior).

---

## Phase A: Frontend — Clickable Navigation (5 files)

This is the core feature. No backend changes required.

### A1. Extend NotifSource union + labels + badge colors

**File: `frontend/lib/notif-center-types.ts`**

```typescript
export type NotifSource =
  | "alert" | "policy" | "gitops" | "diagnostic"
  | "scan" | "cluster" | "audit"
  | "limits" | "velero" | "certmanager";
```

Update `NOTIF_SOURCES` array and `SOURCE_LABELS` record (typed `Record<NotifSource, string>` — compiler enforces completeness).

**File: `frontend/components/ui/NotifCenterBadges.tsx`**

Add color mappings for `limits`, `velero`, `certmanager` in `SourceBadge`.

### A2. Create the URL resolver

**File: `frontend/lib/notif-action.ts`** (new)

```typescript
import type { AppNotification, NotifSource } from "./notif-center-types.ts";

export function notifActionUrl(n: AppNotification): string | null {
  switch (n.source) {
    case "alert":
      return "/alerting";
    case "policy":
      return "/security/violations";
    case "gitops":
      return "/gitops/applications";
    case "diagnostic":
      if (n.resourceNamespace && n.resourceKind && n.resourceName) {
        return `/observability/investigate?namespace=${encodeURIComponent(n.resourceNamespace)}&kind=${encodeURIComponent(n.resourceKind)}&name=${encodeURIComponent(n.resourceName)}`;
      }
      return "/observability/investigate";
    case "scan":
      return "/security/scanning";
    case "cluster":
      return "/admin/clusters";
    case "audit":
      return "/admin/audit";
    case "limits":
      if (n.resourceNamespace) {
        return `/governance/limits/namespaces/${encodeURIComponent(n.resourceNamespace)}`;
      }
      return "/governance/limits";
    case "velero":
      return "/governance/backups";
    case "certmanager":
      if (n.resourceNamespace && n.resourceName) {
        return `/security/certificates/${encodeURIComponent(n.resourceNamespace)}/${encodeURIComponent(n.resourceName)}`;
      }
      return "/security/certificates";
    default: {
      const _exhaustive: never = n.source;
      return null;
    }
  }
}
```

Note: exhaustive `never` check ensures adding a new `NotifSource` variant is a compile error, not a silent fallthrough.

### A3. Replace resourceHref in notification islands

**File: `frontend/islands/NotificationBell.tsx`**

- Replace `resourceHref(n.resourceKind, n.resourceNamespace, n.resourceName)` with `notifActionUrl(n)`
- Fire-and-forget `markRead` (drop the `await`, add `keepalive: true` to fetch options) then navigate immediately — awaiting markRead before `location.href` assignment creates unnecessary delay

**File: `frontend/islands/NotificationFeed.tsx`**

- Same `notifActionUrl` replacement in `handleRowClick` and the `href` render variable
- Update `cursor: pointer` conditional to use `notifActionUrl(n) !== null`
- Same fire-and-forget `markRead` with `keepalive: true`

### A4. Visual affordance (inline, no extra files)

All navigable notification rows:
- `cursor: pointer` + accent-colored title text (already partially implemented)
- Subtle hover background using `var(--surface-hover)`

Non-navigable notifications: `cursor: default`, muted title.

### Phase A Files

| File | Change |
|---|---|
| `frontend/lib/notif-center-types.ts` | Extend NotifSource union, NOTIF_SOURCES, SOURCE_LABELS |
| `frontend/components/ui/NotifCenterBadges.tsx` | Add 3 source color mappings |
| `frontend/lib/notif-action.ts` (new) | `notifActionUrl()` function (~35 lines) |
| `frontend/islands/NotificationBell.tsx` | Replace resourceHref, fire-and-forget markRead |
| `frontend/islands/NotificationFeed.tsx` | Replace resourceHref, fire-and-forget markRead |

---

## Phase B: Backend Bug Fixes (separate PR)

These are pre-existing bugs that affect notification persistence and data quality. They do not block Phase A (clickable navigation works regardless) but should be fixed promptly.

### B1. Database migration — expand CHECK constraint

**File: `backend/internal/store/migrations/000010_expand_notification_sources.up.sql`** (new)

```sql
ALTER TABLE nc_notifications DROP CONSTRAINT IF EXISTS nc_notifications_source_check;
ALTER TABLE nc_notifications ADD CONSTRAINT nc_notifications_source_check
  CHECK (source IN ('alert','policy','gitops','diagnostic','scan','cluster','audit','limits','velero','certmanager'));
```

**File: `backend/internal/store/migrations/000010_expand_notification_sources.down.sql`** (new)

Drops and re-adds with original 7 values.

**Impact:** Without this, all `limits`, `velero`, and `certmanager` notifications are silently dropped on INSERT. This is a data loss bug independent of the navigation feature.

### B2. Fix certmanager poller ResourceKind sentinel

**File: `backend/internal/certmanager/poller.go`**

Change `ResourceKind` from `"certificate.expiring"` / `"certificate.expired"` to `"Certificate"`. The expiry status is already distinguished via the `Title` field. The sentinel value is not a valid Kubernetes kind and produces meaningless data in webhook payloads.

### Phase B Files

| File | Change |
|---|---|
| `backend/internal/store/migrations/000010_expand_notification_sources.up.sql` (new) | Expand CHECK constraint |
| `backend/internal/store/migrations/000010_expand_notification_sources.down.sql` (new) | Down migration |
| `backend/internal/certmanager/poller.go` | Fix ResourceKind to `"Certificate"` |

---

## Acceptance Criteria

### Phase A
- [ ] Every notification source has a defined click destination per the Navigation Map
- [ ] Clicking a notification in the bell dropdown navigates to the correct page
- [ ] Clicking a notification in the feed page navigates to the correct page
- [ ] markRead fires with `keepalive: true` (no await before navigation)
- [ ] Frontend `NotifSource` type includes all 10 sources with correct labels and badge colors
- [ ] Diagnostic notifications navigate to `/observability/investigate` with pre-filled query params
- [ ] CertManager notifications navigate to `/security/certificates/{ns}/{name}`
- [ ] Limits notifications navigate to `/governance/limits/namespaces/{namespace}`
- [ ] Non-navigable notifications (null URL) remain non-clickable with default cursor
- [ ] `deno fmt --check` passes on all frontend changes

### Phase B
- [ ] `limits`, `velero`, and `certmanager` notifications persist to the database
- [ ] CertManager poller emits `ResourceKind: "Certificate"` (not sentinel values)
- [ ] `go vet ./...` passes on backend changes

## Dependencies & Risks

- **Phase A has zero backend dependencies.** Can ship immediately.
- **Phase B should ship before or shortly after Phase A** to unblock limits/velero/certmanager notification persistence.
- **Existing notifications in DB have old certmanager ResourceKind values** (`"certificate.expiring"`). The `notifActionUrl` function routes by `source`, not `resourceKind`, so old notifications navigate correctly.
- **Admin-only destinations** (`/admin/clusters`, `/admin/audit`) will 403 for non-admin users. Consistent with how other admin links work throughout the app.

## Out of Scope

- Email digest per-notification links (requires base URL configuration)
- Slack/webhook payload URLs (same base URL problem)
- Per-violation policy deep links (requires per-violation notification emission)
- GitOps deep links to specific applications (requires composite ID at emit time)
- Client-side RBAC pre-check before navigation
- Admin-only destination icons (destination page handles 403)

## References

- Notification Center PR: #162
- Notification types: `backend/internal/notifications/types.go`
- Frontend notification islands: `frontend/islands/NotificationBell.tsx`, `frontend/islands/NotificationFeed.tsx`
- URL builder: `frontend/lib/k8s-links.ts` (existing `resourceHref`, supplemented not replaced)
- DB migration 000007: `backend/internal/store/migrations/000007_create_notification_center.up.sql`
