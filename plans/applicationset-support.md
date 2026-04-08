# feat: Argo CD ApplicationSet Support

## Overview

Full CRUD support for Argo CD ApplicationSets — list, detail, create (YAML), edit (YAML), delete (with child app warning), and refresh. New tab in the GitOps section with list and detail views showing generator types, generated application counts, and aggregated sync/health status.

## Problem Statement

k8sCenter can display and manage individual Argo CD Applications but has no visibility into ApplicationSets — the parent objects that generate them. Users cannot see generator configurations, the relationship between ApplicationSets and their child Applications, or perform lifecycle operations on ApplicationSets.

## Architecture

### Design Decisions

| Decision | Choice | Why |
|---|---|---|
| Scope | Full CRUD: list, detail, create, edit, delete, refresh | ApplicationSets are first-class GitOps objects |
| Data model | New `NormalizedAppSet` type (not extending NormalizedApp) | Different concept — generators, not source/destination |
| Composite ID prefix | `argo-as:namespace:name` | Follows existing scheme |
| Child app linking | Label selector `argocd.argoproj.io/application-set-name` | More reliable than ownerRefs |
| Caching | Separate singleflight + 30s cache from Applications | Different data, independent lifecycle |
| Discovery | Explicit CRD check for `applicationsets` in `argoproj.io/v1alpha1` | Don't assume existence from Applications |
| Create/Edit | YAML editor (existing Monaco pipeline) | AppSets are too complex for a wizard |
| Delete | Confirmation dialog with child app count + preserve-on-deletion status | Destructive: can cascade-delete all child apps |
| Sync summary | Reuse existing `AppListMetadata` type | No new type needed |

### Types

```go
type NormalizedAppSet struct {
    ID                  string          `json:"id"`
    Name                string          `json:"name"`
    Namespace           string          `json:"namespace"`
    Tool                Tool            `json:"tool"`            // always "argocd" for now
    GeneratorTypes      []string        `json:"generatorTypes"`  // ["list", "git", "clusters"]
    TemplateSource      AppSource       `json:"templateSource"`
    TemplateDestination string          `json:"templateDestination"`
    Status              string          `json:"status"`          // Healthy, Error, Progressing
    StatusMessage       string          `json:"statusMessage"`
    GeneratedAppCount   int             `json:"generatedAppCount"`
    Summary             AppListMetadata `json:"summary"`
    PreserveOnDeletion  bool            `json:"preserveOnDeletion"`
    CreatedAt           string          `json:"createdAt"`
}

type AppSetCondition struct {
    Type    string `json:"type"`
    Status  string `json:"status"`
    Message string `json:"message"`
    Reason  string `json:"reason"`
}

type AppSetDetail struct {
    NormalizedAppSet
    Generators   []map[string]any  `json:"generators"`    // raw generator config
    Conditions   []AppSetCondition `json:"conditions"`
    Applications []NormalizedApp   `json:"applications"`  // child apps
}
```

### API

```
GET    /api/v1/gitops/applicationsets              → list all ApplicationSets
GET    /api/v1/gitops/applicationsets/{id}          → detail with generators + child apps
POST   /api/v1/gitops/applicationsets/{id}/refresh  → trigger re-generation (annotation patch)
DELETE /api/v1/gitops/applicationsets/{id}           → delete ApplicationSet
```

Create and edit use the existing YAML apply pipeline (`POST /api/v1/yaml/apply`).

### Backend Flow

```
Argo CD detected by discovery (explicit applicationsets CRD check)
  → WatchCRD for applicationsets GVR (WebSocket events)
  → Separate singleflight + cache for AppSet data
  → For each AppSet:
    ├── Normalize generators → type strings
    ├── Query child Applications via label selector
    ├── Aggregate sync/health into AppListMetadata
    └── Build NormalizedAppSet
  → RBAC filter: CanAccessGroupResource("argoproj.io", "applicationsets", ns)
```

### Edge Cases

| Scenario | Behavior |
|---|---|
| Argo CD not installed | ApplicationSets tab hidden |
| ApplicationSet CRD absent (old Argo CD) | Tab hidden; explicit CRD discovery |
| AppSet with 0 generated apps | Show "0 Applications" with status from conditions |
| Delete with preserveOnDeletion=false | Warning: "This will also delete N child Applications and their resources" |
| Delete with preserveOnDeletion=true | Info: "Child Applications will be preserved as standalone" |
| Matrix/merge generators | Show nested types: "matrix", "merge" |
| status.applicationStatus absent | Skip; only in Argo CD 2.12+ with progressive syncs |
| Create via YAML | Redirect to AppSet detail after successful apply |

---

## Implementation Phases

### Phase 1: Backend — Types, Normalizer, Handler

**Files to modify:**

| File | Change |
|---|---|
| `backend/internal/gitops/types.go` | Add `NormalizedAppSet`, `AppSetDetail`, `AppSetCondition` |
| `backend/internal/gitops/argocd.go` | Add `ArgoApplicationSetGVR`, `ListArgoAppSets()`, `NormalizeArgoAppSet()`, `GetArgoAppSetDetail()`, `RefreshArgoAppSet()`, `DeleteArgoAppSet()`, `detectGeneratorType()` |
| `backend/internal/gitops/handler.go` | Add separate appSetFetchGroup + cachedAppSets, `HandleListAppSets`, `HandleGetAppSet`, `HandleRefreshAppSet`, `HandleDeleteAppSet`, update `toolGVR` for `argo-as` |
| `backend/internal/gitops/discovery.go` | Add explicit `ApplicationSet` CRD check, expose `AppSetsAvailable` in ToolDetail |

**Key implementation:**

`NormalizeArgoAppSet()` extracts:
- Generator types via `detectGeneratorType()` (check single key in each generator map)
- Template source from `spec.template.spec.source`
- Status from conditions (ErrorOccurred=True → Error, ResourcesUpToDate=False → Progressing, else Healthy)
- `preserveResourcesOnDeletion` from `spec.syncPolicy`

`HandleListAppSets()` flow:
1. List ApplicationSets via separate singleflight cache
2. Fetch Applications from existing app cache (`fetchApps`)
3. Match child apps via label `argocd.argoproj.io/application-set-name`
4. Aggregate into `AppListMetadata` per AppSet
5. RBAC filter

`HandleDeleteAppSet()`:
- Uses `prepareAction()` with `"argo-as"` prefix (checks "delete" verb)
- Impersonating dynamic client
- Audit logged

`RefreshArgoAppSet()`:
- Patches annotation `argocd.argoproj.io/application-set-refresh: "true"`
- Same MergePatch pattern as existing actions

**Acceptance criteria:**
- [ ] All NormalizedAppSet fields populated correctly
- [ ] Generator type detection handles all 9 types + unknown
- [ ] Status derived from conditions correctly
- [ ] Child app count via label selector
- [ ] Separate singleflight cache, invalidated by CRD events
- [ ] Explicit CRD discovery for applicationsets
- [ ] `toolGVR("argo-as")` returns `"argoproj.io", "applicationsets"`
- [ ] Delete with impersonation + audit
- [ ] Refresh patches annotation
- [ ] `go vet` passes

---

### Phase 2: Routes, WebSocket, Wiring

**Files to modify:**

| File | Change |
|---|---|
| `backend/internal/server/routes.go` | Add 4 routes inside `registerGitOpsRoutes` |
| `backend/cmd/kubecenter/main.go` | Add `WatchCRD` for applicationsets GVR in Argo CD discovery callback |

**Routes:**
```go
gr.Get("/applicationsets", h.HandleListAppSets)
gr.Get("/applicationsets/{id}", h.HandleGetAppSet)
gr.Post("/applicationsets/{id}/refresh", h.HandleRefreshAppSet)
gr.Delete("/applicationsets/{id}", h.HandleDeleteAppSet)
```

**WebSocket wiring:**
```go
websocket.RegisterAllowedKind("applicationsets", "argoproj.io")
informerMgr.WatchCRD(ctx, ArgoApplicationSetGVR, "applicationsets", normalizer, eventCallback)
```

**Acceptance criteria:**
- [ ] Routes registered with rate limiter
- [ ] WebSocket events fire for ApplicationSet changes
- [ ] Both app and appset caches invalidated on events
- [ ] `go build ./...` passes

---

### Phase 3A: Frontend — Types, Routes, Nav

**Files to modify/create:**

| File | Change |
|---|---|
| `frontend/lib/gitops-types.ts` | Add TS types for NormalizedAppSet, AppSetDetail, AppSetCondition |
| `frontend/lib/constants.ts` | Add "ApplicationSets" tab to GitOps nav section |
| `frontend/islands/CommandPalette.tsx` | Add "View ApplicationSets" quick action |
| `frontend/routes/gitops/applicationsets.tsx` | List route |
| `frontend/routes/gitops/applicationsets/[id].tsx` | Detail route |

**Acceptance criteria:**
- [ ] TS types mirror backend types
- [ ] Nav tab appears when Argo CD detected
- [ ] Routes render placeholder islands

---

### Phase 3B: Frontend — List + Detail Islands

**Files to create:**

| File | Purpose |
|---|---|
| `frontend/islands/GitOpsAppSets.tsx` | List view: table with generator badges, sync summary, link to detail |
| `frontend/islands/GitOpsAppSetDetail.tsx` | Detail: generators, template, child apps, conditions, actions |

**GitOpsAppSets island:**
- Fetches `GET /v1/gitops/applicationsets`
- WebSocket subscription for `applicationsets` kind
- Table: Name, Generator(s) badges, Apps count, Sync Summary, Health Summary, Status, Age
- Rows link to `/gitops/applicationsets/{id}`
- "Create ApplicationSet" button → `/tools/yaml-apply` (YAML editor)

**GitOpsAppSetDetail island:**
- Fetches `GET /v1/gitops/applicationsets/{id}`
- Header: name, namespace, status badge
- Action buttons: Refresh, Edit (YAML), Delete
- Generators panel: cards per generator with type badge + raw config (collapsible YAML)
- Template panel: source repo, path, destination
- Child Applications table: Name, Sync, Health, Revision, Dest NS (rows link to existing app detail)
- Conditions panel: collapsible table
- Delete confirmation: shows child app count + preserve-on-deletion status with clear warning

**Edit action:** Fetches the raw YAML of the ApplicationSet, opens it in the YAML editor page (`/tools/yaml-apply`) with the content pre-filled. Uses existing YAML apply infrastructure for SSA.

**Acceptance criteria:**
- [ ] List renders with generator type badges
- [ ] Sync/health summary aggregated from child apps
- [ ] Detail shows generators, template, conditions, and child apps
- [ ] Child app rows link to existing `/gitops/applications/{id}`
- [ ] Refresh button patches annotation
- [ ] Delete button shows confirmation with child app count
- [ ] Delete warning differs based on preserveOnDeletion
- [ ] WebSocket live updates
- [ ] Theme-compliant
- [ ] `deno lint` and `deno fmt --check` pass

---

## File Summary

### New Files (4)

| File | Phase |
|---|---|
| `frontend/islands/GitOpsAppSets.tsx` | 3B |
| `frontend/islands/GitOpsAppSetDetail.tsx` | 3B |
| `frontend/routes/gitops/applicationsets.tsx` | 3A |
| `frontend/routes/gitops/applicationsets/[id].tsx` | 3A |

### Modified Files (8)

| File | Phase | Change |
|---|---|---|
| `backend/internal/gitops/types.go` | 1 | New types |
| `backend/internal/gitops/argocd.go` | 1 | GVR, normalizer, actions |
| `backend/internal/gitops/handler.go` | 1 | 4 handlers, cache, toolGVR |
| `backend/internal/gitops/discovery.go` | 1 | Explicit AppSet CRD check |
| `backend/internal/server/routes.go` | 2 | 4 routes |
| `backend/cmd/kubecenter/main.go` | 2 | WatchCRD |
| `frontend/lib/gitops-types.ts` | 3A | TS types |
| `frontend/lib/constants.ts` | 3A | Nav tab |
| `frontend/islands/CommandPalette.tsx` | 3A | Quick action |

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Delete cascades to all child apps | Data loss | Confirmation dialog with count + preserve status |
| AppSet CRD absent in old Argo CD | Tab shows but 404s | Explicit CRD discovery check |
| Large AppSets (100+ child apps) | Slow detail | Client-side truncation at 200 |
| Edit via YAML may break generators | Invalid config | Existing YAML validate endpoint catches schema errors |

## Future Enhancements

- ApplicationSet creation wizard (generator-specific forms)
- Rolling sync progress visualization (Argo CD 2.12+)
- Generator parameter preview (show computed values)
- Bulk actions on child Applications

## References

### Internal
- GitOps types: `backend/internal/gitops/types.go`
- Argo CD adapter: `backend/internal/gitops/argocd.go`
- GitOps handler: `backend/internal/gitops/handler.go`
- CRD discovery: `backend/internal/gitops/discovery.go`
- WebSocket wiring: `backend/cmd/kubecenter/main.go:460-509`
- YAML apply: `backend/internal/yaml/applier.go`
- Frontend patterns: `frontend/islands/GitOpsApplications.tsx`, `frontend/islands/GitOpsAppDetail.tsx`

### External
- [Argo CD ApplicationSet Docs](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/)
- [ApplicationSet Generators](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/Generators/)
