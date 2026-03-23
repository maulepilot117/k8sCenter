# Phase 4C: Storage — Snapshot Lifecycle & PVC Wizard

## Overview

Phase 4C adds VolumeSnapshot CRUD (create, get, delete), a snapshot creation wizard, a restore-from-snapshot wizard, scheduled snapshots via CronJob, and a PVC creation wizard. The existing read-only snapshot listing (`storage/handler.go`) and CRD availability check are extended with write operations.

**Branch:** `feat/phase4c-storage`
**Depends on:** Phase 4B merged
**Spec:** `docs/superpowers/specs/2026-03-22-phase4-features-design.md` (Phase 4C section)

### Plan Review Changes (from reviewer feedback)

- Collapsed snapshot wizard from 3 steps to 2 (Configure + Review)
- Collapsed PVC wizard from 3 steps to 2 (Configure + Review)
- Collapsed restore wizard from 3 steps to 2 (Configure + Review)
- Moved scheduled snapshot YAML generation from frontend to backend (`wizard/scheduled_snapshot.go`)
- Inlined cron schedule input in ScheduledSnapshotWizard (no premature shared component)
- Added `AuditLogger` field + wiring to storage `Handler` struct
- Added `ErrorMessage` field to `SnapshotInfo` struct
- `HandleListSnapshotClasses` uses `BaseDynamicClient()` (not impersonated) — cluster-scoped metadata
- Added `ReadWriteOncePod` access mode to PVC/restore wizards
- Documented CRD gate behavior: list returns 200+metadata, CRUD returns 404
- Added URL param validation on storage routes
- Added handler-level tests to testing strategy
- Added known limitations section for scheduled snapshots
- Collapsed scheduled snapshot wizard from 4 steps to 3 (Source+Schedule, Retention, Review)

### Design Decisions

- **Dynamic client for all snapshot operations.** VolumeSnapshot is a CRD (not core API), so all CRUD uses `dynamic.Interface` with `unstructured.Unstructured`. Follow the `cilium.go` pattern, not the typed `configmaps.go` pattern.
- **CRD gate behavior:** List endpoint returns `200` with `metadata.available: false` (frontend renders helpful message). CRUD endpoints return `404` with a clear error message. This is intentional — listing is always safe, but creating/deleting a non-existent resource type is an error.
- **Driver/class compatibility validation in wizards.** The create snapshot wizard only offers VolumeSnapshotClasses whose `driver` matches the source PVC's StorageClass provisioner.
- **CronJob-based scheduled snapshots.** No operator dependency. Backend generates CronJob + ServiceAccount + Role + RoleBinding as multi-doc YAML via `wizard/scheduled_snapshot.go`.
- **Restore gated on readyToUse.** The restore wizard is disabled until `status.readyToUse == true` and `status.restoreSize` is populated.

---

## Step 4C.1 — Snapshot CRUD Backend

**Backend: `backend/internal/storage/handler.go`**

### Prerequisites — Handler struct updates

Before adding CRUD handlers, the `Handler` struct needs:

```go
type Handler struct {
    K8sClient         *k8s.ClientFactory
    Informers         *k8s.InformerManager
    AuditLogger       audit.Logger       // NEW — wire in main.go
    Logger            *slog.Logger
    snapshotMu        sync.Mutex
    snapshotAvail     bool
    snapshotCheckedAt time.Time
}
```

Add an `auditWrite` helper method (same pattern as `resources.Handler.auditWrite`).

Add `ErrorMessage` to the existing `SnapshotInfo` struct:
```go
ErrorMessage string `json:"errorMessage,omitempty"`
```
Extract from `buildSnapshotInfo`: `unstructured.NestedString(item, "status", "error", "message")`.

Apply URL param validation: add `ValidateURLParams` middleware (or inline validation) to the storage sub-router for `{namespace}` and `{name}` params.

### HandleCreateSnapshot

- Route: `POST /api/v1/storage/snapshots/{namespace}`
- Body: `{"name", "volumeSnapshotClassName", "sourcePVC"}`
- Implementation:
  1. `requireUser(w, r)` — extract authenticated user
  2. `checkSnapshotCRDs()` — return 404 if not available
  3. Decode body, validate: name (DNS label), sourcePVC (required), namespace from URL
  4. Build `unstructured.Unstructured` with apiVersion/kind/metadata/spec
  5. Impersonated dynamic client → `Resource(volumeSnapshotGVR).Namespace(ns).Create()`
  6. Audit log: `ActionCreate`, kind "VolumeSnapshot"
  7. Return 201

### HandleGetSnapshot

- Route: `GET /api/v1/storage/snapshots/{namespace}/{name}`
- Implementation: impersonated dynamic client → `Get()` → `buildSnapshotInfo()` → return

### HandleDeleteSnapshot

- Route: `DELETE /api/v1/storage/snapshots/{namespace}/{name}`
- Implementation: impersonated dynamic client → `Delete()` → audit log → 204

### HandleListSnapshotClasses

- Route: `GET /api/v1/storage/snapshot-classes`
- Implementation: **`BaseDynamicClient()`** (NOT impersonated — cluster-scoped metadata, consistent with existing `getSnapshotDrivers()`) → `Resource(volumeSnapshotClassGVR).List()` → return
- Used by wizard dropdown to select snapshot class

**Route registration** in `registerStorageRoutes()`:
```go
sr.Post("/snapshots/{namespace}", h.HandleCreateSnapshot)
sr.Get("/snapshots/{namespace}/{name}", h.HandleGetSnapshot)
sr.Delete("/snapshots/{namespace}/{name}", h.HandleDeleteSnapshot)
sr.Get("/snapshot-classes", h.HandleListSnapshotClasses)
```

**Files to modify:**
- `backend/internal/storage/handler.go` (add AuditLogger, ErrorMessage, 4 handlers, URL validation)
- `backend/internal/server/routes.go` (add routes to `registerStorageRoutes`)
- `backend/cmd/kubecenter/main.go` (wire AuditLogger to storage handler)

---

## Step 4C.2 — Snapshot Wizard (Create)

**Backend: `backend/internal/wizard/snapshot.go` (new)**

```go
type SnapshotInput struct {
    Name                    string `json:"name"`
    Namespace               string `json:"namespace"`
    SourcePVC               string `json:"sourcePVC"`
    VolumeSnapshotClassName string `json:"volumeSnapshotClassName,omitempty"`
}
```

- `Validate()` → `[]FieldError`: DNS label name, namespace required, sourcePVC required
- `ToVolumeSnapshot()` → `*unstructured.Unstructured`: builds snapshot with apiVersion/kind/metadata/spec
- Preview endpoint: `POST /api/v1/wizards/snapshot/preview`
- YAML marshaling: `sigsyaml.Marshal(obj.Object)` — note: unstructured produces non-deterministic key ordering (acceptable trade-off for CRD resources without typed structs)

**Frontend: `frontend/islands/SnapshotWizard.tsx` (new)**

2-step wizard:

### Step 1: Configure
- Namespace selector (dropdown from `/v1/resources/namespaces`)
- PVC dropdown (fetched from `/v1/resources/pvcs/{ns}?limit=500`)
  - Only show PVCs with `status.phase == "Bound"` (filter client-side)
  - Show PVC size and storage class next to each option
- VolumeSnapshotClass dropdown (from `GET /v1/storage/snapshot-classes`)
  - **Filter to classes matching the PVC's StorageClass provisioner**
  - Show "(default)" badge on classes with the default annotation
- Name (editable, auto-generated default: `{pvc-name}-snap-{YYYYMMDD-HHmmss}`)

### Step 2: Review
- YAML preview via `POST /v1/wizards/snapshot/preview`
- Apply via WizardReviewStep

**Route:** `frontend/routes/storage/snapshots/new.tsx`

**Files to create/modify:**
- `backend/internal/wizard/snapshot.go` (new)
- `backend/internal/wizard/handler.go` (add `HandleSnapshotPreview`)
- `backend/internal/server/routes.go` (add wizard route)
- `frontend/islands/SnapshotWizard.tsx` (new)
- `frontend/routes/storage/snapshots/new.tsx` (new)

---

## Step 4C.3 — Restore from Snapshot Wizard

**Frontend: `frontend/islands/RestoreSnapshotWizard.tsx` (new)**

Launched from snapshot list "Restore" action. Receives snapshot metadata via URL query params. **Guard at top of component:** if snapshot `readyToUse !== true` or `restoreSize` is empty, redirect back with error message — do not render the wizard.

2-step wizard:

### Step 1: Configure
- PVC name (text, default: `{snapshot-name}-restore`)
- Namespace (dropdown, default: snapshot's namespace)
- Size input with unit selector (Gi/Ti)
  - Pre-filled from `status.restoreSize`
  - **Validated >= restoreSize** (cannot shrink)
- StorageClass dropdown (from informer, pre-selected based on snapshot's driver)
- Access mode radio: ReadWriteOnce / ReadWriteMany / ReadOnlyMany / ReadWriteOncePod

### Step 2: Review
- YAML preview via `POST /v1/wizards/pvc/preview` (reuse PVC wizard preview, with `dataSource` field)
- The generated PVC includes `spec.dataSource` referencing the VolumeSnapshot

**Backend: Extend `wizard/pvc.go` PVCInput** (see Step 4C.5) with optional `DataSource *PVCDataSource` field.

**Route:** `frontend/routes/storage/snapshots/restore.tsx`

**Files to create/modify:**
- `frontend/islands/RestoreSnapshotWizard.tsx` (new)
- `frontend/routes/storage/snapshots/restore.tsx` (new)

---

## Step 4C.4 — Scheduled Snapshots Wizard

**Backend: `backend/internal/wizard/scheduled_snapshot.go` (new)**

```go
type ScheduledSnapshotInput struct {
    Name                    string `json:"name"`
    Namespace               string `json:"namespace"`
    SourcePVC               string `json:"sourcePVC"`
    VolumeSnapshotClassName string `json:"volumeSnapshotClassName"`
    Schedule                string `json:"schedule"`       // cron expression
    RetentionCount          int    `json:"retentionCount"` // keep last N
}
```

- `Validate()` → `[]FieldError`: DNS label name, namespace, sourcePVC, schedule (basic cron format check), retentionCount (1-100)
- `ToResources()` → `[]runtime.Object` or `[]map[string]interface{}`: generates 4 resources:
  1. **ServiceAccount** — `{name}-snapshotter`
  2. **Role** — `create`, `get`, `list`, `delete` on `volumesnapshots` + `get`, `list` on `persistentvolumeclaims`
  3. **RoleBinding** — binds SA to Role
  4. **CronJob** — `bitnami/kubectl:1.31` (pinned) with inline script for create+cleanup
- Preview endpoint: `POST /api/v1/wizards/scheduled-snapshot/preview` — marshals all 4 resources as multi-doc YAML (joined with `---\n`)

**Frontend: `frontend/islands/ScheduledSnapshotWizard.tsx` (new)**

3-step wizard:

### Step 1: Source & Schedule
- Namespace + PVC picker (same pattern as SnapshotWizard, bound-only filter)
- VolumeSnapshotClass dropdown (driver-filtered)
- Schedule name (text, DNS label)
- Cron schedule input (inline, not shared component):
  - Preset buttons: "Every hour", "Daily at midnight", "Weekly Sunday", "Custom"
  - Custom: text input with 5-field cron expression
  - Human-readable preview

### Step 2: Retention
- Keep last N snapshots (number input, default: 5, min: 1, max: 100)
- Info text: "Older snapshots will be automatically deleted after each run"

### Step 3: Review
- YAML preview via `POST /v1/wizards/scheduled-snapshot/preview`
- Apply via WizardReviewStep (multi-doc YAML, existing support)

**Route:** `frontend/routes/storage/snapshots/schedule.tsx`

### Known Limitations (Scheduled Snapshots)

- **Retention cleanup is best-effort.** If the delete step fails partway through (e.g., API server unavailable), some old snapshots may survive. No automatic retry.
- **kubectl image version needs manual tracking.** The `bitnami/kubectl:1.31` pin should be bumped when the cluster is upgraded to a new Kubernetes minor version. Document in the wizard's review step.
- **Shell script debugging.** The create+cleanup logic runs as an inline shell script in the CronJob. Debugging requires `kubectl logs` on completed Job pods.
- **No cross-namespace.** Each schedule targets a single PVC in a single namespace.

**Files to create/modify:**
- `backend/internal/wizard/scheduled_snapshot.go` (new)
- `backend/internal/wizard/handler.go` (add `HandleScheduledSnapshotPreview`)
- `backend/internal/server/routes.go` (add wizard route)
- `frontend/islands/ScheduledSnapshotWizard.tsx` (new)
- `frontend/routes/storage/snapshots/schedule.tsx` (new)

---

## Step 4C.5 — PVC Wizard

**Backend: `backend/internal/wizard/pvc.go` (new)**

```go
type PVCInput struct {
    Name             string            `json:"name"`
    Namespace        string            `json:"namespace"`
    StorageClassName string            `json:"storageClassName"`
    Size             string            `json:"size"`        // e.g. "10Gi"
    AccessMode       string            `json:"accessMode"`  // ReadWriteOnce, ReadWriteMany, ReadOnlyMany, ReadWriteOncePod
    Labels           map[string]string `json:"labels,omitempty"`
    DataSource       *PVCDataSource    `json:"dataSource,omitempty"` // for restore from snapshot
}

type PVCDataSource struct {
    Name     string `json:"name"`
    Kind     string `json:"kind"`
    APIGroup string `json:"apiGroup"`
}
```

- `Validate()` → `[]FieldError`: DNS label name, namespace required, size must parse as `resource.Quantity` and be > 0, accessMode must be valid enum (including `ReadWriteOncePod`), storageClassName validated
- `ToPersistentVolumeClaim()` → `*corev1.PersistentVolumeClaim`: builds typed PVC. If `DataSource` is set, populate `spec.dataSource`.
- Preview endpoint: `POST /api/v1/wizards/pvc/preview`

**Frontend: `frontend/islands/PVCWizard.tsx` (new)**

2-step wizard:

### Step 1: Configure
- Name (text, DNS label)
- Namespace (dropdown)
- StorageClass dropdown (from `/v1/resources/storageclasses`)
- Size input with unit selector: number + dropdown (Mi / Gi / Ti)
- Access mode radio: ReadWriteOnce / ReadWriteMany / ReadOnlyMany / ReadWriteOncePod

### Step 2: Review
- YAML preview via `POST /v1/wizards/pvc/preview`
- Apply via WizardReviewStep

**Route:** `frontend/routes/storage/pvcs/new.tsx`

**Files to create/modify:**
- `backend/internal/wizard/pvc.go` (new)
- `backend/internal/wizard/handler.go` (add `HandlePVCPreview`)
- `backend/internal/server/routes.go` (add wizard route)
- `frontend/islands/PVCWizard.tsx` (new)
- `frontend/routes/storage/pvcs/new.tsx` (new)

---

## Step 4C.6 — Frontend Integration

**Snapshot Browser Enhancement:**
- Update `frontend/islands/SnapshotList.tsx` to add action buttons:
  - "Create Snapshot" button in header → links to `/storage/snapshots/new`
  - Per-row "Restore" action → links to `/storage/snapshots/restore?ns={ns}&name={name}` (disabled when `readyToUse !== true`)
  - Per-row "Delete" action → confirm dialog, calls `DELETE /v1/storage/snapshots/{ns}/{name}`
  - "Schedule Snapshots" button in header → links to `/storage/snapshots/schedule`
- Show `status.error.message` (from new `ErrorMessage` field) in a red badge if present

**PVC Browser Enhancement:**
- Add `createHref="/storage/pvcs/new"` to PVC resource table page
- Add "Snapshot" action to PVC kebab menu in `action-handlers.ts` → navigates to `/storage/snapshots/new?ns={ns}&pvc={name}`

**Files to modify:**
- `frontend/islands/SnapshotList.tsx` (add actions, error display)
- `frontend/routes/storage/pvcs.tsx` (add `createHref`)
- `frontend/lib/action-handlers.ts` (add snapshot action for PVCs)

---

## Implementation Order

1. **4C.5** — PVC wizard (standalone, simplest, establishes wizard/pvc.go with DataSource support)
2. **4C.1** — Snapshot CRUD backend (enables all snapshot features, add AuditLogger + ErrorMessage)
3. **4C.2** — Snapshot create wizard (depends on 4C.1)
4. **4C.3** — Restore wizard (depends on 4C.1 + 4C.5 for PVC preview with dataSource)
5. **4C.4** — Scheduled snapshots wizard (depends on 4C.1, backend generates multi-doc YAML)
6. **4C.6** — Frontend integration (depends on all above)

Steps 4C.5 and 4C.1 can run in parallel. Steps 4C.2, 4C.3, 4C.4 can run in parallel after 4C.1.

---

## Files Summary

### New files (11)
- `backend/internal/wizard/pvc.go`
- `backend/internal/wizard/snapshot.go`
- `backend/internal/wizard/scheduled_snapshot.go`
- `frontend/islands/SnapshotWizard.tsx`
- `frontend/islands/RestoreSnapshotWizard.tsx`
- `frontend/islands/ScheduledSnapshotWizard.tsx`
- `frontend/islands/PVCWizard.tsx`
- `frontend/routes/storage/snapshots/new.tsx`
- `frontend/routes/storage/snapshots/restore.tsx`
- `frontend/routes/storage/snapshots/schedule.tsx`
- `frontend/routes/storage/pvcs/new.tsx`

### Modified files (7)
- `backend/internal/storage/handler.go` (add AuditLogger, ErrorMessage, 4 CRUD handlers, URL validation)
- `backend/internal/wizard/handler.go` (add snapshot + PVC + scheduled-snapshot preview handlers)
- `backend/internal/server/routes.go` (add storage + wizard routes)
- `backend/cmd/kubecenter/main.go` (wire AuditLogger to storage handler)
- `frontend/islands/SnapshotList.tsx` (add action buttons, error display)
- `frontend/routes/storage/pvcs.tsx` (add `createHref`)
- `frontend/lib/action-handlers.ts` (add snapshot action for PVCs)

---

## Testing Strategy

- **Backend unit tests:** table-driven tests for `SnapshotInput.Validate()`, `PVCInput.Validate()`, `ScheduledSnapshotInput.Validate()`, `ToPersistentVolumeClaim()`, `ToVolumeSnapshot()`, `ToResources()`. Test DataSource inclusion in PVC conversion.
- **Handler-level tests (httptest):** CRD-not-installed returns 404, successful create returns 201, delete returns 204, get-not-found returns 404, impersonation is used, audit logging fires.
- **CRD gate tests:** verify `checkSnapshotCRDs()` returns correct state, verify list returns `available: false` (200), verify create returns 404.
- **Frontend:** `deno lint`, `deno fmt --check`, `deno check` on all new files.
- **Homelab smoke test:**
  - Create PVC via wizard, verify it appears in PVC list
  - Create snapshot of bound PVC, verify appears in snapshot list with readyToUse=true
  - Restore from snapshot, verify new PVC is created with correct size
  - Delete snapshot, verify removed from list
  - Create scheduled snapshot CronJob, verify CronJob + RBAC resources created
  - Verify error message displays when snapshot fails

---

## Acceptance Criteria

- [ ] Storage handler has `AuditLogger` wired and used on all write operations
- [ ] `SnapshotInfo` includes `ErrorMessage` field, displayed in frontend
- [ ] `POST /api/v1/storage/snapshots/{ns}` creates VolumeSnapshot via dynamic client
- [ ] `GET /api/v1/storage/snapshots/{ns}/{name}` returns snapshot with status info
- [ ] `DELETE /api/v1/storage/snapshots/{ns}/{name}` deletes snapshot with audit log
- [ ] `GET /api/v1/storage/snapshot-classes` lists classes via `BaseDynamicClient()` (not impersonated)
- [ ] List endpoint returns 200 with `available: false` when CRDs missing; CRUD returns 404
- [ ] URL params validated on storage routes (DNS label check)
- [ ] Snapshot wizard filters classes by driver compatibility with source PVC
- [ ] Snapshot wizard only shows bound PVCs
- [ ] Restore wizard guards on `readyToUse == true` and `restoreSize` populated
- [ ] Restore wizard pre-fills size from restoreSize, validates >= restoreSize
- [ ] Restore wizard generates PVC with correct dataSource
- [ ] PVC wizard supports `ReadWriteOncePod` access mode
- [ ] Scheduled snapshot backend (`wizard/scheduled_snapshot.go`) generates 4 resources as multi-doc YAML
- [ ] CronJob uses pinned kubectl image (`bitnami/kubectl:1.31`)
- [ ] Retention cleanup deletes oldest snapshots beyond count
- [ ] All Go tests pass (including handler-level + CRD gate tests)
- [ ] All frontend lint/type checks pass
- [ ] Homelab smoke test passes
