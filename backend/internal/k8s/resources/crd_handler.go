package resources

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"

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

// dnsSubdomainRegexp validates DNS subdomain names used for API groups and resource names.
var dnsSubdomainRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]*[a-z0-9])?$`)

// GenericCRDHandler provides HTTP handlers for CRD CRUD operations via the dynamic client.
type GenericCRDHandler struct {
	Discovery     *k8s.CRDDiscovery
	ClusterRouter *k8s.ClusterRouter
	AuditLogger   audit.Logger
	Logger        *slog.Logger
}

// HandleListCRDs returns all discovered CRDs grouped by API group.
func (h *GenericCRDHandler) HandleListCRDs(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUser(w, r)
	if !ok {
		return
	}
	writeData(w, h.Discovery.ListCRDs())
}

// HandleGetCRD returns CRD metadata and the OpenAPI schema for a specific CRD.
// Returns a combined response with CRDInfo + the storage version's schema.
func (h *GenericCRDHandler) HandleGetCRD(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	_, info, ok := h.resolveGVR(w, r)
	if !ok {
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

// HandleCRDCounts returns cached instance counts for all discovered CRDs.
func (h *GenericCRDHandler) HandleCRDCounts(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUser(w, r)
	if !ok {
		return
	}
	writeData(w, h.Discovery.GetCounts(r.Context()))
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

	h.auditWrite(r, user, audit.ActionUpdate, gvr.Resource, ns, name, audit.ResultSuccess)
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
