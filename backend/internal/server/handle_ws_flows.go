package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/networking"
)

// flowSubRequest is the filter message sent by the client after auth.
type flowSubRequest struct {
	Namespace string `json:"namespace"`
	Verdict   string `json:"verdict"`
}

const (
	flowWriteWait      = 10 * time.Second
	flowPongWait       = 60 * time.Second
	flowPingPeriod     = (flowPongWait * 9) / 10
	flowMaxReadSize    = 4096 // only expect small auth/filter messages
	maxFlowConnections = 100  // concurrent flow WS connections
)

// flowWSCount tracks active flow WebSocket connections for DoS protection.
var flowWSCount atomic.Int64

// handleWSFlows handles WebSocket connections for real-time Hubble flow streaming.
// Uses a direct per-client gRPC→WS pipe instead of the Hub, because flow volume
// (100s/sec) would starve the Hub's 1024-event channel used for resource events.
// Protocol: client sends auth message (JWT), then filter message, then receives flows.
func (s *Server) handleWSFlows(w http.ResponseWriter, r *http.Request) {
	hc := s.NetworkingHandler.HubbleClient
	if hc == nil {
		http.Error(w, "Hubble is not available", http.StatusServiceUnavailable)
		return
	}

	// Connection limit — prevent goroutine/gRPC exhaustion
	if flowWSCount.Load() >= maxFlowConnections {
		http.Error(w, "too many flow connections", http.StatusServiceUnavailable)
		return
	}

	if !s.validateWSOrigin(w, r) {
		return
	}

	up := upgrader
	up.CheckOrigin = func(r *http.Request) bool { return true }
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		s.Logger.Error("flow ws upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// Prevent oversized messages before auth (DoS protection)
	conn.SetReadLimit(flowMaxReadSize)

	flowWSCount.Add(1)
	defer flowWSCount.Add(-1)

	// Step 1: Read auth message (JWT token)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var authMsg struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := conn.ReadJSON(&authMsg); err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "auth required"})
		return
	}
	if authMsg.Type != "auth" || authMsg.Token == "" {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid auth message"})
		return
	}

	claims, err := s.TokenManager.ValidateAccessToken(authMsg.Token)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid token"})
		return
	}
	user := auth.UserFromClaims(claims)

	// Step 2: Read filter message
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var filter flowSubRequest
	if err := conn.ReadJSON(&filter); err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "filter message required"})
		return
	}
	if filter.Namespace == "" || !resources.ValidateK8sName(filter.Namespace) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "valid namespace required"})
		return
	}
	if filter.Verdict != "" && !networking.ValidVerdict(filter.Verdict) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid verdict filter"})
		return
	}

	// Step 3: RBAC check — flow visibility = pod observability (SelfSubjectAccessReview, cached 60s)
	allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
		r.Context(), user.KubernetesUsername, user.KubernetesGroups,
		"list", "pods", filter.Namespace,
	)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "permission check failed"})
		return
	}
	if !allowed {
		conn.WriteJSON(map[string]any{"type": "error", "message": "no permission to view flows in this namespace"})
		return
	}

	// Confirm subscription
	conn.WriteJSON(map[string]any{
		"type":      "subscribed",
		"namespace": filter.Namespace,
		"verdict":   filter.Verdict,
	})

	s.Logger.Info("flow stream started",
		"user", user.Username,
		"namespace", filter.Namespace,
		"verdict", filter.Verdict,
	)

	// Set up context that cancels when WS closes
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Ping/pong keepalive
	conn.SetReadDeadline(time.Now().Add(flowPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(flowPongWait))
		return nil
	})

	// Read pump: detect close/errors (runs in background)
	go func() {
		defer cancel()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// Ping ticker
	ticker := time.NewTicker(flowPingPeriod)
	defer ticker.Stop()

	// Stream flows from gRPC → WebSocket
	flowCh := make(chan networking.FlowRecord, 64)
	streamErr := make(chan error, 1)

	go func() {
		err := hc.StreamFlows(ctx, filter.Namespace, filter.Verdict, func(flow networking.FlowRecord) {
			select {
			case flowCh <- flow:
			default:
				// Drop flow if channel full — client is slow, flows are ephemeral
			}
		})
		streamErr <- err
	}()

	// Write loop: send flows and pings
	for {
		select {
		case flow := <-flowCh:
			conn.SetWriteDeadline(time.Now().Add(flowWriteWait))
			msg := map[string]any{
				"type": "flow",
				"data": flow,
			}
			if err := conn.WriteJSON(msg); err != nil {
				s.Logger.Debug("flow ws write failed", "error", err)
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(flowWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case err := <-streamErr:
			if err != nil && ctx.Err() == nil {
				s.Logger.Warn("hubble flow stream error", "error", err,
					"namespace", filter.Namespace)
				conn.WriteJSON(map[string]any{
					"type":    "error",
					"message": "flow stream interrupted",
				})
			}
			return

		case <-ctx.Done():
			return
		}
	}
}
