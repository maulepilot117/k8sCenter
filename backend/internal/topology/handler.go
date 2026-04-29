package topology

import (
	"errors"
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
		// Validation errors (unsupported overlay value) surface as 400
		// with a stable user-message and the offending value in detail —
		// matching the envelope shape used by /mesh/* peers. Everything
		// else is a 500.
		if errors.Is(err, ErrUnsupportedOverlay) {
			httputil.WriteError(w, http.StatusBadRequest, "unsupported overlay value", overlay)
			return
		}
		h.Logger.Error("failed to build namespace graph", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to build topology graph", "")
		return
	}

	httputil.WriteData(w, graph)
}
