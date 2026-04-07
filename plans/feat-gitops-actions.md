# feat: GitOps Actions — Argo CD Sync & Flux CD Reconciliation

## Overview

Add POST endpoints and frontend action buttons to trigger Argo CD sync/rollback and Flux CD reconciliation/suspend from the k8sCenter GitOps dashboard. Currently the GitOps integration (Phase 9A/9B) is read-only — users can view application status but cannot take action. This feature closes that gap.

## Problem Statement

Users viewing GitOps applications in k8sCenter must switch to `argocd` CLI, `flux` CLI, or kubectl to trigger syncs, reconciles, suspends, or rollbacks. This breaks the "single pane of glass" experience and requires separate tooling knowledge. The existing GitOps detail page already shows all the context needed to make action decisions (sync status, health, revision history) — it just lacks the buttons.

## Proposed Solution

Add 3 action endpoints (`/sync`, `/suspend`, `/rollback`) that dispatch to the correct tool adapter based on the composite ID prefix (`argo:`, `flux-ks:`, `flux-hr:`), with corresponding frontend action buttons on the GitOps detail page.

---

## Technical Approach

### Architecture

All actions follow the existing resource action pattern (`backend/internal/k8s/resources/`):

1. Extract authenticated user from request context
2. Parse composite ID → tool prefix + namespace + name
3. RBAC check: `CanAccessGroupResource(ctx, user, groups, "patch", apiGroup, resource, ns)`
4. Create impersonating dynamic client via `DynamicClientForUser()`
5. Build JSON merge patch payload
6. Apply patch via `dynClient.Resource(gvr).Namespace(ns).Patch()`
7. Audit log the action
8. Invalidate the cached apps list
9. Return result to client

**Patch type:** `types.MergePatchType` for all operations (CRDs don't have strategic merge patch metadata).

**Field manager:** Flux patches must use `metav1.PatchOptions{FieldManager: "flux-client-side-apply"}` to match Flux controller ownership expectations. Argo CD patches need no specific field manager.

### API Endpoints

All under `/api/v1/gitops/applications/{id}/`:

| Endpoint | Method | Argo CD | Flux Kustomization | Flux HelmRelease |
|----------|--------|---------|---------------------|-------------------|
| `/sync` | POST | Patch `operation.sync` on Application | Annotate `reconcile.fluxcd.io/requestedAt` | Annotate `reconcile.fluxcd.io/requestedAt` |
| `/suspend` | POST | Stash sync policy in annotation + remove `spec.syncPolicy.automated`, OR restore from annotation | Patch `spec.suspend` | Patch `spec.suspend` |
| `/rollback` | POST | Sync to revision from `status.history` | 405 (not supported — rollback is a git operation) | 405 (not supported) |

**Request bodies:**

```
POST /sync          — {} (empty, syncs to HEAD)
POST /suspend       — {"suspend": true} or {"suspend": false}
POST /rollback      — {"revision": "<git-sha-from-history>"}
```

**Response:** `{"data": {"message": "Sync triggered"}}` or standard error envelope. Frontend re-fetches detail after action completes.

### Implementation Phases

#### Phase 1: Backend Actions (3 files)

**Step 1: Add action methods to gitops handler**

File: `backend/internal/gitops/handler.go`

- Add `AuditLogger` field to `Handler` struct
- Add private `prepareAction(w, r) (toolPrefix, ns, name string, dynClient dynamic.Interface, user *auth.User, ok bool)` helper to extract the common preamble (requireUser → parseCompositeID → toolGVR → RBAC check → DynamicClientForUser) — avoids 3x boilerplate
- Add `HandleSync(w, r)` — dispatches to Argo sync or Flux reconcile based on composite ID prefix
- Add `HandleSuspend(w, r)` — both tools, reads `{"suspend": true/false}` from body
- Add `HandleRollback(w, r)` — Argo CD only, 405 for Flux
- Add `invalidateCache()` helper: acquires `cacheMu.Lock()`, sets `h.cachedData = nil`, AND calls `h.fetchGroup.Forget("fetch")` to prevent stale singleflight repopulation
- All handlers follow pattern: prepareAction → build patch → apply → audit log → invalidateCache → respond

**Step 2: Add patch builders to adapters**

File: `backend/internal/gitops/argocd.go`

All adapter functions return `(*unstructured.Unstructured, error)` — the patched CRD from the Patch response. No re-fetch needed.

- `SyncArgoApp(ctx, dynClient, ns, name, username string) (*unstructured.Unstructured, error)`
  - Builds `operation.sync` merge patch with `initiatedBy.username`, syncs to HEAD
  - Pre-check: fetch app, verify `status.operationState.phase != "Running"` (reject if sync in progress)
- `SuspendArgoApp(ctx, dynClient, ns, name string) (*unstructured.Unstructured, error)`
  - Read current `spec.syncPolicy.automated`, stash in annotation `kubecenter.io/pre-suspend-sync-policy` as JSON
  - Then patch `spec.syncPolicy.automated` to null (removes automated sync)
- `ResumeArgoApp(ctx, dynClient, ns, name string) (*unstructured.Unstructured, error)`
  - Read annotation `kubecenter.io/pre-suspend-sync-policy`, restore original policy
  - Remove the annotation after restore
  - If annotation missing, restore defaults `{"prune": true, "selfHeal": true}` and warn in response
- `RollbackArgoApp(ctx, dynClient, ns, name, revision, username string) (*unstructured.Unstructured, error)`
  - Same as sync but with explicit `revision` from request body
  - Pre-check: verify revision exists in `status.history`
  - Pre-check: reject with 409 if `spec.syncPolicy.automated` is set (D1)

File: `backend/internal/gitops/flux.go`

- `ReconcileFluxResource(ctx, dynClient, gvr, ns, name string) (*unstructured.Unstructured, error)`
  - Patches `metadata.annotations["reconcile.fluxcd.io/requestedAt"]` with Unix timestamp
  - Uses `FieldManager: "flux-client-side-apply"`
  - Pre-check: reject with 409 if `spec.suspend == true`
- `SuspendFluxResource(ctx, dynClient, gvr, ns, name string, suspend bool) (*unstructured.Unstructured, error)`
  - Patches `spec.suspend` to the given boolean

**Step 3: Register routes**

File: `backend/internal/server/routes.go`

- Add 3 POST routes under the existing gitops group:
  ```
  POST /gitops/applications/{id}/sync
  POST /gitops/applications/{id}/suspend
  POST /gitops/applications/{id}/rollback
  ```

**Step 4: Add GitOps audit action constants**

File: `backend/internal/audit/logger.go`

- Add constants: `ActionGitOpsSync`, `ActionGitOpsSuspend`, `ActionGitOpsResume`, `ActionGitOpsRollback`
- Use `ActionGitOpsSync` for both Argo sync and Flux reconcile; record tool name in the `Detail` field

**Step 5: Wire audit logger**

File: `backend/internal/server/server.go`

- Pass `AuditLogger` to `gitops.Handler` during construction (same as other handlers)

#### Phase 2: Frontend Action Buttons (2 files)

**Step 6: Add action buttons to detail page**

File: `frontend/islands/GitOpsAppDetail.tsx`

- Add action bar in the header section with buttons:
  - **Sync/Reconcile** (primary) — label adapts to tool: "Sync" for Argo CD, "Reconcile" for Flux
  - **Suspend/Resume** toggle — shows "Suspend" when active, "Resume" when suspended
  - **Rollback** — Argo CD only, per-row button in the revision history table (D6). Clicking opens confirmation: "Roll back to revision {sha} deployed at {date}?"
- Each button calls `apiPost(`/gitops/applications/${id}/${action}`)` from `lib/api.ts`
- Confirmation dialog before sync, rollback, and suspend (not resume)
- Toast notification on success/failure
- Re-fetch detail data after action completes
- Disable Sync/Rollback buttons when `syncStatus == "progressing"`
- Disable Reconcile button when `app.suspended === true` (tooltip: "Resume before reconciling")
- Let 403 surface naturally as toast error (no frontend RBAC pre-check needed)

#### Phase 3: Tests (2 files)

**Step 8: Backend unit tests**

File: `backend/internal/gitops/handler_test.go`

- Test each action handler: sync, refresh, suspend, resume, rollback
- Test RBAC rejection (403)
- Test composite ID parsing for actions
- Test Argo CD in-progress guard (409 when sync already running)
- Test Flux 405 for unsupported actions (refresh, rollback)
- Test cache invalidation after write

File: `backend/internal/gitops/argocd_test.go` / `flux_test.go`

- Test patch payload construction for each action
- Test pre-checks (operation in progress, resource suspended)

---

## Design Decisions

### D1: Rollback + Auto-Sync Conflict

Refuse with 409 if `spec.syncPolicy.automated` is set. Return: `"Cannot rollback: auto-sync is enabled. Suspend auto-sync first."` This avoids the UX trap where a rollback silently gets re-synced within seconds.

### D2: Argo CD Suspend/Resume — Preserve Original Config

Before suspending, stash the current `spec.syncPolicy.automated` value in annotation `kubecenter.io/pre-suspend-sync-policy` as JSON. On resume, read the annotation and restore the original policy, then remove the annotation. If annotation is missing (e.g., manually deleted), fall back to defaults `{"prune": true, "selfHeal": true}` and include a warning in the response. This prevents the data-destructive behavior of silently overwriting user config on resume.

### D3: Rollback Revision Picker UI

Per-row "Rollback" button in the existing revision history table on the detail page. Each row already shows revision SHA, status, and deployed-at. Clicking opens a confirmation dialog. No separate modal or wizard — the history table IS the picker.

### D4: Multi-Cluster Scope

Local cluster only for this phase. Action endpoints use `DynamicClientForUser()` (same as `HandleGetApplication`). Multi-cluster GitOps actions are a future enhancement requiring `ClusterRouter` integration.

### D5: Cache Invalidation

`invalidateCache()` acquires `cacheMu.Lock()`, sets `h.cachedData = nil`, AND calls `h.fetchGroup.Forget("fetch")` to prevent stale singleflight repopulation from concurrent in-flight fetches.

### Implementation Notes

- All actions use RBAC verb `patch` via `CanAccessGroupResource`
- Audit: single `ActionGitOpsSync` constant for both Argo sync and Flux reconcile; tool name recorded in `Detail` field
- Suspend requires confirmation dialog; Resume does not
- Frontend disables Reconcile button when `app.suspended === true` (tooltip: "Resume before reconciling"). Backend also returns 409 as safety net
- Backend checks `status.operationState.phase` before Argo CD sync — returns 409 if already Running

---

## Edge Cases & Safety Guards

| Scenario | Handling |
|----------|----------|
| Sync triggered while sync in progress | Argo CD: return 409 Conflict with message. Flux: no-op (annotation idempotent) |
| Rollback with auto-sync enabled | Return 409: "Cannot rollback: auto-sync is enabled. Suspend first." (D1) |
| Reconcile on suspended Flux resource | Return 409 with message: "Resource is suspended. Resume before reconciling." |
| User lacks RBAC to patch CRD | Return 403 before attempting patch |
| CRD not installed on cluster | Discovery already handles this — tool shows as unavailable, routes return 404 |
| Concurrent users trigger same action | First patch wins; second gets 409 (Argo CD) or is idempotent (Flux) |
| Argo CD `operation` field already set | Fetch first, check `status.operationState.phase`, reject if Running |
| Flux annotation same as last handled | Always use fresh timestamp to guarantee new value |
| Network timeout during patch | Standard k8s client timeout (10s), return 504 to frontend |

## Acceptance Criteria

### Functional
- [ ] Argo CD apps can be synced, suspended, resumed, and rolled back from the detail page
- [ ] Flux Kustomizations and HelmReleases can be reconciled, suspended, and resumed
- [ ] Unsupported actions return 405 (e.g., rollback for Flux)
- [ ] All actions use user impersonation (never service account)
- [ ] All actions are audit-logged with GitOps-specific constants
- [ ] RBAC pre-checked (verb `patch`) before attempting the action
- [ ] Cached app list invalidated after any write (including singleflight forget)
- [ ] Frontend shows confirmation dialog before sync, rollback, and suspend
- [ ] Frontend disables buttons during in-flight actions
- [ ] Toast notifications on success/failure

### Safety
- [ ] Rollback is rejected (409) when Argo CD auto-sync is enabled
- [ ] Sync is rejected (409) when an Argo CD sync is already in-progress
- [ ] Reconcile is rejected (409) when Flux resource is suspended
- [ ] Argo CD suspend stashes original sync policy in annotation; resume restores it

### Non-Functional
- [ ] Sync action completes within 5s (patch only — actual sync is async)
- [ ] No new dependencies added
- [ ] All existing tests continue to pass

---

## Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `backend/internal/gitops/handler.go` | Modify | Add AuditLogger, prepareAction helper, 3 action handlers, invalidateCache |
| `backend/internal/gitops/argocd.go` | Modify | Add Sync, Suspend, Resume, Rollback patch builders |
| `backend/internal/gitops/flux.go` | Modify | Add Reconcile, Suspend patch builders |
| `backend/internal/audit/logger.go` | Modify | Add 4 GitOps action constants |
| `backend/internal/server/routes.go` | Modify | Register 3 POST routes |
| `backend/internal/server/server.go` | Modify | Pass AuditLogger to GitOps handler |
| `frontend/islands/GitOpsAppDetail.tsx` | Modify | Add action buttons, confirmation dialogs, rollback per-row |
| `backend/internal/gitops/handler_test.go` | Modify | Add action handler tests |
| `backend/internal/gitops/argocd_test.go` | Modify | Add patch builder + pre-check tests |
| `backend/internal/gitops/flux_test.go` | Modify | Add patch builder tests |

## References

### Internal
- GitOps handler: `backend/internal/gitops/handler.go` (composite ID parsing, RBAC, impersonation)
- Resource action pattern: `backend/internal/k8s/resources/deployments.go:179` (HandleScaleDeployment)
- Audit logging: `backend/internal/audit/logger.go`
- API client: `frontend/lib/api.ts:155` (apiPost)
- Dynamic client: `backend/internal/k8s/client.go:162` (DynamicClientForUser)

### External
- Argo CD sync via kubectl: argo-cd.readthedocs.io/en/stable/user-guide/sync-kubectl/
- Argo CD annotations: argo-cd.readthedocs.io/en/latest/user-guide/annotations-and-labels/
- Flux reconciliation trigger: fluxcd.io/flux/components/kustomize/kustomizations/
- Flux HelmRelease API v2: fluxcd.io/flux/components/helm/api/v2/
- client-go dynamic patch: pkg.go.dev/k8s.io/client-go/dynamic

### Related PRs
- Phase 9A (GitOps backend): #141
- Phase 9B (GitOps frontend): #142
