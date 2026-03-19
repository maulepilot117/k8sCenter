# feat: Context-Aware Resource Action Buttons

## Overview

Add a kebab menu (three-dot icon) to each row in the resource tables for Deployments, StatefulSets, DaemonSets, Pods, Jobs, and CronJobs. Actions are context-aware — only valid actions for that resource type appear, and actions the user lacks RBAC permission for are disabled.

## Action Matrix

| Resource     | Scale | Restart | Rollback | Delete | Suspend/Resume | Trigger |
|-------------|-------|---------|----------|--------|----------------|---------|
| Deployment   | Y     | Y       | Y        | Y      |                |         |
| StatefulSet  | Y     | Y       |          | Y      |                |         |
| DaemonSet    |       | Y       |          | Y      |                |         |
| Pod          |       |         |          | Y      |                |         |
| Job          |       |         |          | Y      | Y              |         |
| CronJob      |       |         |          | Y      | Y              | Y       |

**Excluded (per review):**
- ReplicaSet actions — routes are explicitly read-only, managed by Deployments
- Pod exec/logs shortcuts — already accessible from detail page tabs
- Pod evict — niche operation, delete is sufficient for GUI users

## Backend Changes

### New Endpoints (5 total)

| Endpoint | Handler | Pattern |
|----------|---------|---------|
| `POST /resources/statefulsets/{ns}/{name}/restart` | `HandleRestartStatefulSet` | Shared restart helper |
| `POST /resources/daemonsets/{ns}/{name}/restart` | `HandleRestartDaemonSet` | Shared restart helper |
| `POST /resources/jobs/{ns}/{name}/suspend` | `HandleSuspendJob` | Patch `spec.suspend` (body: `{"suspend": bool}`) |
| `POST /resources/cronjobs/{ns}/{name}/suspend` | `HandleSuspendCronJob` | Patch `spec.suspend` |
| `POST /resources/cronjobs/{ns}/{name}/trigger` | `HandleTriggerCronJob` | Create Job from `spec.jobTemplate` |

### Existing Endpoints (already implemented)

- `POST /resources/deployments/{ns}/{name}/scale`
- `POST /resources/deployments/{ns}/{name}/restart`
- `POST /resources/deployments/{ns}/{name}/rollback`
- `POST /resources/statefulsets/{ns}/{name}/scale`
- `DELETE /resources/{kind}/{ns}/{name}` (all kinds)

### Implementation Details

#### Shared restart helper (handler.go)

Extract the restart annotation patch from `HandleRestartDeployment` into a reusable helper. All three workload types use the same `kubectl.kubernetes.io/restartedAt` annotation patch:

```go
func (h *Handler) restartWorkload(w http.ResponseWriter, r *http.Request, kind string,
    patchFn func(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions) (any, error)) {
    // Extract user, namespace, name
    // RBAC check: update on {kind}
    // Build restart annotation patch JSON
    // Call patchFn
    // Audit log (audit.ActionUpdate)
}
```

Then each handler is a thin 5-line wrapper calling `restartWorkload` with the appropriate client method.

#### HandleSuspendJob / HandleSuspendCronJob (jobs.go)

```go
// Single endpoint handles both suspend and resume via body: {"suspend": bool}
// RBAC check: update on jobs/cronjobs
// Guard: if job is completed (status.completionTime != nil), return 422
// Patch spec.suspend via strategic merge
// Audit log (audit.ActionUpdate)
```

#### HandleTriggerCronJob (jobs.go)

```go
// RBAC check: create on jobs (NOT update on cronjobs)
// Fetch CronJob from informer, read spec.jobTemplate
// Construct Job with generateName: truncate(cronJob.Name, 43) + "-manual-"
// Set ownerReferences to CronJob
// Create Job via impersonating client
// Return created Job name in response
// Audit log (audit.ActionCreate)
```

### Routes (routes.go)

```go
// StatefulSet/DaemonSet restart
ar.Post("/resources/statefulsets/{namespace}/{name}/restart", h.HandleRestartStatefulSet)
ar.Post("/resources/daemonsets/{namespace}/{name}/restart", h.HandleRestartDaemonSet)

// Job/CronJob suspend
ar.Post("/resources/jobs/{namespace}/{name}/suspend", h.HandleSuspendJob)
ar.Post("/resources/cronjobs/{namespace}/{name}/suspend", h.HandleSuspendCronJob)
ar.Post("/resources/cronjobs/{namespace}/{name}/trigger", h.HandleTriggerCronJob)
```

## Frontend Changes

### Architecture (simplified per review)

No separate `resource-actions.ts` file or generic `ResourceAction` framework. Instead:

1. **Action map** — a simple `Record<string, string[]>` mapping kind → action IDs, defined at the top of ResourceTable or in constants
2. **Action handlers** — a `handleAction(actionId, resource)` function with a switch statement for API calls, extracted to `lib/action-handlers.ts`
3. **Kebab menu** — inline in ResourceTable (not a separate component file)
4. **Two confirmation modes** — `confirm` (simple OK/Cancel with message) and `destructive` (type resource name to confirm). No separate tiers for simple vs warning — just vary the message text.

### Action Map

```typescript
const ACTIONS_BY_KIND: Record<string, string[]> = {
  deployments:  ["scale", "restart", "rollback", "delete"],
  statefulsets: ["scale", "restart", "delete"],
  daemonsets:   ["restart", "delete"],
  pods:         ["delete"],
  jobs:         ["suspend", "delete"],
  cronjobs:     ["suspend", "trigger", "delete"],
};
```

### Action Handler (lib/action-handlers.ts)

Maps action IDs to API calls. Keeps ResourceTable focused on rendering:

```typescript
export async function executeAction(
  actionId: string, kind: string, namespace: string, name: string, params?: Record<string, unknown>
): Promise<{ ok: boolean; message: string }> {
  switch (actionId) {
    case "scale":
      await apiPost(`/v1/resources/${kind}/${namespace}/${name}/scale`, { replicas: params?.replicas });
      return { ok: true, message: `Scaled to ${params?.replicas} replicas` };
    case "restart":
      await apiPost(`/v1/resources/${kind}/${namespace}/${name}/restart`);
      return { ok: true, message: "Rolling restart initiated" };
    // ... etc
  }
}
```

### Confirmation Behavior

| Action | Mode | Message |
|--------|------|---------|
| Scale | None | Opens scale dialog directly |
| Restart | Confirm | "Rolling restart will cycle all pods. Continue?" |
| Rollback | None | Opens revision picker (existing detail page) |
| Suspend/Resume | Confirm | "Suspend/Resume this {kind}?" |
| Trigger | Confirm | "Create a new Job from this CronJob's template?" |
| Delete | Destructive | Owner warning if managed, "permanently delete" if not |

### Owner Reference Warnings (in delete confirmation)

When deleting a resource with `ownerReferences`:
- Show a warning banner: "This [kind] is managed by [ownerKind]/[ownerName] and will be recreated after deletion."
- No link to parent (avoid routing complexity)

### Post-Action Feedback

- **Success:** Toast notification with action-specific message (e.g., "Restart initiated", "Job daily-report-manual-xxx created")
- **Failure:** Toast with error message from API response
- **In-flight:** Kebab menu disabled while action is executing to prevent double-submission
- Table rows update automatically via existing WebSocket MODIFIED/DELETED events

### Files to Create/Modify (Frontend)

| File | Change |
|------|--------|
| `frontend/lib/action-handlers.ts` | **New** — action ID → API call mapping |
| `frontend/islands/ResourceTable.tsx` | **Modify** — add kebab menu column, confirm/scale dialogs inline, toast feedback |
| `frontend/lib/k8s-types.ts` | **Modify** — verify Job/CronJob types have `spec.suspend` |

### RBAC Strategy

Use the `SelfSubjectRulesReview` data already returned by `/api/v1/auth/me` for the selected namespace. Derive client-side whether the user can `update`/`delete`/`create` the relevant resource type. Disable unauthorized actions with "Insufficient permissions" tooltip.

No per-row API calls. RBAC data refreshes when namespace changes (already handled by existing auth flow).

## Implementation Order

1. **Backend: shared restart helper** — extract from deployments.go, add sts/ds handlers
2. **Backend: suspend/trigger handlers** — job suspend, cronjob suspend, cronjob trigger
3. **Backend: register routes** — add to routes.go
4. **Frontend: action handlers** — `lib/action-handlers.ts`
5. **Frontend: kebab menu + dialogs** — modify ResourceTable.tsx
6. **Backend tests** — handler tests for new endpoints

## Acceptance Criteria

- [ ] Deployment, StatefulSet, DaemonSet, Pod, Job, CronJob tables have kebab menus
- [ ] Actions are context-appropriate per resource type (no Scale on DaemonSet, etc.)
- [ ] Scale dialog shows current replicas with number input (0-1000, matches backend)
- [ ] Restart triggers rolling restart for Deployments, StatefulSets, DaemonSets
- [ ] Delete shows type-name-to-confirm with owner warning for managed resources
- [ ] Job/CronJob suspend/resume toggles based on current `spec.suspend` state
- [ ] Suspending a completed Job returns 422 with clear message
- [ ] CronJob trigger creates a new Job (using generateName) and shows name in toast
- [ ] Actions unavailable due to RBAC are disabled with tooltip
- [ ] All new actions are audit logged
- [ ] Success/failure toasts after every action
- [ ] WebSocket updates refresh table rows after actions
