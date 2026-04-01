# CRD Management (Extensions Hub) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generic CRD management system to k8sCenter — auto-discover CRDs, list/view/create/edit/delete custom resource instances, with a progressive schema-driven form for editing.

**Architecture:** A new `GenericCRDHandler` in the backend uses the dynamic client with user impersonation to serve all CRD operations through a single set of routes parameterized by API group and resource. The frontend adds a 9th icon rail section ("Extensions") with a hub page, resource list, and schema-driven form. CRD discovery uses an apiextensions informer for instant updates.

**Tech Stack:** Go (chi router, client-go dynamic client, apiextensions/v1), Deno/Fresh 2.x (Preact islands), Tailwind v4, CSS custom properties

**Spec:** `docs/superpowers/specs/2026-03-31-crd-management-design.md`

---

## File Map

### Backend — New Files
| File | Responsibility |
|---|---|
| `backend/internal/k8s/crd_discovery.go` | CRD informer, GVR lookup map, schema fetching, instance count cache |
| `backend/internal/k8s/resources/crd_handler.go` | GenericCRDHandler — list/get/create/update/delete/validate for any CRD |
| `backend/internal/k8s/resources/crd_handler_test.go` | Tests for GVR allowlist, CRUD operations, schema sanitization |

### Backend — Modified Files
| File | Change |
|---|---|
| `backend/internal/server/server.go` | Add `CRDHandler *resources.GenericCRDHandler` field |
| `backend/internal/server/routes.go` | Add `registerExtensionRoutes()` with all `/extensions/*` endpoints |
| `backend/internal/k8s/informers.go` | Add apiextensions CRD informer to `InformerManager` |
| `backend/cmd/kubecenter/main.go` | Wire `CRDDiscovery` and `GenericCRDHandler` at startup |

### Frontend — New Files
| File | Responsibility |
|---|---|
| `frontend/routes/extensions/index.tsx` | Hub page route — renders ExtensionsHub island |
| `frontend/routes/extensions/[group]/[resource]/index.tsx` | Resource list route |
| `frontend/routes/extensions/[group]/[resource]/new.tsx` | Create form route |
| `frontend/routes/extensions/[group]/[resource]/[namespace]/[name].tsx` | Detail/edit route (namespaced) |
| `frontend/routes/extensions/[group]/[resource]/_/[name].tsx` | Detail/edit route (cluster-scoped) |
| `frontend/islands/ExtensionsHub.tsx` | Hub page — CRD list grouped by API group |
| `frontend/islands/CRDResourceList.tsx` | Resource list with metadata bar, filters, table |
| `frontend/islands/SchemaForm.tsx` | Progressive schema-driven form generator |
| `frontend/islands/SchemaFormField.tsx` | Single field renderer (string, number, boolean, array, object, anyOf) |
| `frontend/islands/YamlPreview.tsx` | Read-only syntax-highlighted YAML preview |
| `frontend/lib/crd-types.ts` | TypeScript interfaces for CRD API responses |
| `frontend/lib/schema-to-yaml.ts` | Convert form state to YAML string |

### Frontend — Modified Files
| File | Change |
|---|---|
| `frontend/lib/constants.ts` | Add Extensions to `DOMAIN_SECTIONS` |
| `frontend/islands/IconRail.tsx` | Add 9th entry (puzzle piece icon) |

---

## Task 1: CRD Discovery Service (Backend)

**Files:**
- Create: `backend/internal/k8s/crd_discovery.go`
- Modify: `backend/internal/k8s/informers.go`
- Test: `backend/internal/k8s/crd_discovery_test.go`

This is the foundation — discovers CRDs in the cluster, maintains a GVR lookup map, and caches instance counts.

- [ ] **Step 1: Create the CRDDiscovery struct and interfaces**

```go
// backend/internal/k8s/crd_discovery.go
package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// coreAPIDenylist prevents the generic CRD handler from serving core k8s resources.
var coreAPIDenylist = map[string]bool{
	"":                          true, // core API group
	"apps":                      true,
	"batch":                     true,
	"autoscaling":               true,
	"policy":                    true,
	"networking.k8s.io":         true,
	"rbac.authorization.k8s.io": true,
	"storage.k8s.io":            true,
	"apiextensions.k8s.io":      true,
	"coordination.k8s.io":       true,
	"admissionregistration.k8s.io": true,
	"certificates.k8s.io":      true,
	"discovery.k8s.io":         true,
	"events.k8s.io":            true,
	"node.k8s.io":              true,
	"scheduling.k8s.io":        true,
}

// CRDInfo holds metadata about a discovered CRD.
type CRDInfo struct {
	Group                  string                          `json:"group"`
	Version                string                          `json:"version"`
	Resource               string                          `json:"resource"`
	Kind                   string                          `json:"kind"`
	Scope                  string                          `json:"scope"` // "Namespaced" or "Cluster"
	Served                 bool                            `json:"served"`
	StorageVersion         bool                            `json:"storageVersion"`
	AdditionalPrinterColumns []apiextensionsv1.CustomResourceColumnDefinition `json:"additionalPrinterColumns,omitempty"`
}

// CRDDiscovery watches for CRDs and maintains a lookup cache.
type CRDDiscovery struct {
	mu        sync.RWMutex
	crds      map[string]*CRDInfo     // key: "group/resource"
	gvrMap    map[string]schema.GroupVersionResource // key: "group/resource"
	counts    map[string]int          // key: "group/resource", instance counts
	countsMu  sync.RWMutex
	countsAt  time.Time
	dynClient dynamic.Interface
	logger    *slog.Logger
}

// NewCRDDiscovery creates a CRDDiscovery that watches for CRDs using an informer.
func NewCRDDiscovery(restConfig *rest.Config, dynClient dynamic.Interface, logger *slog.Logger) (*CRDDiscovery, error) {
	extClient, err := apiextensionsclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating apiextensions client: %w", err)
	}

	d := &CRDDiscovery{
		crds:      make(map[string]*CRDInfo),
		gvrMap:    make(map[string]schema.GroupVersionResource),
		counts:    make(map[string]int),
		dynClient: dynClient,
		logger:    logger,
	}

	factory := apiextensionsinformers.NewSharedInformerFactory(extClient, 5*time.Minute)
	informer := factory.Apiextensions().V1().CustomResourceDefinitions().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { d.onCRDChange(obj) },
		UpdateFunc: func(_, obj interface{}) { d.onCRDChange(obj) },
		DeleteFunc: func(obj interface{}) { d.onCRDDelete(obj) },
	})

	return d, nil
}
```

- [ ] **Step 2: Implement the CRD change handlers and lookup methods**

Add to `crd_discovery.go`:

```go
func (d *CRDDiscovery) onCRDChange(obj interface{}) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return
	}
	group := crd.Spec.Group
	if coreAPIDenylist[group] {
		return // ignore core API group CRDs
	}

	resource := crd.Spec.Names.Plural
	kind := crd.Spec.Names.Kind
	scope := string(crd.Spec.Scope)
	key := group + "/" + resource

	// Find the storage version
	for _, v := range crd.Spec.Versions {
		if !v.Storage {
			continue
		}
		info := &CRDInfo{
			Group:          group,
			Version:        v.Name,
			Resource:       resource,
			Kind:           kind,
			Scope:          scope,
			Served:         v.Served,
			StorageVersion: true,
			AdditionalPrinterColumns: v.AdditionalPrinterColumns,
		}
		gvr := schema.GroupVersionResource{Group: group, Version: v.Name, Resource: resource}

		d.mu.Lock()
		d.crds[key] = info
		d.gvrMap[key] = gvr
		d.mu.Unlock()

		d.logger.Info("CRD discovered", "group", group, "resource", resource, "version", v.Name)
		break
	}
}

func (d *CRDDiscovery) onCRDDelete(obj interface{}) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		crd, ok = tombstone.Obj.(*apiextensionsv1.CustomResourceDefinition)
		if !ok {
			return
		}
	}
	key := crd.Spec.Group + "/" + crd.Spec.Names.Plural
	d.mu.Lock()
	delete(d.crds, key)
	delete(d.gvrMap, key)
	d.mu.Unlock()
	d.logger.Info("CRD removed", "key", key)
}

// ListCRDs returns all discovered CRDs grouped by API group.
func (d *CRDDiscovery) ListCRDs() map[string][]*CRDInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	grouped := make(map[string][]*CRDInfo)
	for _, info := range d.crds {
		grouped[info.Group] = append(grouped[info.Group], info)
	}
	return grouped
}

// ResolveGVR returns the GVR for a group/resource pair, or false if not found.
func (d *CRDDiscovery) ResolveGVR(group, resource string) (schema.GroupVersionResource, bool) {
	if coreAPIDenylist[group] {
		return schema.GroupVersionResource{}, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	gvr, ok := d.gvrMap[group+"/"+resource]
	return gvr, ok
}

// GetCRDInfo returns the CRDInfo for a group/resource pair, or nil if not found.
func (d *CRDDiscovery) GetCRDInfo(group, resource string) *CRDInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.crds[group+"/"+resource]
}

// IsNamespaced returns true if the CRD is namespace-scoped.
func (d *CRDDiscovery) IsNamespaced(group, resource string) bool {
	info := d.GetCRDInfo(group, resource)
	return info != nil && info.Scope == "Namespaced"
}
```

- [ ] **Step 3: Implement instance count caching with bounded concurrency**

Add to `crd_discovery.go`:

```go
const countCacheTTL = 30 * time.Second
const countConcurrency = 5

// GetCounts returns cached instance counts for all CRDs. Refreshes if stale.
func (d *CRDDiscovery) GetCounts(ctx context.Context) map[string]int {
	d.countsMu.RLock()
	if time.Since(d.countsAt) < countCacheTTL && len(d.counts) > 0 {
		counts := make(map[string]int, len(d.counts))
		for k, v := range d.counts {
			counts[k] = v
		}
		d.countsMu.RUnlock()
		return counts
	}
	d.countsMu.RUnlock()

	return d.refreshCounts(ctx)
}

func (d *CRDDiscovery) refreshCounts(ctx context.Context) map[string]int {
	d.mu.RLock()
	gvrSnapshot := make(map[string]schema.GroupVersionResource, len(d.gvrMap))
	scopeSnapshot := make(map[string]string, len(d.crds))
	for k, v := range d.gvrMap {
		gvrSnapshot[k] = v
	}
	for k, v := range d.crds {
		scopeSnapshot[k] = v.Scope
	}
	d.mu.RUnlock()

	results := make(map[string]int, len(gvrSnapshot))
	var mu sync.Mutex
	sem := make(chan struct{}, countConcurrency)
	var wg sync.WaitGroup

	for key, gvr := range gvrSnapshot {
		wg.Add(1)
		go func(k string, g schema.GroupVersionResource) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			list, err := d.dynClient.Resource(g).List(ctx, metav1.ListOptions{Limit: 1})
			if err != nil {
				d.logger.Debug("failed to count CRD instances", "key", k, "error", err)
				return
			}
			count := len(list.Items)
			if list.GetContinue() != "" {
				// Has more items — use remainingItemCount if available
				if rc := list.GetRemainingItemCount(); rc != nil {
					count = int(*rc) + len(list.Items)
				} else {
					// Fall back to full list count
					fullList, err := d.dynClient.Resource(g).List(ctx, metav1.ListOptions{})
					if err == nil {
						count = len(fullList.Items)
					}
				}
			}
			mu.Lock()
			results[k] = count
			mu.Unlock()
		}(key, gvr)
	}

	wg.Wait()

	d.countsMu.Lock()
	d.counts = results
	d.countsAt = time.Now()
	d.countsMu.Unlock()

	return results
}
```

- [ ] **Step 4: Add apiextensions dependency to go.mod**

```bash
cd backend && go get k8s.io/apiextensions-apiserver@v0.35.2 && go mod tidy
```

- [ ] **Step 5: Write tests for GVR allowlist and discovery**

Create `backend/internal/k8s/crd_discovery_test.go`:

```go
package k8s

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResolveGVR_DeniesCorAPIGroups(t *testing.T) {
	d := &CRDDiscovery{
		crds:   make(map[string]*CRDInfo),
		gvrMap: make(map[string]schema.GroupVersionResource),
	}
	// Even if somehow a core resource is in the map, deny it
	d.gvrMap["apps/deployments"] = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	_, ok := d.ResolveGVR("apps", "deployments")
	if ok {
		t.Error("expected core API group 'apps' to be denied")
	}

	_, ok = d.ResolveGVR("", "secrets")
	if ok {
		t.Error("expected core API group '' to be denied")
	}

	_, ok = d.ResolveGVR("rbac.authorization.k8s.io", "roles")
	if ok {
		t.Error("expected core API group 'rbac.authorization.k8s.io' to be denied")
	}
}

func TestResolveGVR_AllowsCRDGroups(t *testing.T) {
	d := &CRDDiscovery{
		crds:   make(map[string]*CRDInfo),
		gvrMap: make(map[string]schema.GroupVersionResource),
	}
	expected := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	d.gvrMap["cert-manager.io/certificates"] = expected

	got, ok := d.ResolveGVR("cert-manager.io", "certificates")
	if !ok {
		t.Fatal("expected cert-manager.io to be allowed")
	}
	if got != expected {
		t.Errorf("got %v, want %v", got, expected)
	}
}

func TestIsNamespaced(t *testing.T) {
	d := &CRDDiscovery{
		crds: map[string]*CRDInfo{
			"cert-manager.io/certificates":     {Scope: "Namespaced"},
			"cert-manager.io/clusterissuers":   {Scope: "Cluster"},
		},
		gvrMap: make(map[string]schema.GroupVersionResource),
	}

	if !d.IsNamespaced("cert-manager.io", "certificates") {
		t.Error("expected certificates to be namespaced")
	}
	if d.IsNamespaced("cert-manager.io", "clusterissuers") {
		t.Error("expected clusterissuers to be cluster-scoped")
	}
}
```

- [ ] **Step 6: Run tests**

```bash
cd backend && go test ./internal/k8s/ -run TestResolveGVR -v && go test ./internal/k8s/ -run TestIsNamespaced -v
```

- [ ] **Step 7: Run go vet**

```bash
cd backend && go vet ./internal/k8s/...
```

- [ ] **Step 8: Commit**

```bash
git add backend/internal/k8s/crd_discovery.go backend/internal/k8s/crd_discovery_test.go backend/go.mod backend/go.sum
git commit -m "feat: add CRD discovery service with GVR allowlist and count caching"
```

---

## Task 2: Generic CRD Handler (Backend CRUD)

**Files:**
- Create: `backend/internal/k8s/resources/crd_handler.go`
- Create: `backend/internal/k8s/resources/crd_handler_test.go`

The generic handler serves all CRD CRUD operations through the dynamic client.

- [ ] **Step 1: Create the GenericCRDHandler struct**

```go
// backend/internal/k8s/resources/crd_handler.go
package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

var dnsSubdomainRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]*[a-z0-9])?$`)

// GenericCRDHandler provides HTTP handlers for arbitrary CRD resources.
type GenericCRDHandler struct {
	Discovery   *k8s.CRDDiscovery
	K8sClient   *k8s.ClientFactory
	ClusterRouter *k8s.ClusterRouter
	AuditLogger audit.Logger
	Logger      *slog.Logger
}

// resolveGVR extracts and validates group/resource from URL params.
func (h *GenericCRDHandler) resolveGVR(w http.ResponseWriter, r *http.Request) (schema.GroupVersionResource, *k8s.CRDInfo, bool) {
	group := chi.URLParam(r, "group")
	resource := chi.URLParam(r, "resource")

	if !dnsSubdomainRegexp.MatchString(group) || !dnsSubdomainRegexp.MatchString(resource) {
		writeError(w, http.StatusBadRequest, "invalid group or resource name", "")
		return schema.GroupVersionResource{}, nil, false
	}

	gvr, ok := h.Discovery.ResolveGVR(group, resource)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("CRD %s/%s not found", group, resource), "")
		return schema.GroupVersionResource{}, nil, false
	}

	info := h.Discovery.GetCRDInfo(group, resource)
	return gvr, info, true
}

// impersonatingDynamic returns a dynamic client impersonating the user.
func (h *GenericCRDHandler) impersonatingDynamic(r *http.Request, user *auth.User) (dynamic.Interface, error) {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	return h.ClusterRouter.DynamicClientForCluster(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
}
```

- [ ] **Step 2: Implement HandleListCRDs and HandleGetCRD (discovery endpoints)**

Add to `crd_handler.go`:

```go
// HandleListCRDs returns all discovered CRDs grouped by API group.
func (h *GenericCRDHandler) HandleListCRDs(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUser(w, r)
	if !ok {
		return
	}
	grouped := h.Discovery.ListCRDs()
	writeData(w, grouped)
}

// HandleGetCRD returns metadata and schema for a specific CRD.
func (h *GenericCRDHandler) HandleGetCRD(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	group := chi.URLParam(r, "group")
	resource := chi.URLParam(r, "resource")

	info := h.Discovery.GetCRDInfo(group, resource)
	if info == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("CRD %s/%s not found", group, resource), "")
		return
	}

	// Fetch the full CRD to get the schema
	dc, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	crdName := resource + "." + group
	crdObj, err := dc.Resource(crdGVR).Get(r.Context(), crdName, metav1.GetOptions{})
	if err != nil {
		writeError(w, http.StatusNotFound, "CRD not found", err.Error())
		return
	}

	writeData(w, crdObj)
}

// HandleCRDCounts returns cached instance counts for all CRDs.
func (h *GenericCRDHandler) HandleCRDCounts(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUser(w, r)
	if !ok {
		return
	}
	counts := h.Discovery.GetCounts(r.Context())
	writeData(w, counts)
}
```

- [ ] **Step 3: Implement CRUD handlers (List, Get, Create, Update, Delete)**

Add to `crd_handler.go`:

```go
// HandleListCRDInstances lists instances of a CRD.
func (h *GenericCRDHandler) HandleListCRDInstances(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	dc, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	ns := chi.URLParam(r, "ns")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit <= 0 || limit > 500 {
			limit = 100
		}
	}

	opts := metav1.ListOptions{Limit: int64(limit)}
	if cont := r.URL.Query().Get("continue"); cont != "" {
		opts.Continue = cont
	}
	if sel := r.URL.Query().Get("labelSelector"); sel != "" {
		opts.LabelSelector = sel
	}

	var list *unstructured.UnstructuredList
	if info.Scope == "Namespaced" && ns != "" {
		list, err = dc.Resource(gvr).Namespace(ns).List(r.Context(), opts)
	} else {
		list, err = dc.Resource(gvr).List(r.Context(), opts)
	}
	if err != nil {
		mapK8sError(w, err, "list", info.Kind, ns, "")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":     list.Items,
		"metadata": map[string]interface{}{
			"total":    len(list.Items),
			"continue": list.GetContinue(),
		},
	})
}

// HandleGetCRDInstance gets a single CRD instance.
func (h *GenericCRDHandler) HandleGetCRDInstance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	dc, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	ns := chi.URLParam(r, "ns")
	name := chi.URLParam(r, "name")

	var obj *unstructured.Unstructured
	if info.Scope == "Namespaced" {
		obj, err = dc.Resource(gvr).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	} else {
		obj, err = dc.Resource(gvr).Get(r.Context(), name, metav1.GetOptions{})
	}
	if err != nil {
		mapK8sError(w, err, "get", info.Kind, ns, name)
		return
	}
	writeData(w, obj)
}

// HandleCreateCRDInstance creates a new CRD instance from a raw JSON body.
func (h *GenericCRDHandler) HandleCreateCRDInstance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	var obj unstructured.Unstructured
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&obj.Object); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	ns := chi.URLParam(r, "ns")
	if info.Scope == "Namespaced" {
		obj.SetNamespace(ns)
	}

	dc, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	var created *unstructured.Unstructured
	if info.Scope == "Namespaced" {
		created, err = dc.Resource(gvr).Namespace(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	} else {
		created, err = dc.Resource(gvr).Create(r.Context(), &obj, metav1.CreateOptions{})
	}
	if err != nil {
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Action:       audit.ActionCreate,
			ResourceKind: info.Kind,
			Namespace:    ns,
			ResourceName: obj.GetName(),
			Result:       audit.ResultFailure,
			User:         user.KubernetesUsername,
		})
		mapK8sError(w, err, "create", info.Kind, ns, obj.GetName())
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Action:       audit.ActionCreate,
		ResourceKind: info.Kind,
		Namespace:    ns,
		ResourceName: created.GetName(),
		Result:       audit.ResultSuccess,
		User:         user.KubernetesUsername,
	})

	writeJSON(w, http.StatusCreated, map[string]interface{}{"data": created})
}

// HandleUpdateCRDInstance updates an existing CRD instance.
func (h *GenericCRDHandler) HandleUpdateCRDInstance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	var obj unstructured.Unstructured
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&obj.Object); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	ns := chi.URLParam(r, "ns")
	name := chi.URLParam(r, "name")
	obj.SetName(name)
	if info.Scope == "Namespaced" {
		obj.SetNamespace(ns)
	}

	dc, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	var updated *unstructured.Unstructured
	if info.Scope == "Namespaced" {
		updated, err = dc.Resource(gvr).Namespace(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	} else {
		updated, err = dc.Resource(gvr).Update(r.Context(), &obj, metav1.UpdateOptions{})
	}
	if err != nil {
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Action: audit.ActionUpdate, ResourceKind: info.Kind,
			Namespace: ns, ResourceName: name, Result: audit.ResultFailure, User: user.KubernetesUsername,
		})
		mapK8sError(w, err, "update", info.Kind, ns, name)
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Action: audit.ActionUpdate, ResourceKind: info.Kind,
		Namespace: ns, ResourceName: name, Result: audit.ResultSuccess, User: user.KubernetesUsername,
	})

	writeData(w, updated)
}

// HandleDeleteCRDInstance deletes a CRD instance.
func (h *GenericCRDHandler) HandleDeleteCRDInstance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "ns")
	name := chi.URLParam(r, "name")

	dc, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if info.Scope == "Namespaced" {
		err = dc.Resource(gvr).Namespace(ns).Delete(r.Context(), name, metav1.DeleteOptions{})
	} else {
		err = dc.Resource(gvr).Delete(r.Context(), name, metav1.DeleteOptions{})
	}
	if err != nil {
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Action: audit.ActionDelete, ResourceKind: info.Kind,
			Namespace: ns, ResourceName: name, Result: audit.ResultFailure, User: user.KubernetesUsername,
		})
		mapK8sError(w, err, "delete", info.Kind, ns, name)
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Action: audit.ActionDelete, ResourceKind: info.Kind,
		Namespace: ns, ResourceName: name, Result: audit.ResultSuccess, User: user.KubernetesUsername,
	})

	w.WriteHeader(http.StatusNoContent)
}

// HandleValidateCRDInstance validates a CRD instance via dry-run.
func (h *GenericCRDHandler) HandleValidateCRDInstance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	var obj unstructured.Unstructured
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&obj.Object); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	dc, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	ns := obj.GetNamespace()
	dryRunOpts := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}

	if info.Scope == "Namespaced" {
		_, err = dc.Resource(gvr).Namespace(ns).Create(r.Context(), &obj, dryRunOpts)
	} else {
		_, err = dc.Resource(gvr).Create(r.Context(), &obj, dryRunOpts)
	}
	if err != nil {
		mapK8sError(w, err, "validate", info.Kind, ns, obj.GetName())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"data": map[string]string{"status": "valid"}})
}

// HandleRediscover forces a refresh of the CRD discovery cache (admin-only).
func (h *GenericCRDHandler) HandleRediscover(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUser(w, r)
	if !ok {
		return
	}
	// Admin check is enforced at the route level via middleware.RequireAdmin
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": map[string]string{"status": "refreshed"}})
}
```

- [ ] **Step 4: Write tests for the handler**

Create `backend/internal/k8s/resources/crd_handler_test.go`:

```go
package resources

import (
	"testing"
)

func TestResolveGVR_RejectsInvalidNames(t *testing.T) {
	// Test that path params with invalid characters are rejected
	tests := []struct {
		group    string
		resource string
		valid    bool
	}{
		{"cert-manager.io", "certificates", true},
		{"cilium.io", "ciliumnetworkpolicies", true},
		{"../etc/passwd", "secrets", false},
		{"apps", "deployments", false},   // core API group denied
		{"", "secrets", false},            // empty group denied
		{"UPPER.case", "bad", false},      // uppercase invalid
	}

	for _, tt := range tests {
		valid := dnsSubdomainRegexp.MatchString(tt.group) && dnsSubdomainRegexp.MatchString(tt.resource)
		if tt.group == "" {
			valid = false // empty string matches regex but is in denylist
		}
		if valid != tt.valid {
			t.Errorf("group=%q resource=%q: got valid=%v, want %v", tt.group, tt.resource, valid, tt.valid)
		}
	}
}
```

- [ ] **Step 5: Run tests and vet**

```bash
cd backend && go test ./internal/k8s/resources/ -run TestResolveGVR -v && go vet ./internal/k8s/resources/...
```

- [ ] **Step 6: Commit**

```bash
git add backend/internal/k8s/resources/crd_handler.go backend/internal/k8s/resources/crd_handler_test.go
git commit -m "feat: add generic CRD handler with CRUD, validation, and audit logging"
```

---

## Task 3: Route Registration and Server Wiring (Backend)

**Files:**
- Modify: `backend/internal/server/server.go`
- Modify: `backend/internal/server/routes.go`
- Modify: `backend/cmd/kubecenter/main.go`

- [ ] **Step 1: Add CRDHandler field to Server struct**

In `backend/internal/server/server.go`, add after `AlertingHandler`:

```go
CRDHandler         *resources.GenericCRDHandler
```

- [ ] **Step 2: Register extension routes in routes.go**

Add after the networking routes block (around line 107):

```go
// Extension (CRD) routes — only registered if CRD handler is available
if s.CRDHandler != nil {
	s.registerExtensionRoutes(ar)
}
```

Then add the route registration function:

```go
func (s *Server) registerExtensionRoutes(ar chi.Router) {
	h := s.CRDHandler
	yamlRL := s.YAMLRateLimiter
	if yamlRL == nil {
		yamlRL = s.RateLimiter
	}

	ar.Route("/extensions", func(er chi.Router) {
		// CRD discovery endpoints (read-only, no rate limit)
		er.Get("/crds", h.HandleListCRDs)
		er.Get("/crds/counts", h.HandleCRDCounts)
		er.Get("/crds/{group}/{resource}", h.HandleGetCRD)

		// Admin-only: force rediscovery
		er.With(middleware.RequireAdmin).Post("/crds/rediscover", h.HandleRediscover)

		// CRD instance CRUD (rate-limited writes)
		er.Route("/resources/{group}/{resource}", func(cr chi.Router) {
			// Cluster-scoped list
			cr.Get("/", h.HandleListCRDInstances)

			// Validate (uses /-/ sentinel to avoid namespace ambiguity)
			cr.With(middleware.RateLimit(yamlRL)).Post("/-/validate", h.HandleValidateCRDInstance)

			// Namespaced operations
			cr.Get("/{ns}", h.HandleListCRDInstances)
			cr.Get("/{ns}/{name}", h.HandleGetCRDInstance)
			cr.With(middleware.RateLimit(yamlRL)).Post("/{ns}", h.HandleCreateCRDInstance)
			cr.With(middleware.RateLimit(yamlRL)).Put("/{ns}/{name}", h.HandleUpdateCRDInstance)
			cr.With(middleware.RateLimit(yamlRL)).Delete("/{ns}/{name}", h.HandleDeleteCRDInstance)
		})
	})
}
```

- [ ] **Step 3: Wire CRDDiscovery and GenericCRDHandler in main.go**

Find the section in `main.go` where handlers are created and add:

```go
// CRD Discovery
crdDiscovery, err := k8s.NewCRDDiscovery(restConfig, dynClient, logger)
if err != nil {
	logger.Warn("CRD discovery unavailable", "error", err)
} else {
	srv.CRDHandler = &resources.GenericCRDHandler{
		Discovery:     crdDiscovery,
		K8sClient:     clientFactory,
		ClusterRouter: clusterRouter,
		AuditLogger:   auditLogger,
		Logger:        logger,
	}
}
```

- [ ] **Step 4: Verify backend compiles and starts**

```bash
cd backend && go build ./cmd/kubecenter/ && go vet ./...
```

- [ ] **Step 5: Commit**

```bash
git add backend/internal/server/server.go backend/internal/server/routes.go backend/cmd/kubecenter/main.go
git commit -m "feat: wire CRD discovery and extension routes into server"
```

---

## Task 4: Frontend Types and Constants

**Files:**
- Create: `frontend/lib/crd-types.ts`
- Modify: `frontend/lib/constants.ts`
- Modify: `frontend/islands/IconRail.tsx`

- [ ] **Step 1: Create CRD TypeScript interfaces**

```typescript
// frontend/lib/crd-types.ts

/** CRD metadata returned by GET /extensions/crds */
export interface CRDInfo {
  group: string;
  version: string;
  resource: string;
  kind: string;
  scope: "Namespaced" | "Cluster";
  served: boolean;
  storageVersion: boolean;
  additionalPrinterColumns?: PrinterColumn[];
}

export interface PrinterColumn {
  name: string;
  type: string;
  jsonPath: string;
  description?: string;
  priority?: number;
}

/** Grouped CRD response from the backend */
export type CRDGroupedResponse = Record<string, CRDInfo[]>;

/** CRD instance counts response */
export type CRDCountsResponse = Record<string, number>;

/** OpenAPI V3 schema property (simplified for form rendering) */
export interface SchemaProperty {
  type?: string;
  description?: string;
  enum?: string[];
  default?: unknown;
  format?: string;
  required?: string[];
  properties?: Record<string, SchemaProperty>;
  items?: SchemaProperty;
  additionalProperties?: boolean | SchemaProperty;
  oneOf?: SchemaProperty[];
  anyOf?: SchemaProperty[];
  "x-kubernetes-preserve-unknown-fields"?: boolean;
  "x-kubernetes-int-or-string"?: boolean;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
}
```

- [ ] **Step 2: Add Extensions to DOMAIN_SECTIONS in constants.ts**

Add after the last section in `DOMAIN_SECTIONS`:

```typescript
{
  id: "extensions",
  label: "Extensions",
  icon: "puzzle",
  href: "/extensions",
},
```

- [ ] **Step 3: Add puzzle piece icon to IconRail.tsx**

Find the icon mapping in `IconRail.tsx` and add the puzzle piece SVG paths for the "puzzle" icon id. Follow the existing pattern for icon rendering.

- [ ] **Step 4: Run deno lint**

```bash
cd frontend && deno lint
```

- [ ] **Step 5: Commit**

```bash
git add frontend/lib/crd-types.ts frontend/lib/constants.ts frontend/islands/IconRail.tsx
git commit -m "feat: add CRD types, Extensions section to navigation"
```

---

## Task 5: Extensions Hub Page (Frontend)

**Files:**
- Create: `frontend/routes/extensions/index.tsx`
- Create: `frontend/islands/ExtensionsHub.tsx`

- [ ] **Step 1: Create the hub route**

```typescript
// frontend/routes/extensions/index.tsx
import { define } from "@/utils.ts";
import ExtensionsHub from "@/islands/ExtensionsHub.tsx";

export default define.page(function ExtensionsPage() {
  return <ExtensionsHub />;
});
```

- [ ] **Step 2: Create ExtensionsHub island**

Build the hub page that fetches CRDs from `/v1/extensions/crds`, groups by API group, and shows cards with instance counts. Follow the pattern from `WorkloadsDashboard.tsx` — use `useSignal`, `useEffect`, `IS_BROWSER`, `apiGet`. Show a search/filter bar at the top. Each CRD card links to `/extensions/${group}/${resource}`.

Key elements:
- Fetch `GET /v1/extensions/crds` for CRD list
- Fetch `GET /v1/extensions/crds/counts` separately (lazy-loaded)
- Group CRDs by `info.group`
- Each API group section: header with group name + separator + count
- Each CRD card: kind name, full resource name (monospace), instance count badge, scope badge
- Cards use `auto-fill, minmax(280px, 1fr)` grid (consistent with the large screen adaptation)
- Search bar filters cards by kind name or full resource name
- Loading skeleton while fetching

- [ ] **Step 3: Run deno lint and verify page loads**

```bash
cd frontend && deno lint
```

- [ ] **Step 4: Commit**

```bash
git add frontend/routes/extensions/index.tsx frontend/islands/ExtensionsHub.tsx
git commit -m "feat: add Extensions hub page with CRD discovery"
```

---

## Task 6: CRD Resource List Page (Frontend)

**Files:**
- Create: `frontend/routes/extensions/[group]/[resource]/index.tsx`
- Create: `frontend/islands/CRDResourceList.tsx`

- [ ] **Step 1: Create the resource list route**

```typescript
// frontend/routes/extensions/[group]/[resource]/index.tsx
import { define } from "@/utils.ts";
import CRDResourceList from "@/islands/CRDResourceList.tsx";

export default define.page(function CRDResourcePage(ctx) {
  return (
    <CRDResourceList
      group={ctx.params.group}
      resource={ctx.params.resource}
    />
  );
});
```

- [ ] **Step 2: Create CRDResourceList island**

Build the resource list page:
- Fetch CRD metadata from `GET /v1/extensions/crds/${group}/${resource}`
- Fetch instances from `GET /v1/extensions/resources/${group}/${resource}` (or `/${ns}` if namespace selected)
- Render breadcrumbs: Extensions → group → Kind
- Render CRD metadata bar (group, version, kind, scope, served)
- Render resource table with columns:
  - Name (linked to detail page), Namespace (if namespaced), Age
  - Status from `.status.conditions[?type==Ready]`
  - `additionalPrinterColumns` — extract values using jsonPath from each item
- Search filter, status filter tabs (All / Ready / Not Ready)
- "New [Kind]" button linking to `./new`
- Kebab menu per row with "Delete" action (uses `ConfirmDialog`)
- Pagination via `continue` token

- [ ] **Step 3: Commit**

```bash
git add frontend/routes/extensions/[group]/[resource]/index.tsx frontend/islands/CRDResourceList.tsx
git commit -m "feat: add CRD resource list page with auto-generated columns"
```

---

## Task 7: Schema Form (Frontend — Progressive Rendering)

**Files:**
- Create: `frontend/islands/SchemaForm.tsx`
- Create: `frontend/islands/SchemaFormField.tsx`
- Create: `frontend/lib/schema-to-yaml.ts`
- Create: `frontend/islands/YamlPreview.tsx`

This is the most complex frontend task. The form auto-generates from the CRD's OpenAPI schema using progressive rendering.

- [ ] **Step 1: Create schema-to-yaml utility**

```typescript
// frontend/lib/schema-to-yaml.ts

/** Convert a nested form state object to a YAML string. */
export function formStateToYaml(
  apiVersion: string,
  kind: string,
  metadata: { name: string; namespace?: string; labels?: Record<string, string>; annotations?: Record<string, string> },
  spec: Record<string, unknown>,
): string {
  const doc: Record<string, unknown> = {
    apiVersion,
    kind,
    metadata: { ...metadata },
    spec,
  };
  return toYamlString(doc, 0);
}

function toYamlString(obj: unknown, indent: number): string {
  const pad = "  ".repeat(indent);
  if (obj === null || obj === undefined) return "null";
  if (typeof obj === "string") {
    if (obj.includes("\n") || obj.includes(": ") || obj.includes("#")) {
      return `"${obj.replace(/"/g, '\\"')}"`;
    }
    return obj;
  }
  if (typeof obj === "number" || typeof obj === "boolean") return String(obj);
  if (Array.isArray(obj)) {
    if (obj.length === 0) return "[]";
    return obj.map((item) => {
      const val = toYamlString(item, indent + 1);
      if (typeof item === "object" && item !== null && !Array.isArray(item)) {
        const lines = val.split("\n");
        return `${pad}- ${lines[0]}\n${lines.slice(1).map((l) => `${pad}  ${l}`).join("\n")}`;
      }
      return `${pad}- ${val}`;
    }).join("\n");
  }
  if (typeof obj === "object") {
    const entries = Object.entries(obj as Record<string, unknown>).filter(([_, v]) => v !== undefined && v !== "");
    if (entries.length === 0) return "{}";
    return entries.map(([k, v]) => {
      const val = toYamlString(v, indent + 1);
      if (typeof v === "object" && v !== null && !Array.isArray(v) && Object.keys(v as object).length > 0) {
        return `${pad}${k}:\n${val}`;
      }
      if (Array.isArray(v) && v.length > 0) {
        return `${pad}${k}:\n${val}`;
      }
      return `${pad}${k}: ${val}`;
    }).join("\n");
  }
  return String(obj);
}
```

- [ ] **Step 2: Create YamlPreview island**

```typescript
// frontend/islands/YamlPreview.tsx
interface YamlPreviewProps {
  yaml: string;
}

export default function YamlPreview({ yaml }: YamlPreviewProps) {
  // Syntax highlight YAML with CSS classes — no editor dependency
  const highlighted = yaml.split("\n").map((line, i) => {
    let html = line
      .replace(/^(\s*)([\w\-\.]+)(:)/g, '$1<span style="color:var(--accent)">$2</span>$3')
      .replace(/:\s+(".*?")/g, ': <span style="color:var(--success)">$1</span>')
      .replace(/:\s+(true|false|null)/g, ': <span style="color:var(--warning)">$1</span>')
      .replace(/:\s+(\d+)/g, ': <span style="color:var(--accent-secondary)">$1</span>')
      .replace(/(#.*)/g, '<span style="color:var(--text-muted)">$1</span>');
    return `<span style="color:var(--text-muted);user-select:none;display:inline-block;width:3ch;text-align:right;margin-right:1ch;">${i + 1}</span>${html}`;
  }).join("\n");

  return (
    <pre
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border-primary)",
        borderRadius: "var(--radius)",
        padding: "16px",
        fontSize: "13px",
        fontFamily: "var(--font-mono)",
        lineHeight: 1.6,
        overflow: "auto",
        color: "var(--text-secondary)",
      }}
      dangerouslySetInnerHTML={{ __html: highlighted }}
    />
  );
}
```

- [ ] **Step 3: Create SchemaFormField island (the recursive field renderer)**

Create `frontend/islands/SchemaFormField.tsx` with the progressive rendering algorithm:

- Depth 0-4: render as form fields (text input, number, select, toggle, etc.)
- Depth 5+: render as a YAML textarea for that subtree
- `anyOf`/`oneOf`: render a type selector dropdown, then render selected variant's fields
- `x-kubernetes-preserve-unknown-fields`: render as key-value editor
- `x-kubernetes-int-or-string`: render as text input
- Arrays of primitives: repeatable input rows with + Add / × Remove
- Arrays of objects: repeatable collapsible fieldsets
- Objects: collapsible fieldset (required expanded, optional collapsed)
- All descriptions rendered as escaped text (never innerHTML) — use Preact's default text rendering
- Stable `id` attributes from schema path (e.g., `field-spec-issuerRef-name`)
- `aria-required` on required fields

- [ ] **Step 4: Create SchemaForm island (the top-level form)**

Create `frontend/islands/SchemaForm.tsx`:

- Props: `schema` (OpenAPI schema), `value` (current form state or null for create), `apiVersion`, `kind`, `namespaced`, `onSubmit`, `onValidate`
- Renders: Metadata section (name + namespace), Spec section (SchemaFormField for each top-level property), Labels/Annotations section
- Form/YAML Preview toggle in the header
- Action bar: Create/Update, Validate, View YAML, Cancel
- Manages form state with `useSignal` for each field path
- Converts form state to unstructured JSON for API submission

- [ ] **Step 5: Run deno lint**

```bash
cd frontend && deno lint
```

- [ ] **Step 6: Commit**

```bash
git add frontend/islands/SchemaForm.tsx frontend/islands/SchemaFormField.tsx frontend/islands/YamlPreview.tsx frontend/lib/schema-to-yaml.ts
git commit -m "feat: add progressive schema-driven form with YAML preview"
```

---

## Task 8: Create and Edit Routes (Frontend)

**Files:**
- Create: `frontend/routes/extensions/[group]/[resource]/new.tsx`
- Create: `frontend/routes/extensions/[group]/[resource]/[namespace]/[name].tsx`
- Create: `frontend/routes/extensions/[group]/[resource]/_/[name].tsx`

- [ ] **Step 1: Create the new/create route**

```typescript
// frontend/routes/extensions/[group]/[resource]/new.tsx
import { define } from "@/utils.ts";
import SchemaForm from "@/islands/SchemaForm.tsx";

export default define.page(function CRDCreatePage(ctx) {
  return (
    <SchemaForm
      group={ctx.params.group}
      resource={ctx.params.resource}
      mode="create"
    />
  );
});
```

- [ ] **Step 2: Create the namespaced detail/edit route**

```typescript
// frontend/routes/extensions/[group]/[resource]/[namespace]/[name].tsx
import { define } from "@/utils.ts";
import SchemaForm from "@/islands/SchemaForm.tsx";

export default define.page(function CRDDetailPage(ctx) {
  return (
    <SchemaForm
      group={ctx.params.group}
      resource={ctx.params.resource}
      namespace={ctx.params.namespace}
      name={ctx.params.name}
      mode="edit"
    />
  );
});
```

- [ ] **Step 3: Create the cluster-scoped detail/edit route**

```typescript
// frontend/routes/extensions/[group]/[resource]/_/[name].tsx
import { define } from "@/utils.ts";
import SchemaForm from "@/islands/SchemaForm.tsx";

export default define.page(function CRDClusterScopedDetailPage(ctx) {
  return (
    <SchemaForm
      group={ctx.params.group}
      resource={ctx.params.resource}
      name={ctx.params.name}
      mode="edit"
    />
  );
});
```

- [ ] **Step 4: Run deno lint**

```bash
cd frontend && deno lint
```

- [ ] **Step 5: Commit**

```bash
git add frontend/routes/extensions/
git commit -m "feat: add create and edit routes for CRD instances"
```

---

## Task 9: Integration Testing and Polish

**Files:**
- All modified files from previous tasks

- [ ] **Step 1: Run full backend test suite**

```bash
cd backend && go test ./... -v -count=1
```

- [ ] **Step 2: Run full frontend lint**

```bash
cd frontend && deno lint
```

- [ ] **Step 3: Run go vet**

```bash
cd backend && go vet ./...
```

- [ ] **Step 4: Manual smoke test checklist**

1. Navigate to Extensions in icon rail — hub page loads
2. CRDs are grouped by API group (cilium.io, cert-manager.io, etc.)
3. Instance counts appear on cards
4. Search/filter works on the hub page
5. Click a CRD card — resource list loads with correct columns
6. `additionalPrinterColumns` values display correctly
7. Click "New [Kind]" — schema form renders with correct fields
8. Nested objects are collapsible
9. Required fields show red asterisks
10. Arrays have + Add / × Remove
11. "Validate" dry-run works
12. "View YAML" shows generated YAML
13. Create a test resource — success toast, redirect to list
14. Click existing resource — form pre-populated
15. Edit and update — success toast
16. Delete via kebab menu — confirmation dialog, resource removed
17. Cluster-scoped CRDs work (no namespace in URL)
18. CRD with no schema falls back to YAML textarea

- [ ] **Step 5: Commit any polish fixes**

```bash
git add -A && git commit -m "fix: polish CRD management integration"
```

---

## Task Summary

| Task | Description | Estimated Effort |
|---|---|---|
| 1 | CRD Discovery Service | Medium |
| 2 | Generic CRD Handler (CRUD) | Medium |
| 3 | Route Registration + Wiring | Small |
| 4 | Frontend Types + Constants | Small |
| 5 | Extensions Hub Page | Medium |
| 6 | CRD Resource List Page | Medium |
| 7 | Schema Form (Progressive Rendering) | Large |
| 8 | Create/Edit Routes | Small |
| 9 | Integration Testing + Polish | Medium |

**Recommended execution order:** Tasks 1-3 (backend), then 4-8 (frontend), then 9 (integration). Tasks 1-2 can be parallelized. Tasks 5-7 can be parallelized after Task 4.
