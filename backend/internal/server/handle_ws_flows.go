package server

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/networking"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// flowSubRequest is the filter message sent by the client after auth.
type flowSubRequest struct {
	Namespace string `json:"namespace"`
	Verdict   string `json:"verdict"`
}

var flowNSRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$`)

const (
	flowWriteWait  = 10 * time.Second
	flowPongWait   = 60 * time.Second
	flowPingPeriod = (flowPongWait * 9) / 10
)

// handleWSFlows handles WebSocket connections for real-time Hubble flow streaming.
// Protocol: client sends auth message (JWT), then filter message, then receives flows.
func (s *Server) handleWSFlows(w http.ResponseWriter, r *http.Request) {
	hc := s.NetworkingHandler.HubbleClient
	if hc == nil {
		http.Error(w, "Hubble is not available", http.StatusServiceUnavailable)
		return
	}

	// Validate origin (same as resource WS)
	origin := r.Header.Get("Origin")
	if origin == "" && !s.Config.Dev {
		http.Error(w, "Origin header required", http.StatusForbidden)
		return
	}
	if origin != "" && !s.isAllowedOrigin(origin) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
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
	if filter.Namespace == "" || !flowNSRegexp.MatchString(filter.Namespace) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "valid namespace required"})
		return
	}
	if filter.Verdict != "" && !networking.ValidVerdict(filter.Verdict) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid verdict filter"})
		return
	}

	// Step 3: RBAC check — can user list pods in the namespace? (flow visibility = pod observability)
	cs, err := s.NetworkingHandler.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "permission check failed"})
		return
	}
	_, err = cs.CoreV1().Pods(filter.Namespace).List(r.Context(), k8smetav1.ListOptions{Limit: 1})
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "no permission to view flows in " + filter.Namespace})
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

	// Ping/pong keepalive in a goroutine
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
	// The callback is called from the gRPC stream goroutine.
	// We use a channel to decouple gRPC recv from WS write.
	flowCh := make(chan networking.FlowRecord, 64)
	streamErr := make(chan error, 1)

	go func() {
		err := hc.StreamFlows(ctx, filter.Namespace, filter.Verdict, func(flow networking.FlowRecord) {
			select {
			case flowCh <- flow:
			default:
				// Drop flow if channel full — client is slow
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
