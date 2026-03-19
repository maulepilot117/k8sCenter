package server

import (
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	ws "github.com/kubecenter/kubecenter/internal/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     nil, // set per-handler after origin validation
}

// validateWSOrigin validates the Origin header for WebSocket connections.
// Returns true if the origin is acceptable, false if the request was rejected.
// In production, requires a valid Origin header to prevent CSWSH attacks.
// In dev mode, allows empty Origin for non-browser clients (curl, CLI tools).
func (s *Server) validateWSOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" && !s.Config.Dev {
		s.Logger.Warn("websocket rejected: missing Origin header")
		http.Error(w, "Origin header required", http.StatusForbidden)
		return false
	}
	if origin != "" && !s.isAllowedOrigin(origin) {
		s.Logger.Warn("websocket origin rejected", "origin", origin)
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return false
	}
	return true
}

// handleWSResources handles WebSocket upgrade for the resource event stream.
func (s *Server) handleWSResources(w http.ResponseWriter, r *http.Request) {
	if s.Hub == nil {
		http.Error(w, "websocket not available", http.StatusServiceUnavailable)
		return
	}

	if s.Hub.ClientCount() >= ws.MaxClients {
		http.Error(w, "too many WebSocket connections", http.StatusServiceUnavailable)
		return
	}

	if !s.validateWSOrigin(w, r) {
		return
	}

	up := upgrader
	up.CheckOrigin = func(r *http.Request) bool { return true }

	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		s.Logger.Error("websocket upgrade failed", "error", err)
		return
	}

	client := ws.NewClient(s.Hub, conn, s.TokenManager, s.Logger)

	s.Hub.Register(client)

	go client.WritePump()
	go client.ReadPump()
}

// isAllowedOrigin checks if the origin is in the allowed origins list.
func (s *Server) isAllowedOrigin(origin string) bool {
	if s.Config.Dev {
		return true
	}
	for _, allowed := range s.Config.CORS.AllowedOrigins {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	return false
}
