package yaml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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
	"k8s.io/apimachinery/pkg/api/meta"
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
	TargetFor(ctx context.Context, clusterID, username string, groups []string) (*k8s.ClientPair, *k8s.TargetSchema, error)
}

// Handler provides HTTP handlers for YAML operations.
type Handler struct {
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
	mapper := (&schemaRefresh{target: target}).mapper()

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

	query := r.URL.Query()
	force := query.Get("force") == "true"

	// D4 / AE2 — a pinned apply runs only against the cluster the operator
	// previewed. The cluster half of the pin is checked before routing, so a
	// mismatch costs no remote round trip; the generation half needs the
	// resolved target. Both refusals happen before any document is applied.
	clusterID := middleware.ClusterIDFromContext(r.Context())
	pin := parseTargetPin(query)
	if pin.TargetCluster != "" && k8s.NormalizedClusterID(pin.TargetCluster) != k8s.NormalizedClusterID(clusterID) {
		h.refusePin(w, r, user, clusterID, "the cluster you previewed is not the cluster this request targets",
			"cluster_pin_mismatch", map[string]any{
				"pinnedClusterId":  k8s.NormalizedClusterID(pin.TargetCluster),
				"requestClusterId": k8s.NormalizedClusterID(clusterID),
			})
		return
	}

	// F#7 / F#19 — see HandleValidate.
	pair, target, ok := h.resolveTarget(w, r, user)
	if !ok {
		return
	}
	if pin.TargetGeneration != "" && pin.TargetGeneration != target.Generation {
		h.refusePin(w, r, user, clusterID, "the cluster you previewed has been re-registered since the preview",
			"cluster_generation_mismatch", map[string]any{
				"pinnedGeneration": pin.TargetGeneration,
				"targetGeneration": target.Generation,
			})
		return
	}
	dynClient := pair.Dynamic
	mapper := newApplySchemaRefresh(target).mapper()

	resp := ApplyDocuments(r.Context(), dynClient, mapper, docs, force, h.Logger)

	// Audit log each document apply. F#6 — record the per-request cluster ID
	// from the request context (not a static per-handler value) so the audit
	// row points at the cluster the apply actually targeted.
	auditClusterID := target.ClusterID
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

	mapper := (&schemaRefresh{target: target}).mapper()
	resp := DiffDocuments(r.Context(), pair.Dynamic, mapper, docs, h.Logger)
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

	refresh := &schemaRefresh{target: target}
	gvr, err := resolveGVR(target.Discovery, kind)
	if err != nil && refresh.once() {
		gvr, err = resolveGVR(target.Discovery, kind)
	}
	if errors.Is(err, errKindNotServed) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("unknown resource kind: %s", kind), err.Error())
		return
	}
	if err != nil {
		// Discovery failed or came back incomplete, so the kind can be
		// neither confirmed nor ruled out: the target cluster is at fault,
		// not the caller's request.
		httputil.WriteError(w, http.StatusBadGateway, "failed to discover resource kinds on the target cluster", err.Error())
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

// parseTargetPin reads the pin an apply echoes back from its preview. The
// apply body is raw YAML, so the pin travels as query parameters. Absent
// fields mean "unpinned" and keep the pre-pinning contract for existing
// clients, mobile included.
func parseTargetPin(q url.Values) targetPin {
	return targetPin{TargetCluster: q.Get("targetCluster"), TargetGeneration: q.Get("targetGeneration")}
}

// refusePin answers 409 for an apply whose pin disagrees with its target and
// audits the refusal: a refused apply is exactly what an operator looks for
// in the audit trail, and the 409 alone would leave no record.
func (h *Handler) refusePin(w http.ResponseWriter, r *http.Request, user *auth.User, clusterID, message, reason string, extra map[string]any) {
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp: time.Now(),
		ClusterID: k8s.NormalizedClusterID(clusterID),
		User:      user.Username,
		SourceIP:  r.RemoteAddr,
		Action:    audit.ActionApply,
		Result:    audit.ResultFailure,
		Detail:    reason,
	})
	httputil.WriteErrorWithReason(w, http.StatusConflict, message, reason, extra)
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

// applyRESTMappingAttempts mirrors applyOne's RESTMapping retry loop in
// applier.go (3 attempts, 500ms/1s backoff between them, waiting for a CRD
// applied earlier in the same bundle to reach discovery). It lives here,
// rather than being read from applier.go, because it bounds a budget this
// file owns: the number of schema invalidations HandleApply's per-GroupKind
// refresh policy allows for a single GroupKind. Keep the two numbers in sync
// if applyOne's attempt count ever changes.
const applyRESTMappingAttempts = 3

// schemaRefresh lets a request invalidate a remote target's cached discovery
// on a RESTMapping miss. The cached remote mapper never resets itself on a
// miss (memCacheClient.Fresh stays true once populated), so a CRD installed
// on the remote cluster after the cache entry was built would otherwise stay
// unknown until the entry expires. A local target is never refreshed: its
// discovery is process-shared and TargetSchema.Invalidate is deliberately
// inert there.
//
// Two budgets are supported, selected by construction:
//   - The zero value (used by validate/diff/export) invalidates at most once
//     for the whole request, however many documents or distinct kinds miss.
//     These paths have no caller-side retry loop of their own, so a bundle of
//     unknown kinds is bounded to a single re-discovery against the remote
//     API server.
//   - newApplySchemaRefresh (used by apply) invalidates at most once per
//     RESTMapping miss, capped per GroupKind at applyRESTMappingAttempts.
//     applyOne retries RESTMapping applyRESTMappingAttempts times specifically
//     to give a CRD applied earlier in the same bundle time to reach
//     discovery on a real API server, where discovery lags the CRD patch: the
//     first invalidation fires immediately, before any backoff wait, and so
//     still repopulates the cache with the stale set. Granting each retry its
//     own invalidation lets a later attempt (after its wait) observe the CRD
//     once discovery has caught up, while the per-GroupKind cap keeps a
//     bundle containing one truly unserved kind from invalidating unboundedly.
type schemaRefresh struct {
	target  *k8s.TargetSchema
	done    bool                     // once-per-request budget state
	perKind map[schema.GroupKind]int // non-nil selects the per-GroupKind apply budget
}

// newApplySchemaRefresh returns a schemaRefresh using the per-GroupKind
// retry budget described above. Only HandleApply uses this constructor;
// HandleValidate, HandleDiff and HandleExport use the zero value.
func newApplySchemaRefresh(target *k8s.TargetSchema) *schemaRefresh {
	return &schemaRefresh{target: target, perKind: make(map[schema.GroupKind]int)}
}

// once invalidates the remote target's schema under the once-per-request
// budget if this request has not done so yet, and reports whether it did (so
// the caller should retry). Called directly by HandleExport's resolveGVR
// fallback, which has no GroupKind to key a per-kind budget by, and via
// invalidateOnMiss for every schemaRefresh built with the zero value.
func (s *schemaRefresh) once() bool {
	if s.target.IsLocal || s.done {
		return false
	}
	s.done = true
	s.target.Invalidate()
	return true
}

// invalidateOnMiss invalidates the schema for a RESTMapping miss on gk under
// whichever budget this schemaRefresh was constructed with, and reports
// whether it did (so the caller should retry).
func (s *schemaRefresh) invalidateOnMiss(gk schema.GroupKind) bool {
	if s.perKind == nil {
		return s.once()
	}
	if s.target.IsLocal || s.perKind[gk] >= applyRESTMappingAttempts {
		return false
	}
	s.perKind[gk]++
	s.target.Invalidate()
	return true
}

// mapper returns the target's RESTMapper, wrapped for a remote target so a
// no-match refreshes the schema (under whichever budget this schemaRefresh
// was constructed with) and retries.
func (s *schemaRefresh) mapper() meta.RESTMapper {
	if s.target.IsLocal {
		return s.target.Mapper
	}
	return refreshingMapper{RESTMapper: s.target.Mapper, refresh: s}
}

// refreshingMapper retries RESTMapping once per call after a schema refresh.
// Only RESTMapping is wrapped: it is the one method DiffDocuments and
// applyOne use. For apply, applyOne's own outer retry loop supplies the
// repeated calls that let the per-GroupKind budget span multiple attempts.
type refreshingMapper struct {
	meta.RESTMapper
	refresh *schemaRefresh
}

func (m refreshingMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	mapping, err := m.RESTMapper.RESTMapping(gk, versions...)
	if meta.IsNoMatchError(err) && m.refresh.invalidateOnMiss(gk) {
		return m.RESTMapper.RESTMapping(gk, versions...)
	}
	return mapping, err
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

// errKindNotServed means discovery answered completely and no API group
// serves the requested resource: the caller named a kind that does not exist.
var errKindNotServed = errors.New("resource not served by the API server")

// resolveGVR resolves a plural resource name to a GroupVersionResource using
// the TARGET cluster's discovery. Passing the local ClientFactory's discovery
// here is the bug this signature change exists to prevent.
//
// A miss wraps errKindNotServed only when discovery was complete. When some
// API groups failed to load, a miss cannot tell "not installed" from "in a
// group we never saw", so it returns the discovery error instead.
func resolveGVR(disc discovery.DiscoveryInterface, kind string) (schema.GroupVersionResource, error) {
	kind = strings.ToLower(kind)

	_, apiResourceLists, discErr := disc.ServerGroupsAndResources()
	if discErr != nil && apiResourceLists == nil {
		// ServerGroupsAndResources may return partial results with an error
		// for unavailable API groups. Only fail outright if nothing loaded.
		return schema.GroupVersionResource{}, fmt.Errorf("discovering API resources: %w", discErr)
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

	if discErr != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("resource %q not found and discovery was incomplete: %w", kind, discErr)
	}
	return schema.GroupVersionResource{}, fmt.Errorf("resource %q: %w", kind, errKindNotServed)
}
