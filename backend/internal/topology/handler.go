package topology

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Handler serves topology HTTP endpoints.
type Handler struct {
	Builder       *Builder
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger
}

// HandleNamespaceGraph returns the full resource dependency graph for a namespace.
// GET /api/v1/topology/{namespace}
//
// Optional query parameter:
//
//	?overlay=mesh — adds service-to-service mesh edges (Istio VirtualService
//	  and Linkerd ServiceProfile routing) when the caller has list
//	  permission on the underlying CRDs. Without this parameter the response
//	  is byte-identical to the no-overlay path.
func (h *Handler) HandleNamespaceGraph(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	overlay := r.URL.Query().Get("overlay")

	graph, err := h.Builder.BuildNamespaceGraphWithOverlay(r.Context(), namespace, user, h.AccessChecker, overlay)
	if err != nil {
		// Validation errors (e.g., unsupported overlay value) surface as 400
		// with a user-safe message; everything else is a 500.
		if isInvalidOverlayErr(err) {
			httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		h.Logger.Error("failed to build namespace graph", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to build topology graph", "")
		return
	}

	httputil.WriteData(w, graph)
}

// isInvalidOverlayErr reports whether err is a builder-level validation
// failure for an unsupported overlay value. Kept narrow so unrelated
// errors don't get misclassified as 400.
func isInvalidOverlayErr(err error) bool {
	if err == nil {
		return false
	}
	const prefix = "unsupported overlay "
	return len(err.Error()) >= len(prefix) && err.Error()[:len(prefix)] == prefix
}
