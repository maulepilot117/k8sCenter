package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/pkg/api"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Handler provides HTTP handler methods for Kubernetes resource operations.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Informers     *k8s.InformerManager
	AccessChecker *AccessChecker
	AuditLogger   audit.Logger
	Logger        *slog.Logger
	TaskManager   *TaskManager
	ClusterID     string
}

// ListParams holds query parameters for list operations.
type ListParams struct {
	Namespace     string
	LabelSelector string
	Limit         int
	Continue      string
}

// parseListParams extracts list query parameters from the request.
func parseListParams(r *http.Request) ListParams {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return ListParams{
		Namespace:     chi.URLParam(r, "namespace"),
		LabelSelector: r.URL.Query().Get("labelSelector"),
		Limit:         limit,
		Continue:      r.URL.Query().Get("continue"),
	}
}

// requireUser extracts the authenticated user from context or writes a 401 error.
func requireUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required", "")
		return nil, false
	}
	return user, true
}

// impersonatingClient returns a k8s clientset impersonating the authenticated user.
func (h *Handler) impersonatingClient(user *auth.User) (*kubernetes.Clientset, error) {
	return h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
}

// impersonatingDynamic returns a dynamic client impersonating the authenticated user.
// Used for CRD resources (e.g., CiliumNetworkPolicy) that have no typed client.
func (h *Handler) impersonatingDynamic(user *auth.User) (dynamic.Interface, error) {
	return h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
}

// checkAccess verifies the user can perform the verb on the resource in the namespace.
// Returns true if allowed, writes 403 and returns false if denied.
func (h *Handler) checkAccess(w http.ResponseWriter, r *http.Request, user *auth.User, verb, resource, namespace string) bool {
	allowed, err := h.AccessChecker.CanAccess(r.Context(), user.KubernetesUsername, user.KubernetesGroups, verb, resource, namespace)
	if err != nil {
		h.Logger.Error("access check failed", "error", err, "user", user.Username, "verb", verb, "resource", resource)
		writeError(w, http.StatusInternalServerError, "failed to check permissions", err.Error())
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden,
			"you do not have permission to "+verb+" "+resource+" in namespace "+namespace,
			"RBAC: user '"+user.KubernetesUsername+"' lacks '"+verb+"' on '"+resource+"'",
		)
		return false
	}
	return true
}

// auditWrite logs an audit entry for a write operation.
func (h *Handler) auditWrite(r *http.Request, user *auth.User, action audit.Action, kind, namespace, name string, result audit.Result) {
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         timeNow(),
		ClusterID:         h.ClusterID,
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      kind,
		ResourceNamespace: namespace,
		ResourceName:      name,
		Result:            result,
	})
}

// restartWorkload performs a rolling restart by patching the pod template annotation.
// This is the shared logic used by Deployments, StatefulSets, and DaemonSets.
// The patchFn receives the impersonating clientset so it can call the correct API.
func (h *Handler) restartWorkload(w http.ResponseWriter, r *http.Request, kind, displayKind string,
	patchFn func(cs kubernetes.Interface, ctx context.Context, ns, name string) (any, error),
) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kind, ns) {
		return
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := patchFn(cs, r.Context(), ns, name)
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, displayKind, ns, name, audit.ResultFailure)
		mapK8sError(w, err, "restart", displayKind, ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, displayKind, ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// restartPatch returns the JSON patch for a rolling restart annotation.
func restartPatch() []byte {
	return []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, timeNow().Format("2006-01-02T15:04:05Z")))
}

// maxRequestBodySize is the maximum allowed request body for create/update operations (1 MB).
const maxRequestBodySize = 1 << 20

// decodeBody decodes a JSON request body into the given value.
// The body is limited to maxRequestBodySize to prevent OOM from oversized payloads.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	return json.NewDecoder(r.Body).Decode(v)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// writeError writes a JSON error response.
// Internal error details are logged server-side but not exposed to the client
// to prevent information disclosure (CLAUDE.md: "never expose internal errors to users").
func writeError(w http.ResponseWriter, status int, message, detail string) {
	if detail != "" && status >= 500 {
		slog.Error("internal error detail", "status", status, "message", message, "detail", detail)
		detail = "" // strip internal details from 5xx responses
	}
	writeJSON(w, status, api.Response{
		Error: &api.APIError{
			Code:    status,
			Message: message,
			Detail:  detail,
		},
	})
}

// writeList writes a paginated list response.
func writeList(w http.ResponseWriter, items any, total int, continueToken string) {
	writeJSON(w, http.StatusOK, api.Response{
		Data: items,
		Metadata: &api.Metadata{
			Total:    total,
			Continue: continueToken,
		},
	})
}

// writeData writes a single data item response.
func writeData(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, api.Response{Data: data})
}

// writeCreated writes a 201 Created response with the standard envelope.
func writeCreated(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusCreated, api.Response{Data: data})
}

// k8sNameRegexp matches valid RFC 1123 DNS labels used by Kubernetes for resource names.
// Must be lowercase alphanumeric, may contain '-' and '.', max 253 chars.
var k8sNameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$`)

// ValidateK8sName checks whether s is a valid Kubernetes resource name (RFC 1123 DNS label).
func ValidateK8sName(s string) bool {
	return s == "" || k8sNameRegexp.MatchString(s)
}

// ValidateURLParams is a chi middleware that validates {name} and {namespace} URL params
// against RFC 1123 DNS label rules, returning 400 before invalid values reach the k8s API.
func ValidateURLParams(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		ns := chi.URLParam(r, "namespace")
		if !ValidateK8sName(name) {
			writeError(w, http.StatusBadRequest, "invalid resource name: "+name, "")
			return
		}
		if !ValidateK8sName(ns) {
			writeError(w, http.StatusBadRequest, "invalid namespace: "+ns, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}
