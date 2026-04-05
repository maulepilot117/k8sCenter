package topology

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/httputil"
)

// Handler serves topology HTTP endpoints.
type Handler struct {
	Builder *Builder
	Logger  *slog.Logger
}

// HandleNamespaceGraph returns the full resource dependency graph for a namespace.
// GET /api/v1/topology/{namespace}
func (h *Handler) HandleNamespaceGraph(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")

	graph, err := h.Builder.BuildNamespaceGraph(r.Context(), namespace)
	if err != nil {
		h.Logger.Error("failed to build namespace graph", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to build topology graph", err.Error())
		return
	}

	httputil.WriteData(w, graph)
}

// HandleFocusedGraph returns a subgraph centered on a specific resource.
// GET /api/v1/topology/{namespace}/{kind}/{name}
func (h *Handler) HandleFocusedGraph(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")
	kind := chi.URLParam(r, "kind")
	name := chi.URLParam(r, "name")

	graph, err := h.Builder.BuildFocusedGraph(r.Context(), namespace, kind, name)
	if err != nil {
		h.Logger.Error("failed to build focused graph", "namespace", namespace, "kind", kind, "name", name, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to build topology graph", err.Error())
		return
	}

	httputil.WriteData(w, graph)
}

// HandleHealthSummary returns a lightweight health count for a namespace.
// GET /api/v1/topology/{namespace}/summary
func (h *Handler) HandleHealthSummary(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")

	graph, err := h.Builder.BuildNamespaceGraph(r.Context(), namespace)
	if err != nil {
		h.Logger.Error("failed to build namespace graph for summary", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to build topology summary", err.Error())
		return
	}

	summary := SummarizeHealth(graph)
	httputil.WriteData(w, summary)
}
