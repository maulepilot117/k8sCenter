# Handler Adapter Refactor — Design Spec

**Date:** 2026-04-16
**Status:** Approved
**Parent:** `plans/cleanup-2026-04-15-plan.md` (Tier 3)
**Approach:** Interface + Registration (Approach B)

## Problem

35+ handler files in `backend/internal/k8s/resources/` repeat 75-90% identical
boilerplate for CRUD operations. Each file reimplements auth, RBAC, param
parsing, error handling, audit logging, and response writing around a 2-4 line
resource-specific clientset call. Adding a new resource requires copy-pasting
~100-380 lines and changing type names.

## Solution

Replace per-resource handler functions with a shared CRUD dispatcher that
delegates to resource-specific adapters via Go interfaces.

## Core Interfaces

### ResourceAdapter

```go
// adapter.go

type ResourceAdapter interface {
    Kind() string
    APIResource() string
    ClusterScoped() bool
    ListFromCache(inf InformerSet, ns string, sel labels.Selector) ([]any, error)
    GetFromCache(inf InformerSet, ns, name string) (any, error)
    Create(cs kubernetes.Interface, ns string, body []byte) (any, error)
    Update(cs kubernetes.Interface, ns string, body []byte) (any, error)
    Delete(cs kubernetes.Interface, ns, name string) error
}
```

### Capability Interfaces (optional, checked via type assertion)

```go
type Scalable interface {
    Scale(cs kubernetes.Interface, ns, name string, replicas int32) error
}

type Restartable interface {
    Restart(cs kubernetes.Interface, ns, name string) error
}

type Suspendable interface {
    Suspend(cs kubernetes.Interface, ns, name string, suspend bool) error
}

type Triggerable interface {
    Trigger(cs kubernetes.Interface, ns, name string) error
}

type Rollbackable interface {
    Rollback(cs kubernetes.Interface, ns, name string, revision int64) error
}
```

### Registry

```go
// registry.go

var registry = map[string]ResourceAdapter{}

func Register(a ResourceAdapter)                    { registry[a.Kind()] = a }
func GetAdapter(kind string) (ResourceAdapter, bool) { a, ok := registry[kind]; return a, ok }
```

## Shared CRUD Handler

```go
// crud.go

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
    kind := chi.URLParam(r, "kind")
    adapter, ok := GetAdapter(kind)
    if !ok { writeError(w, 404, "unknown resource kind", kind); return }

    user, ok := requireUser(w, r)
    if !ok { return }

    ns := chi.URLParam(r, "namespace")
    if !h.checkAccess(w, r, user, "list", adapter.APIResource(), ns) { return }

    params := parseListParams(r)
    sel, ok := parseSelectorOrReject(w, params.LabelSelector)
    if !ok { return }

    items, err := adapter.ListFromCache(h.Informers, ns, sel)
    if err != nil { mapK8sError(w, err, "list", kind, ns, ""); return }

    page := paginate(items, params)
    writeList(w, page, len(items))
}
```

HandleGet, HandleCreate, HandleUpdate, HandleDelete follow the same pattern.
Action handlers (HandleScale, HandleRestart, etc.) check capability interfaces
via type assertion:

```go
func (h *Handler) HandleScale(w http.ResponseWriter, r *http.Request) {
    adapter, ok := GetAdapter(chi.URLParam(r, "kind"))
    // ...
    scalable, ok := adapter.(Scalable)
    if !ok { writeError(w, 400, kind+" does not support scaling", ""); return }
    // ... auth, decode, call scalable.Scale(), audit, respond
}
```

## Routing

Routes change from per-resource to generic:

```go
// Before: ~70 routes
r.Get("/deployments/{namespace}", h.HandleListDeployments)
r.Get("/pods/{namespace}", h.HandleListPods)
// ... 28 more per kind

// After: ~10 routes
r.Get("/{kind}", h.HandleList)
r.Get("/{kind}/{namespace}", h.HandleList)
r.Get("/{kind}/{namespace}/{name}", h.HandleGet)
r.Post("/{kind}/{namespace}", h.HandleCreate)
r.Put("/{kind}/{namespace}/{name}", h.HandleUpdate)
r.Delete("/{kind}/{namespace}/{name}", h.HandleDelete)
r.Post("/{kind}/{namespace}/{name}/scale", h.HandleScale)
r.Post("/{kind}/{namespace}/{name}/restart", h.HandleRestart)
r.Post("/{kind}/{namespace}/{name}/suspend", h.HandleSuspend)
r.Post("/{kind}/{namespace}/{name}/trigger", h.HandleTrigger)
r.Post("/{kind}/{namespace}/{name}/rollback", h.HandleRollback)
```

## Adapter Example (Deployment)

```go
// adapter_deployments.go

type deploymentAdapter struct{}

func (a deploymentAdapter) Kind() string         { return "deployments" }
func (a deploymentAdapter) APIResource() string  { return "deployments" }
func (a deploymentAdapter) ClusterScoped() bool  { return false }

func (a deploymentAdapter) ListFromCache(inf InformerSet, ns string, sel labels.Selector) ([]any, error) {
    items, err := inf.Deployments(ns).List(sel)
    if err != nil { return nil, err }
    out := make([]any, len(items))
    for i, item := range items { out[i] = item }
    return out, nil
}

func (a deploymentAdapter) GetFromCache(inf InformerSet, ns, name string) (any, error) {
    return inf.Deployments(ns).Get(name)
}

func (a deploymentAdapter) Create(cs kubernetes.Interface, ns string, body []byte) (any, error) {
    var obj appsv1.Deployment
    if err := json.Unmarshal(body, &obj); err != nil { return nil, err }
    return cs.AppsV1().Deployments(ns).Create(context.TODO(), &obj, metav1.CreateOptions{})
}

func (a deploymentAdapter) Update(cs kubernetes.Interface, ns string, body []byte) (any, error) {
    var obj appsv1.Deployment
    if err := json.Unmarshal(body, &obj); err != nil { return nil, err }
    return cs.AppsV1().Deployments(ns).Update(context.TODO(), &obj, metav1.UpdateOptions{})
}

func (a deploymentAdapter) Delete(cs kubernetes.Interface, ns, name string) error {
    return cs.AppsV1().Deployments(ns).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

// Deployment supports scale, restart, rollback
func (a deploymentAdapter) Scale(cs kubernetes.Interface, ns, name string, replicas int32) error { ... }
func (a deploymentAdapter) Restart(cs kubernetes.Interface, ns, name string) error { ... }
func (a deploymentAdapter) Rollback(cs kubernetes.Interface, ns, name string, revision int64) error { ... }

func init() { Register(deploymentAdapter{}) }
```

~45 lines vs the current ~380 lines.

## What Does NOT Change

- `handler.go` shared struct and helper methods (requireUser, checkAccess, etc.)
- `errors.go` mapK8sError
- `pods.go` HandlePodExec / HandlePodLogs (90% custom WebSocket/SPDY logic)
- `crd_handler.go` (uses dynamic client, separate pattern)

## Migration Strategy

Six phases, each its own PR with `/review` + CI + E2E gate:

| Phase | Scope | Files | Risk |
|-------|-------|-------|------|
| 1. Foundation | Create adapter.go, registry.go, crud.go. Wire generic routes alongside old routes. No deletions. | 3 new | None |
| 2. Pilot (5) | configmaps, secrets, namespaces, serviceaccounts, endpoints | 5 delete + 5 new | Low |
| 3. Bulk (15) | deployments, services, replicasets, statefulsets, daemonsets, jobs, cronjobs, ingresses, networkpolicies, pvcs, pvs, storageclasses, hpas, pdbs, limitranges | 15 delete + 15 new | Medium |
| 4. RBAC + remaining | roles, clusterroles, rolebindings, clusterrolebindings, resourcequotas, endpointslices, events | 7 delete + 7 new | Low |
| 5. Actions | Wire shared scale/restart/suspend/trigger/rollback handlers. Delete per-resource action code. | ~10 modified | Medium |
| 6. Cleanup | Remove old route registrations, dead code, update docs | ~5 modified | Low |

**Rollback:** Revert one PR; old handlers come back. Old and new routes coexist
until the old handler file is deleted — no flag day.

**E2E safety net:** Playwright tests hit HTTP endpoints, not internal code. They
don't know about the refactor and serve as regression detection.

## Expected Outcome

- ~1200 LOC deleted across 35 handler files
- Adding a new resource = one 25-45 line adapter file with `init()` registration
- 5 generic CRUD routes + 5-6 action routes replace ~70 per-resource routes
- Shared error handling, audit logging, and RBAC enforcement in one place
