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

// flowsWSRoute is the flows stream's cluster contract: it must name its
// cluster, and it alone may name a remote one in-band (#608 KTD10).
var flowsWSRoute = wsRoute{
	acceptsRemoteInBand: true,
	requireCluster:      true,
	auditAction:         audit.Action("read_flows"),
	auditKind:           "Flow",
}

// wsReasonHubbleUnavailable answers a local flows stream when the local
// cluster has no Hubble client.
const wsReasonHubbleUnavailable = "hubble_unavailable"

// handleWSFlows handles WebSocket connections for real-time Hubble flow streaming.
// Uses a direct per-client gRPC→WS pipe instead of the Hub, because flow volume
// (100s/sec) would starve the Hub's 1024-event channel used for resource events.
// Protocol: client sends auth message (JWT), then filter message, then receives flows.
func (s *Server) handleWSFlows(w http.ResponseWriter, r *http.Request) {
	// Connection limit — prevent goroutine/gRPC exhaustion
	if flowWSCount.Load() >= maxFlowConnections {
		http.Error(w, "too many flow connections", http.StatusServiceUnavailable)
		return
	}

	// The stream must name its cluster, in-band or by a header that was
	// actually sent: a connection naming none is refused rather than served
	// as local, so an older bundle under a remote selection can never show
	// local flows (#608 KTD10, AE1).
	conn, user, reqCtx := s.wsAuthAndUpgradeFor(w, r, flowsWSRoute)
	if conn == nil {
		return
	}
	defer conn.Close()

	// F#1 (round-3) — Hubble flow streaming targets the LOCAL cluster's CNI
	// data plane (Hubble gRPC connection is wired to the in-cluster relay).
	// Serving a remote cluster here would stream the local cluster's flows
	// under the remote cluster's name, a classic confused-deputy. Reject
	// BEFORE the connection counter, the filter read, the RBAC check, or any
	// audit-success entry. The remote relay path replaces this in U11.
	flowsClusterID := middleware.ClusterIDFromContext(reqCtx)
	if !k8s.IsLocalClusterID(flowsClusterID) {
		s.Logger.Warn("ws flow stream rejected on remote cluster",
			"user", user.Username,
			"clusterID", flowsClusterID,
		)
		if s.AuditLogger != nil {
			entry := s.newAuditEntry(r, user.Username, flowsWSRoute.auditAction, audit.ResultFailure)
			entry.ClusterID = s.auditClusterID(flowsClusterID)
			entry.ResourceKind = flowsWSRoute.auditKind
			entry.Detail = "ws flow stream rejected: remote clusters not supported"
			s.AuditLogger.Log(reqCtx, entry)
		}
		conn.WriteJSON(map[string]any{
			"type":    "error",
			"message": "WebSocket flow streaming not yet supported on remote clusters",
			"reason":  string(k8s.ReasonUnsupportedPlatform),
		})
		return
	}

	// The route is always mounted, so a local cluster without Hubble is
	// answered in-band rather than by a missing route.
	var hc *networking.HubbleClient
	if s.NetworkingHandler != nil {
		hc = s.NetworkingHandler.HubbleClient
	}
	if hc == nil {
		conn.WriteJSON(map[string]any{
			"type":    "error",
			"message": "Hubble is not available",
			"reason":  wsReasonHubbleUnavailable,
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
		reqCtx, flowsClusterID, user.KubernetesUsername, user.KubernetesGroups,
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
	ctx, cancel := context.WithCancel(reqCtx)
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
