# Handler Adapter Refactor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace 35+ per-resource handler files with a shared CRUD dispatcher + adapter registration pattern, reducing ~1200 LOC and making new resources a 25-45 line declaration.

**Architecture:** Define a `ResourceAdapter` interface with CRUD methods returning `any`. Each k8s resource registers an adapter via `init()`. A shared `CRUDHandler` dispatches based on `{kind}` URL param, handling auth/RBAC/audit/errors once. Optional capability interfaces (Scalable, Restartable, etc.) handle action endpoints.

**Tech Stack:** Go 1.26, chi router, client-go informers + typed clientsets, existing Handler struct + helpers.

---

## File Map

### New files (in `backend/internal/k8s/resources/`)
| File | Responsibility |
|------|---------------|
| `adapter.go` | ResourceAdapter interface + capability interfaces |
| `registry.go` | Adapter registration map + lookup |
| `crud.go` | Shared HandleList/Get/Create/Update/Delete dispatchers |
| `actions.go` | Shared HandleScale/Restart/Suspend/Trigger/Rollback dispatchers |
| `adapter_configmaps.go` | ConfigMap adapter |
| `adapter_secrets.go` | Secret adapter |
| `adapter_namespaces.go` | Namespace adapter (cluster-scoped) |
| `adapter_serviceaccounts.go` | ServiceAccount adapter |
| `adapter_endpoints.go` | Endpoints adapter |
| `adapter_deployments.go` | Deployment adapter + Scalable + Restartable + Rollbackable |
| `adapter_statefulsets.go` | StatefulSet adapter + Scalable + Restartable |
| `adapter_daemonsets.go` | DaemonSet adapter + Restartable |
| `adapter_services.go` | Service adapter |
| `adapter_ingresses.go` | Ingress adapter |
| `adapter_replicasets.go` | ReplicaSet adapter (read-only) |
| `adapter_jobs.go` | Job adapter + Suspendable |
| `adapter_cronjobs.go` | CronJob adapter + Suspendable + Triggerable |
| `adapter_networkpolicies.go` | NetworkPolicy adapter |
| `adapter_pvcs.go` | PVC adapter |
| `adapter_pvs.go` | PV adapter (cluster-scoped, read-only) |
| `adapter_storageclasses.go` | StorageClass adapter (cluster-scoped, read-only) |
| `adapter_hpas.go` | HPA adapter |
| `adapter_pdbs.go` | PDB adapter |
| `adapter_limitranges.go` | LimitRange adapter (read-only) |
| `adapter_resourcequotas.go` | ResourceQuota adapter (read-only) |
| `adapter_events.go` | Event adapter (read-only) |
| `adapter_endpointslices.go` | EndpointSlice adapter (read-only) |
| `adapter_roles.go` | Role adapter (read-only) |
| `adapter_clusterroles.go` | ClusterRole adapter (cluster-scoped, read-only) |
| `adapter_rolebindings.go` | RoleBinding adapter |
| `adapter_clusterrolebindings.go` | ClusterRoleBinding adapter (cluster-scoped) |
| `adapter_webhooks.go` | ValidatingWebhookConfiguration + MutatingWebhookConfiguration adapters (cluster-scoped, read-only) |
| `adapter_test.go` | Unit tests for adapter registry + CRUD dispatch |

### Modified files
| File | Change |
|------|--------|
| `backend/internal/server/routes.go` | Replace `registerResourceEndpoints` with generic routes |

### Deleted files (after migration)
All existing per-resource handler files: `configmaps.go`, `secrets.go`, `namespaces.go`, `deployments.go`, `statefulsets.go`, `daemonsets.go`, `pods.go`, `services.go`, `ingresses.go`, `replicasets.go`, `jobs.go`, `networkpolicies.go`, `pvcs.go`, `pvs.go`, `storageclasses.go`, `hpas.go`, `pdbs.go`, `limitranges.go`, `resourcequotas.go`, `serviceaccounts.go`, `endpoints.go`, `endpointslices.go`, `events.go`, `rbac.go`, `webhooks.go`

### Untouched files
| File | Reason |
|------|--------|
| `handler.go` | Shared Handler struct, helpers — used by CRUD dispatcher |
| `errors.go` | mapK8sError — used by CRUD dispatcher |
| `pods.go` | HandlePodExec + HandlePodLogs stay (90% custom). Pod CRUD moves to adapter. |
| `nodes.go` | Cordon/Uncordon/Drain are node-specific. Node List/Get moves to adapter. |
| `crd_handler.go` | Uses dynamic client, separate pattern |
| `cilium.go` | Uses dynamic client, Cilium-specific |
| `counts.go` | Dashboard batch endpoint |
| `dashboard.go` | Aggregated summary endpoint |
| `tasks.go` | Async task management |
| `access.go` | AccessChecker |

---

### Task 1: Core Interfaces — adapter.go

**Files:**
- Create: `backend/internal/k8s/resources/adapter.go`

- [ ] **Step 1: Create adapter.go with all interfaces**

```go
package resources

import (
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ResourceAdapter provides CRUD operations for a single k8s resource kind.
type ResourceAdapter interface {
	Kind() string
	APIResource() string
	DisplayName() string
	ClusterScoped() bool
	ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error)
	GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error)
	Create(cs kubernetes.Interface, ns string, body []byte) (any, error)
	Update(cs kubernetes.Interface, ns string, name string, body []byte) (any, error)
	Delete(cs kubernetes.Interface, ns, name string) error
}

// ReadOnlyAdapter is an adapter that only supports List and Get.
// Create/Update/Delete return errors.
type ReadOnlyAdapter struct{}

func (ReadOnlyAdapter) Create(_ kubernetes.Interface, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}
func (ReadOnlyAdapter) Update(_ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}
func (ReadOnlyAdapter) Delete(_ kubernetes.Interface, _, _ string) error {
	return errReadOnly
}

var errReadOnly = &readOnlyError{}

type readOnlyError struct{}

func (e *readOnlyError) Error() string { return "resource is read-only" }

// Scalable resources support scale sub-resource (Deployments, StatefulSets).
type Scalable interface {
	Scale(cs kubernetes.Interface, ns, name string, replicas int32) error
}

// Restartable resources support rolling restart via annotation patch.
type Restartable interface {
	Restart(cs kubernetes.Interface, ns, name string) error
}

// Suspendable resources support suspend/resume (Jobs, CronJobs).
type Suspendable interface {
	Suspend(cs kubernetes.Interface, ns, name string, suspend bool) error
}

// Triggerable resources support manual trigger (CronJobs).
type Triggerable interface {
	Trigger(cs kubernetes.Interface, ns, name string) error
}

// Rollbackable resources support rollback to a previous revision (Deployments).
type Rollbackable interface {
	Rollback(cs kubernetes.Interface, ns, name string, revision int64) error
}
```

Note: `InformerSet` here refers to the existing `*k8s.InformerManager` type on Handler. The CRUD handler accesses it as `h.Informers`. The adapter methods receive it so they can call the correct informer.

- [ ] **Step 2: Verify it compiles**

Run: `cd backend && go build ./internal/k8s/resources/`
Expected: success (no consumers yet)

- [ ] **Step 3: Commit**

```bash
git add backend/internal/k8s/resources/adapter.go
git commit -m "refactor(resources): add ResourceAdapter interface + capability interfaces"
```

---

### Task 2: Registry — registry.go

**Files:**
- Create: `backend/internal/k8s/resources/registry.go`

- [ ] **Step 1: Create registry.go**

```go
package resources

import "fmt"

var registry = map[string]ResourceAdapter{}

// Register adds an adapter to the global registry. Called from adapter init() functions.
func Register(a ResourceAdapter) {
	if _, exists := registry[a.Kind()]; exists {
		panic(fmt.Sprintf("resources: duplicate adapter registration for kind %q", a.Kind()))
	}
	registry[a.Kind()] = a
}

// GetAdapter returns the adapter for a kind, or (nil, false) if not registered.
func GetAdapter(kind string) (ResourceAdapter, bool) {
	a, ok := registry[kind]
	return a, ok
}

// RegisteredKinds returns all registered kind strings (for debugging/tests).
func RegisteredKinds() []string {
	kinds := make([]string, 0, len(registry))
	for k := range registry {
		kinds = append(kinds, k)
	}
	return kinds
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd backend && go build ./internal/k8s/resources/`

- [ ] **Step 3: Commit**

```bash
git add backend/internal/k8s/resources/registry.go
git commit -m "refactor(resources): add adapter registry"
```

---

### Task 3: Shared CRUD Handler — crud.go

**Files:**
- Create: `backend/internal/k8s/resources/crud.go`

- [ ] **Step 1: Create crud.go with all 5 CRUD dispatch methods**

```go
package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
)

// HandleListResource dispatches list operations for any registered resource.
func (h *Handler) HandleListResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	ns := params.Namespace
	if adapter.ClusterScoped() {
		ns = ""
	}
	if !h.checkAccess(w, r, user, "list", adapter.APIResource(), ns) {
		return
	}

	items, err := adapter.ListFromCache(h.Informers, ns, sel)
	if err != nil {
		mapK8sError(w, err, "list", adapter.DisplayName(), ns, "")
		return
	}

	page, cont := paginateAny(items, params.Limit, params.Continue)
	writeList(w, page, len(items), cont)
}

// HandleGetResource dispatches get operations for any registered resource.
func (h *Handler) HandleGetResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if adapter.ClusterScoped() {
		// For cluster-scoped resources, the first path segment is the name
		if name == "" {
			name = ns
			ns = ""
		}
	}

	if !h.checkAccess(w, r, user, "get", adapter.APIResource(), ns) {
		return
	}

	obj, err := adapter.GetFromCache(h.Informers, ns, name)
	if err != nil {
		mapK8sError(w, err, "get", adapter.DisplayName(), ns, name)
		return
	}
	writeData(w, obj)
}

// HandleCreateResource dispatches create operations.
func (h *Handler) HandleCreateResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", adapter.APIResource(), ns) {
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	created, err := adapter.Create(cs, ns, body)
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, "", audit.ResultFailure)
		mapK8sError(w, err, "create", adapter.DisplayName(), ns, "")
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, "", audit.ResultSuccess)
	writeCreated(w, created)
}

// HandleUpdateResource dispatches update operations.
func (h *Handler) HandleUpdateResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	updated, err := adapter.Update(cs, ns, name, body)
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", adapter.DisplayName(), ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

// HandleDeleteResource dispatches delete operations.
func (h *Handler) HandleDeleteResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if adapter.ClusterScoped() && name == "" {
		name = ns
		ns = ""
	}

	if !h.checkAccess(w, r, user, "delete", adapter.APIResource(), ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := adapter.Delete(cs, ns, name); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", adapter.DisplayName(), ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// readBody reads and limits the request body, writing an error if it fails.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body", err.Error())
		return nil, err
	}
	return data, nil
}

// paginateAny is a variant of paginate that works with []any.
func paginateAny(items []any, limit int, continueToken string) ([]any, string) {
	start := 0
	if continueToken != "" {
		if idx, err := strconv.Atoi(continueToken); err == nil && idx > 0 && idx < len(items) {
			start = idx
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	var cont string
	if end < len(items) {
		cont = strconv.Itoa(end)
	}
	return items[start:end], cont
}
```

Add the necessary imports (`io`, `strconv`) at the top.

- [ ] **Step 2: Verify it compiles**

Run: `cd backend && go build ./internal/k8s/resources/`

- [ ] **Step 3: Commit**

```bash
git add backend/internal/k8s/resources/crud.go
git commit -m "refactor(resources): add shared CRUD dispatch handler"
```

---

### Task 4: Shared Action Handlers — actions.go

**Files:**
- Create: `backend/internal/k8s/resources/actions.go`

- [ ] **Step 1: Create actions.go**

```go
package resources

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
)

func (h *Handler) HandleScaleResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}
	scalable, ok := adapter.(Scalable)
	if !ok {
		writeError(w, http.StatusBadRequest, kind+" does not support scaling", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	var req struct{ Replicas int32 `json:"replicas"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := scalable.Scale(cs, ns, name, req.Replicas); err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "scale", adapter.DisplayName(), ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, map[string]any{"scaled": true, "replicas": req.Replicas})
}

func (h *Handler) HandleRestartResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}
	restartable, ok := adapter.(Restartable)
	if !ok {
		writeError(w, http.StatusBadRequest, kind+" does not support restart", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := restartable.Restart(cs, ns, name); err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "restart", adapter.DisplayName(), ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, map[string]string{"status": "restarting"})
}

func (h *Handler) HandleSuspendResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}
	suspendable, ok := adapter.(Suspendable)
	if !ok {
		writeError(w, http.StatusBadRequest, kind+" does not support suspend", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	var req struct{ Suspend bool `json:"suspend"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := suspendable.Suspend(cs, ns, name, req.Suspend); err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "suspend", adapter.DisplayName(), ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, map[string]bool{"suspended": req.Suspend})
}

func (h *Handler) HandleTriggerResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}
	triggerable, ok := adapter.(Triggerable)
	if !ok {
		writeError(w, http.StatusBadRequest, kind+" does not support trigger", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "create", adapter.APIResource(), ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := triggerable.Trigger(cs, ns, name); err != nil {
		h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "trigger", adapter.DisplayName(), ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, map[string]string{"status": "triggered"})
}

func (h *Handler) HandleRollbackResource(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	adapter, ok := GetAdapter(kind)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown resource kind: "+kind, "")
		return
	}
	rollbackable, ok := adapter.(Rollbackable)
	if !ok {
		writeError(w, http.StatusBadRequest, kind+" does not support rollback", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	var req struct{ Revision int64 `json:"revision"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := rollbackable.Rollback(cs, ns, name, req.Revision); err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "rollback", adapter.DisplayName(), ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, map[string]string{"status": "rolled back"})
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd backend && go build ./internal/k8s/resources/`

- [ ] **Step 3: Commit**

```bash
git add backend/internal/k8s/resources/actions.go
git commit -m "refactor(resources): add shared action dispatch handlers"
```

---

### Task 5: Pilot Adapters — 5 simple resources

**Files:**
- Create: `backend/internal/k8s/resources/adapter_configmaps.go`
- Create: `backend/internal/k8s/resources/adapter_secrets.go`
- Create: `backend/internal/k8s/resources/adapter_namespaces.go`
- Create: `backend/internal/k8s/resources/adapter_serviceaccounts.go`
- Create: `backend/internal/k8s/resources/adapter_endpoints.go`

- [ ] **Step 1: Create adapter_configmaps.go**

Read the current `configmaps.go` to get exact informer and clientset API paths. Then write:

```go
package resources

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

type configMapAdapter struct{}

func (configMapAdapter) Kind() string         { return "configmaps" }
func (configMapAdapter) APIResource() string  { return "configmaps" }
func (configMapAdapter) DisplayName() string  { return "ConfigMap" }
func (configMapAdapter) ClusterScoped() bool  { return false }

func (configMapAdapter) ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error) {
	var items []*corev1.ConfigMap
	var err error
	if ns != "" {
		items, err = inf.ConfigMaps().ConfigMaps(ns).List(sel)
	} else {
		items, err = inf.ConfigMaps().List(sel)
	}
	if err != nil {
		return nil, err
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out, nil
}

func (configMapAdapter) GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error) {
	return inf.ConfigMaps().ConfigMaps(ns).Get(name)
}

func (configMapAdapter) Create(cs kubernetes.Interface, ns string, body []byte) (any, error) {
	var obj corev1.ConfigMap
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	return cs.CoreV1().ConfigMaps(ns).Create(context.TODO(), &obj, metav1.CreateOptions{})
}

func (configMapAdapter) Update(cs kubernetes.Interface, ns, name string, body []byte) (any, error) {
	var obj corev1.ConfigMap
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj.Namespace = ns
	obj.Name = name
	return cs.CoreV1().ConfigMaps(ns).Update(context.TODO(), &obj, metav1.UpdateOptions{})
}

func (configMapAdapter) Delete(cs kubernetes.Interface, ns, name string) error {
	return cs.CoreV1().ConfigMaps(ns).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

func init() { Register(configMapAdapter{}) }
```

- [ ] **Step 2: Create the remaining 4 pilot adapters**

Follow the exact same pattern for `adapter_secrets.go`, `adapter_namespaces.go` (cluster-scoped), `adapter_serviceaccounts.go` (read-only), `adapter_endpoints.go` (read-only). Read each current handler file to get the correct informer paths and clientset API calls.

For read-only adapters, embed `ReadOnlyAdapter`:

```go
type serviceAccountAdapter struct{ ReadOnlyAdapter }
// Only implement Kind, APIResource, DisplayName, ClusterScoped, ListFromCache, GetFromCache
```

For cluster-scoped (namespaces): `ClusterScoped() bool { return true }`, and adjust ListFromCache to not take namespace.

- [ ] **Step 3: Verify all 5 adapters compile**

Run: `cd backend && go build ./internal/k8s/resources/`

- [ ] **Step 4: Write adapter_test.go — verify registration**

```go
package resources

import "testing"

func TestPilotAdaptersRegistered(t *testing.T) {
	for _, kind := range []string{"configmaps", "secrets", "namespaces", "serviceaccounts", "endpoints"} {
		if _, ok := GetAdapter(kind); !ok {
			t.Errorf("adapter not registered for kind %q", kind)
		}
	}
}
```

- [ ] **Step 5: Run tests**

Run: `cd backend && go test ./internal/k8s/resources/ -run TestPilotAdaptersRegistered -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/internal/k8s/resources/adapter_*.go backend/internal/k8s/resources/adapter_test.go
git commit -m "refactor(resources): add 5 pilot resource adapters (configmaps, secrets, namespaces, serviceaccounts, endpoints)"
```

---

### Task 6: Wire Generic Routes + Delete Pilot Old Handlers

**Files:**
- Modify: `backend/internal/server/routes.go:637-820` (registerResourceEndpoints)
- Delete: `backend/internal/k8s/resources/configmaps.go`
- Delete: `backend/internal/k8s/resources/endpoints.go`

- [ ] **Step 1: Add generic routes to registerResourceEndpoints**

In `routes.go`, inside `registerResourceEndpoints`, add a new block at the TOP before all per-resource routes:

```go
// Generic adapter-based routes (Phase 2+)
ar.Get("/resources/{kind}", h.HandleListResource)
ar.Get("/resources/{kind}/{namespace}", h.HandleListResource)
ar.Get("/resources/{kind}/{namespace}/{name}", h.HandleGetResource)
ar.Post("/resources/{kind}/{namespace}", h.HandleCreateResource)
ar.Put("/resources/{kind}/{namespace}/{name}", h.HandleUpdateResource)
ar.Delete("/resources/{kind}/{namespace}/{name}", h.HandleDeleteResource)

// Generic action routes
ar.Post("/resources/{kind}/{namespace}/{name}/scale", h.HandleScaleResource)
ar.Post("/resources/{kind}/{namespace}/{name}/restart", h.HandleRestartResource)
ar.Post("/resources/{kind}/{namespace}/{name}/suspend", h.HandleSuspendResource)
ar.Post("/resources/{kind}/{namespace}/{name}/trigger", h.HandleTriggerResource)
ar.Post("/resources/{kind}/{namespace}/{name}/rollback", h.HandleRollbackResource)
```

**Important:** chi routes are matched in order. The per-resource routes (`/resources/deployments/...`) are more specific than the generic `{kind}` route, so they take priority. This means old handlers still work during migration.

- [ ] **Step 2: Remove pilot resource routes from per-resource block**

Delete the configmaps, secrets, namespaces, serviceaccounts, and endpoints route blocks from `registerResourceEndpoints` (lines ~704-761 and ~790-803). The generic `{kind}` routes now serve them.

- [ ] **Step 3: Delete old handler files for pilot resources**

```bash
rm backend/internal/k8s/resources/configmaps.go
rm backend/internal/k8s/resources/endpoints.go
rm backend/internal/k8s/resources/serviceaccounts.go
```

Note: `namespaces.go` and `secrets.go` may have custom handlers (HandleRevealSecret, HandleCreateNamespace with no-namespace path). Check before deleting — keep custom handlers, only delete if fully migrated.

- [ ] **Step 4: Verify compilation + tests**

Run: `cd backend && go build ./... && go test ./internal/k8s/resources/ -v && go vet ./...`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(resources): wire generic routes, delete 5 pilot handler files"
```

---

### Task 7: Bulk Adapters — 15 medium-complexity resources

**Files:**
- Create: `adapter_deployments.go`, `adapter_statefulsets.go`, `adapter_daemonsets.go`, `adapter_services.go`, `adapter_ingresses.go`, `adapter_replicasets.go`, `adapter_jobs.go`, `adapter_cronjobs.go`, `adapter_networkpolicies.go`, `adapter_pvcs.go`, `adapter_pvs.go`, `adapter_storageclasses.go`, `adapter_hpas.go`, `adapter_pdbs.go`, `adapter_limitranges.go`

This is the largest task. Use parallel sub-agents — 5 adapters per agent.

- [ ] **Step 1: Read each current handler file to map informer + clientset paths**

For each resource, note: informer accessor (e.g., `h.Informers.Deployments()`), clientset API group (e.g., `cs.AppsV1().Deployments(ns)`), which capability interfaces it needs (Scalable, Restartable, etc.), and any unique action logic.

- [ ] **Step 2: Write all 15 adapter files**

Follow the exact pattern from Task 5 Step 1. For resources with actions:

**Deployments:** implements Scalable, Restartable, Rollbackable
- `Scale`: get/update scale sub-resource via `cs.AppsV1().Deployments(ns).GetScale/UpdateScale`
- `Restart`: patch template annotation via `cs.AppsV1().Deployments(ns).Patch` with `restartPatch()`
- `Rollback`: patch rollback annotation (get current RS, find target revision RS, patch Deployment template)

**StatefulSets:** implements Scalable, Restartable
**DaemonSets:** implements Restartable
**Jobs:** implements Suspendable (patch spec.suspend)
**CronJobs:** implements Suspendable, Triggerable (create Job from template)

Read the existing handler action code in each file and move it into the adapter method.

- [ ] **Step 3: Verify compilation**

Run: `cd backend && go build ./internal/k8s/resources/`

- [ ] **Step 4: Add registration test**

Extend `adapter_test.go`:

```go
func TestAllAdaptersRegistered(t *testing.T) {
	expected := []string{
		"configmaps", "secrets", "namespaces", "serviceaccounts", "endpoints",
		"deployments", "statefulsets", "daemonsets", "services", "ingresses",
		"replicasets", "jobs", "cronjobs", "networkpolicies", "pvcs",
		"pvs", "storageclasses", "hpas", "pdbs", "limitranges",
	}
	for _, kind := range expected {
		if _, ok := GetAdapter(kind); !ok {
			t.Errorf("adapter not registered for kind %q", kind)
		}
	}
}
```

- [ ] **Step 5: Run tests**

Run: `cd backend && go test ./internal/k8s/resources/ -v`

- [ ] **Step 6: Remove old routes + delete old handler files**

Delete the per-resource route blocks for all 15 resources from `registerResourceEndpoints`. Delete the old handler files.

- [ ] **Step 7: Full build + vet + test**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(resources): migrate 15 resources to adapter pattern, delete old handlers"
```

---

### Task 8: RBAC + Remaining Adapters — 7 resources

**Files:**
- Create: `adapter_roles.go`, `adapter_clusterroles.go`, `adapter_rolebindings.go`, `adapter_clusterrolebindings.go`, `adapter_resourcequotas.go`, `adapter_endpointslices.go`, `adapter_events.go`, `adapter_webhooks.go`

- [ ] **Step 1: Write adapters**

All are read-only except RoleBindings and ClusterRoleBindings (have Create/Update/Delete). ClusterRoles and ClusterRoleBindings are cluster-scoped. Webhooks (Validating + Mutating) are cluster-scoped and can share a file with two adapters registered via separate `init()` calls.

- [ ] **Step 2: Remove old routes + delete old handler files**

Delete `rbac.go`, `webhooks.go`, `events.go`, `endpointslices.go`, `resourcequotas.go`, `limitranges.go` (if not already deleted in Task 7).

- [ ] **Step 3: Full build + vet + test**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor(resources): migrate RBAC + remaining resources to adapter pattern"
```

---

### Task 9: Clean Up routes.go + Residual Handlers

**Files:**
- Modify: `backend/internal/server/routes.go`
- Modify: `backend/internal/k8s/resources/pods.go` (keep only HandlePodExec, HandlePodLogs, HandlePodDelete if custom)
- Modify: `backend/internal/k8s/resources/nodes.go` (keep only Cordon/Uncordon/Drain)

- [ ] **Step 1: Slim down registerResourceEndpoints**

After all adapters are migrated, `registerResourceEndpoints` should contain only:
1. The generic routes (from Task 6)
2. Specialty routes that don't fit the adapter pattern:
   - `GET /resources/pods/{ns}/{name}/logs` → `h.HandlePodLogs`
   - `GET /resources/secrets/{ns}/{name}/reveal/{key}` → `h.HandleRevealSecret`
   - `POST /resources/nodes/{name}/cordon` → `h.HandleCordonNode`
   - `POST /resources/nodes/{name}/uncordon` → `h.HandleUncordonNode`
   - `POST /resources/nodes/{name}/drain` → `h.HandleDrainNode`

- [ ] **Step 2: Slim down pods.go**

Delete HandleListPods, HandleGetPod, HandleDeletePod (now served by pod adapter). Keep HandlePodExec, HandlePodLogs.

- [ ] **Step 3: Slim down nodes.go**

Delete HandleListNodes, HandleGetNode (now served by node adapter). Keep HandleCordonNode, HandleUncordonNode, HandleDrainNode.

- [ ] **Step 4: Delete secrets.go except HandleRevealSecret**

Move HandleRevealSecret to a smaller file or keep secrets.go with only that handler.

- [ ] **Step 5: Full build + vet + test + E2E**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(resources): clean up routes.go, slim down pods.go + nodes.go + secrets.go"
```

---

### Task 10: Final Verification

- [ ] **Step 1: Run full test suite**

```bash
cd backend && go test ./... -count=1
```

- [ ] **Step 2: Run E2E tests**

```bash
make test-e2e
```

- [ ] **Step 3: Verify all registered adapters**

Add a test that checks `RegisteredKinds()` matches the expected complete list of all 27+ resources.

- [ ] **Step 4: Count LOC reduction**

```bash
git diff --stat main
```

Expected: ~1200+ lines deleted net.

- [ ] **Step 5: Final commit + push**

```bash
git add -A
git commit -m "refactor(resources): verify all adapters, complete handler adapter refactor"
```
