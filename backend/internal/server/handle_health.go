package server

import (
	"net/http"

	"github.com/kubecenter/kubecenter/pkg/api"
)

// handleHealthz is a trivial liveness check — if the server can respond, it's alive.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.Response{Data: map[string]string{"status": "ok"}})
}

// handleReadyz checks whether the server is ready to serve traffic.
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
