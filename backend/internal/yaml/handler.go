package yaml

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Handler provides HTTP handlers for YAML operations.
type Handler struct {
	K8sClient   *k8s.ClientFactory
	AuditLogger audit.Logger
	Logger      *slog.Logger
	ClusterID   string
}

// HandleValidate validates YAML against the cluster's schema using dry-run apply.
// POST /api/v1/yaml/validate
func (h *Handler) HandleValidate(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := readYAMLBody(w, r)
	if err != nil {
		return
	}

	docs, err := ParseMultiDoc(data)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid YAML: "+err.Error(), "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return
	}
	mapper := h.K8sClient.RESTMapper()

	type validationError struct {
		Field   string `json:"field,omitempty"`
		Message string `json:"message"`
	}
	type docResult struct {
		Index     int               `json:"index"`
		Kind      string            `json:"kind"`
		Name      string            `json:"name"`
		Namespace string            `json:"namespace,omitempty"`
		Valid     bool              `json:"valid"`
		Errors    []validationError `json:"errors,omitempty"`
	}
	type validateResponse struct {
		Documents []docResult `json:"documents"`
		Valid     bool        `json:"valid"`
	}

	resp := validateResponse{
		Documents: make([]docResult, 0, len(docs)),
		Valid:     true,
	}

	for i, obj := range docs {
		dr := docResult{
			Index:     i,
			Kind:      obj.GetKind(),
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
			Valid:     true,
		}

		// Dry-run apply validates against the cluster's schema
		diffResp := DiffDocuments(r.Context(), dynClient, mapper, docs[i:i+1], h.Logger)
		if len(diffResp.Documents) > 0 && diffResp.Documents[0].Error != "" {
			dr.Valid = false
			dr.Errors = []validationError{{
				Message: diffResp.Documents[0].Error,
			}}
			resp.Valid = false
		}

		resp.Documents = append(resp.Documents, dr)
	}

	httputil.WriteData(w, resp)
}

// HandleApply applies YAML via server-side apply.
// POST /api/v1/yaml/apply?force=true
func (h *Handler) HandleApply(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := readYAMLBody(w, r)
	if err != nil {
		return
	}

	docs, err := ParseMultiDoc(data)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid YAML: "+err.Error(), "")
		return
	}

	force := r.URL.Query().Get("force") == "true"

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return
	}
	mapper := h.K8sClient.RESTMapper()

	resp := ApplyDocuments(r.Context(), dynClient, mapper, docs, force, h.Logger)

	// Audit log each document apply
	for _, result := range resp.Results {
		auditResult := audit.ResultSuccess
		if result.Action == "failed" {
			auditResult = audit.ResultFailure
		}
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp:         time.Now(),
			ClusterID:         h.ClusterID,
			User:              user.Username,
			SourceIP:          r.RemoteAddr,
			Action:            audit.ActionApply,
			ResourceKind:      result.Kind,
			ResourceNamespace: result.Namespace,
			ResourceName:      result.Name,
			Result:            auditResult,
			Detail:            result.Action,
		})
	}

	httputil.WriteData(w, resp)
}

// HandleDiff performs a dry-run apply and returns current vs proposed YAML.
// POST /api/v1/yaml/diff
func (h *Handler) HandleDiff(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := readYAMLBody(w, r)
	if err != nil {
		return
	}

	docs, err := ParseMultiDoc(data)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid YAML: "+err.Error(), "")
		return
	}

	// Block diff for Secrets to prevent leaking unmasked values
	for _, doc := range docs {
		if strings.EqualFold(doc.GetKind(), "Secret") {
			httputil.WriteError(w, http.StatusUnprocessableEntity,
				"Secrets cannot be diffed via YAML to prevent accidental data exposure. Use the Secrets management interface.",
				"")
			return
		}
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return
	}
	mapper := h.K8sClient.RESTMapper()

	resp := DiffDocuments(r.Context(), dynClient, mapper, docs, h.Logger)
	httputil.WriteData(w, resp)
}

// HandleExport exports a resource as clean, reapply-ready YAML.
// GET /api/v1/yaml/export/{kind}/{namespace}/{name}
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	kind := chi.URLParam(r, "kind")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if kind == "" || name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "kind and name are required", "")
		return
	}

	// Validate URL params to prevent injection (matches resource route validation)
	if !resources.ValidateK8sName(name) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid resource name: "+name, "")
		return
	}
	if namespace != "_" && !resources.ValidateK8sName(namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace: "+namespace, "")
		return
	}

	// Block secret export to prevent data loss (D1)
	if strings.EqualFold(kind, "secrets") || strings.EqualFold(kind, "secret") {
		httputil.WriteError(w, http.StatusUnprocessableEntity,
			"Secrets cannot be exported via YAML to prevent accidental data loss. Use the Secrets management interface with audit-logged reveal.",
			"")
		return
	}

	// Use "_" for cluster-scoped resources (no namespace)
	if namespace == "_" {
		namespace = ""
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return
	}

	gvr, err := resolveGVR(h.K8sClient, kind)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("unknown resource kind: %s", kind), err.Error())
		return
	}

	var obj *unstructured.Unstructured
	if namespace != "" {
		obj, err = dynClient.Resource(gvr).Namespace(namespace).Get(r.Context(), name, metav1.GetOptions{})
	} else {
		obj, err = dynClient.Resource(gvr).Get(r.Context(), name, metav1.GetOptions{})
	}
	if err != nil {
		if apierrors.IsNotFound(err) {
			httputil.WriteError(w, http.StatusNotFound, fmt.Sprintf("%s '%s' not found", kind, name), "")
		} else if apierrors.IsForbidden(err) {
			httputil.WriteError(w, http.StatusForbidden, fmt.Sprintf("permission denied for %s '%s'", kind, name), "")
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get %s '%s'", kind, name), err.Error())
		}
		return
	}

	yamlBytes, err := ExportToYAML(obj)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to export YAML", err.Error())
		return
	}

	// Return YAML as a string inside the standard JSON envelope so
	// the frontend api() wrapper (which calls res.json()) works correctly.
	httputil.WriteData(w, string(yamlBytes))
}

// --- Helpers ---

// readYAMLBody reads and validates the raw YAML body from the request.
func readYAMLBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodySize)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			httputil.WriteError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds maximum size of %d bytes", MaxBodySize), "")
		} else {
			httputil.WriteError(w, http.StatusBadRequest, "failed to read request body", err.Error())
		}
		return nil, err
	}

	if err := CheckSecurity(data); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return nil, err
	}

	return data, nil
}

// resolveGVR resolves a plural resource kind name to a GroupVersionResource
// using the API server's discovery API.
func resolveGVR(clientFactory *k8s.ClientFactory, kind string) (schema.GroupVersionResource, error) {
	kind = strings.ToLower(kind)

	disc := clientFactory.DiscoveryClient()
	_, apiResourceLists, err := disc.ServerGroupsAndResources()
	if err != nil {
		// ServerGroupsAndResources may return partial results with an error
		// for unavailable API groups. Only fail if no results were returned.
		if apiResourceLists == nil {
			return schema.GroupVersionResource{}, fmt.Errorf("discovering API resources: %w", err)
		}
	}

	for _, list := range apiResourceLists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.EqualFold(r.Name, kind) {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name,
				}, nil
			}
		}
	}

	return schema.GroupVersionResource{}, fmt.Errorf("resource %q not found in API server", kind)
}
