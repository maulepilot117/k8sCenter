package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// flowSubRequest is the filter message sent by the client after auth.
type flowSubRequest struct {
	Namespace string `json:"namespace"`
	Verdict   string `json:"verdict"`
}

const (
	maxFlowConnections = 100 // concurrent flow WS connections
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

	conn, user := s.wsAuthAndUpgrade(w, r)
	if conn == nil {
		return
	}
	defer conn.Close()

	// F#1 (round-3) — Hubble flow streaming targets the LOCAL cluster's CNI
	// data plane (Hubble gRPC connection is wired to the in-cluster relay).
	// Forwarding a remote X-Cluster-ID through this handler would still
	// stream the local cluster's flows but advertise them as the remote
	// cluster's, a classic confused-deputy. Reject BEFORE the connection
	// counter, the filter read, the RBAC check, or any audit-success entry
	// — mirrors handle_ws_logs.go:102-128.
	flowsClusterID := middleware.ClusterIDFromContext(r.Context())
	if !k8s.IsLocalClusterID(flowsClusterID) {
		s.Logger.Warn("ws flow stream rejected on remote cluster",
			"user", user.Username,
			"clusterID", flowsClusterID,
		)
		if s.AuditLogger != nil {
			entry := s.newAuditEntry(r, user.Username, audit.Action("read_flows"), audit.ResultFailure)
			entry.ResourceKind = "Flow"
			entry.Detail = "ws flow stream rejected: remote clusters not supported"
			s.AuditLogger.Log(r.Context(), entry)
		}
		conn.WriteJSON(map[string]any{
			"type":    "error",
			"message": "WebSocket flow streaming not yet supported on remote clusters",
		})
		return
	}

	flowWSCount.Add(1)
	defer flowWSCount.Add(-1)

	// Read filter message
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

	// RBAC check — flow visibility = pod observability (SelfSubjectAccessReview, cached 60s).
	// flowsClusterID was already gated to local above; forward it explicitly so the
	// cluster routing in CanAccess matches the gate that ran first.
	allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
		r.Context(), flowsClusterID, user.KubernetesUsername, user.KubernetesGroups,
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

	// Ping/pong keepalive + read pump
	ticker := wsStartKeepalive(conn, cancel)
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
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			msg := map[string]any{
				"type": "flow",
				"data": flow,
			}
			if err := conn.WriteJSON(msg); err != nil {
				s.Logger.Debug("flow ws write failed", "error", err)
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
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
