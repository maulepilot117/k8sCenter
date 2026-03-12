package resources

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/pkg/api"
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
}

// ListParams holds query parameters for list operations.
type ListParams struct {
	Namespace     string
	LabelSelector string
	FieldSelector string
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
		FieldSelector: r.URL.Query().Get("fieldSelector"),
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
		ClusterID:         "", // filled by the caller or we could pass config
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      kind,
		ResourceNamespace: namespace,
		ResourceName:      name,
		Result:            result,
	})
}

// decodeBody decodes a JSON request body into the given value.
func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
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
func writeError(w http.ResponseWriter, status int, message, detail string) {
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
