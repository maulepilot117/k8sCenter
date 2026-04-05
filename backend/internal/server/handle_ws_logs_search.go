package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/loki"
)

// logSearchSubRequest is the filter message sent by the client after auth.
type logSearchSubRequest struct {
	Type      string `json:"type"`      // "subscribe"
	Query     string `json:"query"`     // LogQL query
	StartNano int64  `json:"start"`     // nanosecond timestamp (optional)
	Limit     int    `json:"limit"`     // max entries per batch (optional, default 30)
	Namespace string `json:"namespace"` // required for non-admin users
}

const maxLogSearchConnections = 50

// logSearchWSCount tracks active log search WebSocket connections.
var logSearchWSCount atomic.Int64

// handleWSLogsSearch handles WebSocket connections for real-time Loki log tailing.
// Protocol: client sends auth message (JWT), then subscribe message with LogQL query,
// then receives log stream messages from Loki.
func (s *Server) handleWSLogsSearch(w http.ResponseWriter, r *http.Request) {
	lokiClient := s.LokiHandler.Discoverer.Client()
	if lokiClient == nil {
		http.Error(w, "Loki is not available", http.StatusServiceUnavailable)
		return
	}

	if logSearchWSCount.Load() >= maxLogSearchConnections {
		http.Error(w, "too many log search connections", http.StatusServiceUnavailable)
		return
	}

	conn, user := s.wsAuthAndUpgrade(w, r)
	if conn == nil {
		return
	}
	defer conn.Close()

	logSearchWSCount.Add(1)
	defer logSearchWSCount.Add(-1)

	// Read subscribe message
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var sub logSearchSubRequest
	if err := conn.ReadJSON(&sub); err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "subscribe message required"})
		return
	}
	if sub.Query == "" {
		conn.WriteJSON(map[string]any{"type": "error", "message": "query is required"})
		return
	}

	// Enforce namespace scoping on the LogQL query
	var allowedNamespaces []string
	if !auth.IsAdmin(user) {
		if sub.Namespace == "" {
			conn.WriteJSON(map[string]any{"type": "error", "message": "namespace required for non-admin users"})
			return
		}

		// P1-4 fix: validate namespace against Kubernetes RBAC (same pattern as handleWSFlows)
		if s.ResourceHandler != nil && s.ResourceHandler.AccessChecker != nil {
			allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
				r.Context(), user.KubernetesUsername, user.KubernetesGroups,
				"list", "pods", sub.Namespace,
			)
			if err != nil {
				conn.WriteJSON(map[string]any{"type": "error", "message": "permission check failed"})
				return
			}
			if !allowed {
				conn.WriteJSON(map[string]any{"type": "error", "message": "no permission to view logs in this namespace"})
				return
			}
		}

		allowedNamespaces = []string{sub.Namespace}
	}

	enforcedQuery, err := loki.EnforceNamespaces(sub.Query, allowedNamespaces)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid query: " + err.Error()})
		return
	}

	// Confirm subscription
	conn.WriteJSON(map[string]any{
		"type":  "subscribed",
		"query": enforcedQuery,
	})

	s.Logger.Info("log search stream started",
		"user", user.Username,
		"query", enforcedQuery,
	)

	// Set up context
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Ping/pong keepalive + read pump
	ticker := wsStartKeepalive(conn, cancel)
	defer ticker.Stop()

	// Connect to Loki tail WebSocket
	limit := sub.Limit
	if limit <= 0 {
		limit = 30
	}
	tailURL := lokiClient.TailURL(enforcedQuery, sub.StartNano, limit)

	// Add tenant header via dialer
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	headers := http.Header{}
	if tenantID := s.LokiHandler.Discoverer.TenantID(); tenantID != "" {
		headers.Set("X-Scope-OrgID", tenantID)
	}

	lokiConn, _, err := dialer.DialContext(ctx, tailURL, headers)
	if err != nil {
		s.Logger.Error("failed to connect to loki tail", "url", tailURL, "error", err)
		conn.WriteJSON(map[string]any{"type": "error", "message": "failed to connect to log stream"})
		return
	}
	defer lokiConn.Close()

	// Bridge: Loki tail → client WebSocket
	lokiCh := make(chan json.RawMessage, 64)
	lokiErr := make(chan error, 1)

	go func() {
		for {
			_, message, err := lokiConn.ReadMessage()
			if err != nil {
				lokiErr <- err
				return
			}
			select {
			case lokiCh <- json.RawMessage(message):
			default:
				// Drop if channel full — client is slow
			}
		}
	}()

	// Write loop
	for {
		select {
		case msg := <-lokiCh:
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			// Forward Loki's tail message wrapped in our protocol
			wrapped := map[string]any{
				"type":    "log",
				"payload": msg,
			}
			if err := conn.WriteJSON(wrapped); err != nil {
				s.Logger.Debug("log search ws write failed", "error", err)
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case err := <-lokiErr:
			if err != nil && ctx.Err() == nil {
				s.Logger.Warn("loki tail stream error", "error", err)
				conn.WriteJSON(map[string]any{
					"type":    "error",
					"message": "log stream disconnected",
				})
			}
			return

		case <-ctx.Done():
			return
		}
	}
}
