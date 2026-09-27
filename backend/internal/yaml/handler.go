package yaml

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// clusterTargeter is the subset of *k8s.ClusterRouter the YAML handlers use.
// It is an interface rather than the concrete router only so remote_test.go
// can inject a remote TargetSchema: from outside package k8s a remote target
// cannot be pointed at a test server (the SSRF policy blocks loopback and
// TargetSchemaFor needs a live cluster store) — the same reason k8s's
// clusterGetter and server's clusterRecordGetter exist. Production always
// assigns a *k8s.ClusterRouter.
type clusterTargeter interface {
	RouterFor(ctx context.Context, clusterID, username string, groups []string) (*k8s.ClientPair, error)
	TargetFor(ctx context.Context, clusterID, username string, groups []string) (*k8s.ClientPair, *k8s.TargetSchema, error)
}

// Handler provides HTTP handlers for YAML operations.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	ClusterRouter clusterTargeter
	AuditLogger   audit.Logger
	Logger        *slog.Logger
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

	// F#7 / F#19 — the client and the RESTMapper both come from the header's
	// cluster in one call, so a CRD that exists only remotely resolves
	// against the cluster the dry-run executes on.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}
	dynClient := pair.Dynamic
	mapper := target.Mapper

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
		targetPin
	}

	resp := validateResponse{
		Documents: make([]docResult, 0, len(docs)),
		Valid:     true,
		targetPin: pinFor(target),
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

	clusterID := middleware.ClusterIDFromContext(r.Context())
	pair, err := h.ClusterRouter.RouterFor(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return
	}
	// F#7 / F#19 — see HandleValidate. Remote-cluster YAML apply needs a
	// remote-discovery-backed RESTMapper before it can be re-enabled.
	if !pair.IsLocal {
		httputil.WriteError(w, http.StatusNotImplemented,
			"YAML apply is not yet supported on remote clusters",
			"Connect to that cluster directly to use this feature")
		return
	}
	dynClient := pair.Dynamic
	mapper := h.K8sClient.RESTMapper()

	resp := ApplyDocuments(r.Context(), dynClient, mapper, docs, force, h.Logger)

	// Audit log each document apply. F#6 — record the per-request cluster ID
	// from the request context (not a static per-handler value) so the audit
	// row points at the cluster the apply actually targeted.
	auditClusterID := clusterID
	for _, result := range resp.Results {
		auditResult := audit.ResultSuccess
		if result.Action == "failed" {
			auditResult = audit.ResultFailure
		}
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp:         time.Now(),
			ClusterID:         auditClusterID,
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

	// F#7 / F#19 — see HandleValidate. The Secret refusal above stays ahead
	// of routing so it holds identically for every target.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}

	resp := DiffDocuments(r.Context(), pair.Dynamic, target.Mapper, docs, h.Logger)
	httputil.WriteData(w, struct {
		*DiffResponse
		targetPin
	}{resp, pinFor(target)})
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

	// F#7 / F#19 — the GVR resolves through the header cluster's discovery,
	// so a CRD that exists only remotely is visible to the export.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}
	dynClient := pair.Dynamic

	gvr, err := resolveGVR(target.Discovery, kind)
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

	// ?expectUID= lets a caller that already holds an object's identity
	// refuse a same-name replacement: the export strips metadata.uid, so the
	// caller could not tell otherwise. Absent, nothing changes.
	if !uidMatchesExpected(obj, r.URL.Query().Get("expectUID")) {
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			fmt.Sprintf("%s '%s' was replaced by a different object with the same name", kind, name),
			"uid_mismatch", nil)
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

// targetPin identifies the cluster schema a validate or diff response was
// computed against. The client stores it so a later apply can refuse to run
// against a different cluster (or a re-registered one under the same ID)
// than the one the operator reviewed.
type targetPin struct {
	TargetCluster    string `json:"targetCluster"`
	TargetGeneration string `json:"targetGeneration"`
}

func pinFor(target *k8s.TargetSchema) targetPin {
	return targetPin{TargetCluster: target.ClusterID, TargetGeneration: target.Generation}
}

// resolveTarget resolves the client pair and schema for the request's
// X-Cluster-ID in a single TargetFor call and writes the error response
// itself on failure. There is no local fallback: a remote target that cannot
// be resolved fails the request.
func (h *Handler) resolveTarget(w http.ResponseWriter, r *http.Request, user *auth.User) (*k8s.ClientPair, *k8s.TargetSchema, bool) {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	pair, target, err := h.ClusterRouter.TargetFor(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return nil, nil, false
	}
	return pair, target, true
}

// uidMatchesExpected reports whether obj is the object the caller expects.
// An empty expectation matches anything, so callers that do not send one
// keep the endpoint's original behaviour.
func uidMatchesExpected(obj *unstructured.Unstructured, expected string) bool {
	return expected == "" || string(obj.GetUID()) == expected
}

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

// resolveGVR resolves a plural resource name to a GroupVersionResource using
// the TARGET cluster's discovery. Passing the local ClientFactory's discovery
// here is the bug this signature change exists to prevent.
func resolveGVR(disc discovery.DiscoveryInterface, kind string) (schema.GroupVersionResource, error) {
	kind = strings.ToLower(kind)

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
