package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/pkg/api"
	"github.com/kubecenter/kubecenter/pkg/version"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) registerRoutes() {
	// Health endpoints — no auth required
	s.Router.Get("/healthz", s.handleHealthz)
	s.Router.Get("/readyz", s.handleReadyz)

	// API v1
	s.Router.Route("/api/v1", func(r chi.Router) {
		r.Get("/cluster/info", s.handleClusterInfo)
	})
}

// handleHealthz is a trivial liveness check — if the server can respond, it's alive.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "ok"}})
}

// handleReadyz checks whether the server is ready to serve traffic.
// Returns 503 during startup (informer sync) and shutdown.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.ready() {
		writeJSON(w, http.StatusServiceUnavailable, api.Response{
			Error: &api.APIError{
				Code:    503,
				Message: "server is not ready",
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "ready"}})
}

// handleClusterInfo returns basic cluster information.
func (s *Server) handleClusterInfo(w http.ResponseWriter, r *http.Request) {
	cs := s.K8sClient.BaseClientset()

	serverVersion, err := cs.Discovery().ServerVersion()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to query cluster info"},
		})
		s.Logger.Error("failed to get server version", "error", err)
		return
	}

	nodes, err := s.Informers.Factory().Core().V1().Nodes().Lister().List(labels.Everything())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to list nodes"},
		})
		s.Logger.Error("failed to list nodes from informer", "error", err)
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"clusterID":        s.Config.ClusterID,
			"kubernetesVersion": serverVersion.GitVersion,
			"platform":         serverVersion.Platform,
			"nodeCount":        len(nodes),
			"kubecenter":       version.Get(),
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}
