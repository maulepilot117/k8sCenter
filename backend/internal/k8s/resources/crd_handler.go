package resources

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// crdAccessConcurrency bounds parallel SAR calls when filtering CRD inventory
// for the P3-2 RBAC filter. Matches the existing countConcurrency=5 in
// crd_discovery.go — apiserver SAR cost amortizes well at this fan-out without
// overwhelming the API priority/fairness queue under burst load.
// Review-fix REL-001 / PERF-1.
const crdAccessConcurrency = 5

// dnsSubdomainRegexp validates DNS subdomain names used for API groups and resource names.
var dnsSubdomainRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]*[a-z0-9])?$`)

// GenericCRDHandler provides HTTP handlers for CRD CRUD operations via the dynamic client.
//
// AccessChecker is optional (nil disables the P3-2 RBAC filter — preserves the
// pre-audit behavior for tests / dev scenarios that don't wire one). Production
// main.go always supplies it.
type GenericCRDHandler struct {
	Discovery     *k8s.CRDDiscovery
	ClusterRouter *k8s.ClusterRouter
	AccessChecker *AccessChecker
	AuditLogger   audit.Logger
	Logger        *slog.Logger
}

// HandleListCRDs returns CRDs grouped by API group, filtered by per-user RBAC.
//
// P3-2 (security audit 2026-05-22): non-admins previously received the full
// cluster-wide CRD inventory, which leaked operator-deployed feature surfaces
// (external-secrets, cert-manager, mesh, scanners). Each CRD is now SSAR-checked
// against the request's cluster context for verb=list at the cluster level. If
// the user lacks cluster-wide list permission for the CRD's GVR, it's omitted
// from the response. Users with namespace-only Role bindings won't see those
// CRDs in inventory — they can still list instances directly via the namespaced
// resource endpoints if they know the GVR.
func (h *GenericCRDHandler) HandleListCRDs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	all := h.Discovery.ListCRDs()
	if h.AccessChecker == nil || auth.IsAdmin(user) {
		writeData(w, all)
		return
	}

	allowedSet := h.batchCRDListAccess(r.Context(), user, collectCRDKeys(all))
	filtered := make(map[string][]*k8s.CRDInfo, len(all))
	for group, infos := range all {
		for _, info := range infos {
			if allowedSet[info.Group+"/"+info.Resource] {
				filtered[group] = append(filtered[group], info)
			}
		}
	}
	writeData(w, filtered)
}

// HandleGetCRD returns CRD metadata and the OpenAPI schema for a specific CRD.
// Returns a combined response with CRDInfo + the storage version's schema.
//
// P3-2: non-admins must have cluster-wide list permission on the CRD's GVR;
// otherwise the response would leak the CRD's existence + schema even if the
// user couldn't enumerate instances.
func (h *GenericCRDHandler) HandleGetCRD(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	_, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	if !h.userCanAccessCRD(w, r, user, info) {
		return
	}

	dynClient, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dynamic client", err.Error())
		return
	}

	// Fetch the full CRD object from apiextensions.k8s.io/v1
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	crdName := info.Resource + "." + info.Group
	crdObj, err := dynClient.Resource(crdGVR).Get(r.Context(), crdName, metav1.GetOptions{})
	if err != nil {
		mapK8sError(w, err, "get", "CustomResourceDefinition", "", crdName)
		return
	}

	// Extract the storage version's schema
	var schemaObj interface{}
	versions, _, _ := unstructured.NestedSlice(crdObj.Object, "spec", "versions")
	for _, v := range versions {
		vMap, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if storage, _, _ := unstructured.NestedBool(vMap, "storage"); storage {
			schemaObj, _, _ = unstructured.NestedMap(vMap, "schema", "openAPIV3Schema")
			break
		}
	}

	writeData(w, map[string]interface{}{
		"info":   info,
		"schema": schemaObj,
	})
}

// HandleCRDCounts returns cached instance counts for CRDs the user can list.
//
// P3-2: shared count cache stays — counts are non-sensitive cluster aggregates
// — but the response is filtered to only CRDs the user has cluster-wide list
// permission for. Same SSAR gate as HandleListCRDs.
func (h *GenericCRDHandler) HandleCRDCounts(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	counts := h.Discovery.GetCounts(r.Context())
	if h.AccessChecker == nil || auth.IsAdmin(user) {
		writeData(w, counts)
		return
	}

	keys := make([]crdAccessKey, 0, len(counts))
	for key := range counts {
		group, resource, ok := splitGroupResourceKey(key)
		if !ok {
			h.Logger.Warn("CRD count key malformed; skipping", "key", key)
			continue
		}
		keys = append(keys, crdAccessKey{group: group, resource: resource})
	}

	allowedSet := h.batchCRDListAccess(r.Context(), user, keys)
	filtered := make(map[string]int, len(counts))
	for key, count := range counts {
		if allowedSet[key] {
			filtered[key] = count
		}
	}
	writeData(w, filtered)
}

// crdAccessKey is a (group, resource) pair used to batch SAR checks for the
// P3-2 CRD inventory filter. Review-fix REL-001 / PERF-1.
type crdAccessKey struct {
	group    string
	resource string
}

// collectCRDKeys extracts (group, resource) pairs from the ListCRDs response.
func collectCRDKeys(all map[string][]*k8s.CRDInfo) []crdAccessKey {
	var total int
	for _, infos := range all {
		total += len(infos)
	}
	keys := make([]crdAccessKey, 0, total)
	for _, infos := range all {
		for _, info := range infos {
			keys = append(keys, crdAccessKey{group: info.Group, resource: info.Resource})
		}
	}
	return keys
}

// batchCRDListAccess fans out cluster-wide list SSARs for the given (group,
// resource) keys with bounded concurrency, returning a set keyed by
// "group/resource" of CRDs the user is permitted to list. SAR errors are
// logged and treated as denied (fail-closed) — matches the documented P3-2
// contract that omits unverifiable entries from the inventory.
//
// Concurrency is bounded to crdAccessConcurrency (5) to mirror the existing
// fetchCounts pattern in crd_discovery.go without overwhelming the apiserver's
// API priority/fairness queue. Review-fix REL-001 / PERF-1.
func (h *GenericCRDHandler) batchCRDListAccess(ctx context.Context, user *auth.User, keys []crdAccessKey) map[string]bool {
	if len(keys) == 0 {
		return map[string]bool{}
	}
	clusterID := middleware.ClusterIDFromContext(ctx)

	allowed := make(map[string]bool, len(keys))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, crdAccessConcurrency)

	for _, k := range keys {
		wg.Add(1)
		go func(k crdAccessKey) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			ok, err := h.AccessChecker.CanAccessGroupResource(
				ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups,
				"list", k.group, k.resource, "",
			)
			if err != nil {
				h.Logger.Warn("CRD RBAC check failed; omitting",
					"group", k.group, "resource", k.resource, "error", err)
				return
			}
			if ok {
				mu.Lock()
				allowed[k.group+"/"+k.resource] = true
				mu.Unlock()
			}
		}(k)
	}
	wg.Wait()
	return allowed
}

// splitGroupResourceKey parses a "group/resource" key as stored in the
// CRDDiscovery count cache. Returns false on malformed input.
func splitGroupResourceKey(key string) (group, resource string, ok bool) {
	idx := strings.IndexByte(key, '/')
	if idx <= 0 || idx == len(key)-1 {
		return "", "", false
	}
	return key[:idx], key[idx+1:], true
}

// userCanAccessCRD performs a cluster-wide list SSAR for the CRD's GVR.
// Admins and the no-AccessChecker path pass through. On denial it writes a 403
// response and returns false; on success returns true; on SAR error returns
// false with a 500 response (fail closed). P3-2 security audit 2026-05-22.
func (h *GenericCRDHandler) userCanAccessCRD(w http.ResponseWriter, r *http.Request, user *auth.User, info *k8s.CRDInfo) bool {
	if h.AccessChecker == nil || auth.IsAdmin(user) {
		return true
	}
	clusterID := middleware.ClusterIDFromContext(r.Context())
	allowed, err := h.AccessChecker.CanAccessGroupResource(
		r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups,
		"list", info.Group, info.Resource, "",
	)
	if err != nil {
		h.Logger.Error("CRD RBAC check failed", "group", info.Group, "resource", info.Resource, "error", err)
		writeError(w, http.StatusInternalServerError, "permission check failed", "")
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "insufficient permissions", "")
		return false
	}
	return true
}

// HandleListCRDInstances lists instances of a CRD. Supports namespace scoping,
// limit/continue pagination, and label selectors.
func (h *GenericCRDHandler) HandleListCRDInstances(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	dynClient, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dynamic client", err.Error())
		return
	}

	params := parseListParams(r)
	ns := chi.URLParam(r, "ns")

	opts := metav1.ListOptions{
		Limit:         int64(params.Limit),
		Continue:      params.Continue,
		LabelSelector: params.LabelSelector,
	}

	var list *unstructured.UnstructuredList
	if info.Scope == "Namespaced" && ns != "" {
		list, err = dynClient.Resource(gvr).Namespace(ns).List(r.Context(), opts)
	} else {
		list, err = dynClient.Resource(gvr).List(r.Context(), opts)
	}
	if err != nil {
		mapK8sError(w, err, "list", gvr.Resource, ns, "")
		return
	}

	writeList(w, list.Items, len(list.Items), list.GetContinue())
}

// HandleGetCRDInstance gets a single CRD instance by namespace and name.
func (h *GenericCRDHandler) HandleGetCRDInstance(w http.ResponseWriter, r *http.Request) {
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

	if !h.validateInstanceParams(w, ns, name) {
		return
	}

	dynClient, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dynamic client", err.Error())
		return
	}

	var obj *unstructured.Unstructured
	if info.Scope == "Namespaced" && ns != "" {
		obj, err = dynClient.Resource(gvr).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	} else {
		obj, err = dynClient.Resource(gvr).Get(r.Context(), name, metav1.GetOptions{})
	}
	if err != nil {
		mapK8sError(w, err, "get", gvr.Resource, ns, name)
		return
	}

	writeData(w, obj)
}

// HandleCreateCRDInstance creates a new CRD instance from the JSON request body.
func (h *GenericCRDHandler) HandleCreateCRDInstance(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	gvr, info, ok := h.resolveGVR(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "ns")

	if !h.validateInstanceParams(w, ns, "") {
		return
	}

	var obj unstructured.Unstructured
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}

	dynClient, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dynamic client", err.Error())
		return
	}

	var created *unstructured.Unstructured
	if info.Scope == "Namespaced" && ns != "" {
		created, err = dynClient.Resource(gvr).Namespace(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	} else {
		created, err = dynClient.Resource(gvr).Create(r.Context(), &obj, metav1.CreateOptions{})
	}
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, gvr.Resource, ns, obj.GetName(), audit.ResultFailure)
		mapK8sError(w, err, "create", gvr.Resource, ns, obj.GetName())
		return
	}

	h.auditWrite(r, user, audit.ActionCreate, gvr.Resource, ns, created.GetName(), audit.ResultSuccess)
	writeCreated(w, created)
}

// HandleUpdateCRDInstance updates an existing CRD instance.
//
// P3-4 (security audit 2026-05-22): body metadata.name and metadata.namespace
// MUST match the URL path. A mismatch is rejected with 400 rather than silently
// updating a different object — the audit log only carries the URL name, so
// silently honoring the body would let an attacker rename "foo" while logging
// edits to "bar". Audit records the actually-returned object's name + namespace
// so a successful update is anchored to what the server saw, not what the URL
// said.
func (h *GenericCRDHandler) HandleUpdateCRDInstance(w http.ResponseWriter, r *http.Request) {
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

	if !h.validateInstanceParams(w, ns, name) {
		return
	}

	var obj unstructured.Unstructured
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}

	// P3-4: reject body/URL name + namespace mismatches before any k8s call.
	if msg, detail, ok := validateCRDUpdateIdentity(name, ns, info.Scope, obj.GetName(), obj.GetNamespace()); !ok {
		writeError(w, http.StatusBadRequest, msg, detail)
		return
	}

	dynClient, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dynamic client", err.Error())
		return
	}

	var updated *unstructured.Unstructured
	if info.Scope == "Namespaced" && ns != "" {
		updated, err = dynClient.Resource(gvr).Namespace(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	} else {
		updated, err = dynClient.Resource(gvr).Update(r.Context(), &obj, metav1.UpdateOptions{})
	}
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, gvr.Resource, ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", gvr.Resource, ns, name)
		return
	}

	// P3-4: audit the returned object's actual name + namespace so forensics
	// trace the server-acknowledged identity, not the URL-supplied one.
	// Belt-and-suspenders fallback (review-fix sec-3): if the apiserver ever
	// returns an object with empty name/namespace (theoretical edge), fall back
	// to the URL-validated values so the audit entry is never blank.
	auditName := updated.GetName()
	if auditName == "" {
		auditName = name
	}
	auditNamespace := updated.GetNamespace()
	if auditNamespace == "" && info.Scope == "Namespaced" {
		auditNamespace = ns
	}
	h.auditWrite(r, user, audit.ActionUpdate, gvr.Resource, auditNamespace, auditName, audit.ResultSuccess)
	writeData(w, updated)
}

// HandleDeleteCRDInstance deletes a CRD instance. Returns 204 No Content on success.
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

	if !h.validateInstanceParams(w, ns, name) {
		return
	}

	dynClient, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dynamic client", err.Error())
		return
	}

	if info.Scope == "Namespaced" && ns != "" {
		err = dynClient.Resource(gvr).Namespace(ns).Delete(r.Context(), name, metav1.DeleteOptions{})
	} else {
		err = dynClient.Resource(gvr).Delete(r.Context(), name, metav1.DeleteOptions{})
	}
	if err != nil {
		h.auditWrite(r, user, audit.ActionDelete, gvr.Resource, ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", gvr.Resource, ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionDelete, gvr.Resource, ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// HandleValidateCRDInstance performs a dry-run create to validate a CRD instance.
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}

	dynClient, err := h.impersonatingDynamic(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create dynamic client", err.Error())
		return
	}

	ns := chi.URLParam(r, "ns")
	dryRunOpts := metav1.CreateOptions{
		DryRun: []string{metav1.DryRunAll},
	}

	var result *unstructured.Unstructured
	if info.Scope == "Namespaced" && ns != "" {
		result, err = dynClient.Resource(gvr).Namespace(ns).Create(r.Context(), &obj, dryRunOpts)
	} else {
		result, err = dynClient.Resource(gvr).Create(r.Context(), &obj, dryRunOpts)
	}
	if err != nil {
		mapK8sError(w, err, "validate", gvr.Resource, ns, obj.GetName())
		return
	}

	writeData(w, result)
}

// validateInstanceParams validates {ns} and {name} URL params against RFC 1123 DNS label rules.
// Empty strings are allowed (they indicate cluster-scoped or list operations).
// The sentinel value "_" used by the frontend for cluster-scoped CRDs is also allowed.
func (h *GenericCRDHandler) validateInstanceParams(w http.ResponseWriter, ns, name string) bool {
	if ns != "" && ns != "_" && !k8sNameRegexp.MatchString(ns) {
		writeError(w, http.StatusBadRequest, "invalid namespace", "namespace: "+ns)
		return false
	}
	if name != "" && !k8sNameRegexp.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid resource name", "name: "+name)
		return false
	}
	return true
}

// resolveGVR extracts {group} and {resource} URL params, validates them, and
// resolves to a GroupVersionResource via Discovery. Returns false and writes an
// error response if validation or resolution fails.
func (h *GenericCRDHandler) resolveGVR(w http.ResponseWriter, r *http.Request) (schema.GroupVersionResource, *k8s.CRDInfo, bool) {
	group := chi.URLParam(r, "group")
	resource := chi.URLParam(r, "resource")

	if !dnsSubdomainRegexp.MatchString(group) {
		writeError(w, http.StatusBadRequest, "invalid API group name", "group: "+group)
		return schema.GroupVersionResource{}, nil, false
	}
	if !dnsSubdomainRegexp.MatchString(resource) {
		writeError(w, http.StatusBadRequest, "invalid resource name", "resource: "+resource)
		return schema.GroupVersionResource{}, nil, false
	}

	gvr, ok := h.Discovery.ResolveGVR(group, resource)
	if !ok {
		writeError(w, http.StatusNotFound, "CRD not found: "+group+"/"+resource, "")
		return schema.GroupVersionResource{}, nil, false
	}

	info := h.Discovery.GetCRDInfo(group, resource)
	return gvr, info, true
}

// impersonatingDynamic returns a dynamic client impersonating the authenticated user,
// routed to the correct cluster based on the X-Cluster-ID request context.
func (h *GenericCRDHandler) impersonatingDynamic(r *http.Request, user *auth.User) (dynamic.Interface, error) {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	return h.ClusterRouter.DynamicClientForCluster(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
}

// validateCRDUpdateIdentity verifies that body metadata.name / metadata.namespace
// either match the URL params or are empty. Empty body fields are allowed
// (k8s tolerates omitting them on UPDATE since they're redundant with the URL),
// but explicit mismatches are rejected. P3-4 security audit 2026-05-22.
//
// Returns (errorMessage, detail, ok). ok==true means identity is consistent.
// scope is "Namespaced" or "Cluster"; for cluster-scoped resources the
// namespace check is skipped (urlNS is expected to be "").
func validateCRDUpdateIdentity(urlName, urlNS, scope, bodyName, bodyNS string) (string, string, bool) {
	if bodyName != "" && bodyName != urlName {
		return "body metadata.name does not match URL name",
			"URL name=" + urlName + " body name=" + bodyName,
			false
	}
	if scope == "Namespaced" && urlNS != "" && bodyNS != "" && bodyNS != urlNS {
		return "body metadata.namespace does not match URL namespace",
			"URL namespace=" + urlNS + " body namespace=" + bodyNS,
			false
	}
	return "", "", true
}

// auditWrite logs an audit entry for a CRD write operation.
func (h *GenericCRDHandler) auditWrite(r *http.Request, user *auth.User, action audit.Action, kind, namespace, name string, result audit.Result) {
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         timeNow(),
		ClusterID:         middleware.ClusterIDFromContext(r.Context()),
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      kind,
		ResourceNamespace: namespace,
		ResourceName:      name,
		Result:            result,
	})
}
