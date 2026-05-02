---
name: Audit Detail leaks cross-tenant UIDs on ClusterSecretStore-scoped bulk refresh
status: completed
priority: p2
issue_id: 348
tags: [code-review, eso, phase-e, security, multi-tenant, audit-redaction]
dependencies: []
---

## Problem Statement

`auditBulkJob` writes a single audit row at job completion with full `failed[]` and `skipped[]` arrays in `Entry.Detail` JSON. For `action = refresh_cluster_store`, `ResourceNamespace` is empty and the Detail enumerates UIDs collected across **every namespace** the requesting user has `update` perm on.

Phase D explicitly suppresses `ResourceNS` / `ResourceName` in ESO **notifications** via the `SuppressResourceFields bool` field on `notifications.Notification`, with the rationale documented in CLAUDE.md:

> "Slack channels and webhook receivers don't honor in-app RBAC, so leaking namespace/name there would defeat the RBAC-generic title."

Phase E audit log applies no equivalent suppression — the audit-log viewer is admin-readable cluster-wide, but tenant-scoped compliance reports (or any future per-tenant audit export) would surface cross-namespace UIDs without a namespace anchor for filtering.

Plan §693 documents the audit row's role but doesn't address the cross-tenant case.

## Findings

- adversarial reviewer (adv-3 medium, conf 0.80)

**Affected files:**
- `backend/internal/externalsecrets/bulk.go:1110-1148` (auditBulkJob)
- compare against `internal/notifications/dispatch.go` for the SuppressResourceFields precedent
- `frontend/islands/AuditLogViewer.tsx` (consumer; renders Detail inline)

## Proposed Solutions

### Option A — UID-only Detail for cluster-scoped actions (recommended)

For `action == refresh_cluster_store`, strip the `failed[].uid` / `skipped[].uid` fields and replace with anonymized indices:

```go
if msg.Action == store.BulkRefreshActionClusterStore {
    detail["failed"] = anonymizeOutcomes(job.Failed)
    detail["skipped"] = anonymizeOutcomes(job.Skipped)
} else {
    detail["failed"] = job.Failed
    detail["skipped"] = job.Skipped
}
// anonymizeOutcomes returns []map[string]any with reasons but UIDs replaced by "[redacted]"
```

Aggregate counts + reason histograms remain visible (operationally useful), individual UIDs do not.

### Option B — namespace-scoped Detail enrichment

Group `failed[]` / `skipped[]` by namespace and only include groups for which the audit reader holds `get externalsecret`. Requires audit-log-read-time RBAC check; bigger surface change.

### Option C — accept the leak; document it

Cluster-scoped actions inherently expose cross-namespace data to admins. Audit log is admin-only by current RBAC. Document it in CLAUDE.md and move on.

**Recommendation:** Option A is the conservative match for the Phase D precedent. The aggregate counts give operators what they need without leaking per-secret identity across tenants.

## Acceptance Criteria

- [ ] `refresh_cluster_store` audit Detail does not enumerate per-namespace UIDs in `failed`/`skipped`.
- [ ] `refresh_store` and `refresh_namespace` audit Details retain full UID lists (single-namespace scope already establishes the permissioned context).
- [ ] Test: a cluster-store-scoped job with mixed-namespace failures produces an audit row whose Detail contains aggregate counts and reason histograms but no namespaced UIDs.
- [ ] CLAUDE.md (Phase 14 entry) documents the audit redaction rule alongside the existing Phase D notification suppression rule.
