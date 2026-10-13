package server

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
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

// errFlowPumpPanicked is the pump result when the flow pump panicked; the
// panic itself is recovered and logged.
var errFlowPumpPanicked = errors.New("flow pump panicked")

// handleWSFlows handles WebSocket connections for real-time Hubble flow streaming.
// Uses a direct per-client gRPC→WS pipe instead of the Hub, because flow volume
// (100s/sec) would starve the Hub's 1024-event channel used for resource events.
// Protocol: client sends auth message (JWT), then filter message, then receives flows.
//
// The local cluster streams from the local Hubble client. A remote cluster
// streams from its own relay through a port-forward tunnel (flow_streams.go)
// and never touches the local client, so it works whether or not the local
// cluster runs Hubble (R12).
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

	flowsClusterID := middleware.ClusterIDFromContext(reqCtx)
	remote := !k8s.IsLocalClusterID(flowsClusterID)

	// The route is always mounted, so a missing flow source is answered
	// in-band rather than by a missing route.
	var hc *networking.HubbleClient
	var openRemote flowStreamOpener
	if remote {
		if openRemote = s.remoteFlowOpener(); openRemote == nil {
			conn.WriteJSON(map[string]any{
				"type":    "error",
				"message": "remote Hubble flows are not available",
				"reason":  string(k8s.ReasonUnsupportedPlatform),
			})
			return
		}
	} else {
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

	// The stream's context ends when the WebSocket closes, or with a
	// *flowStreamEnd cause when a remote stream must end.
	ctx, cancel := context.WithCancelCause(reqCtx)
	defer cancel(nil)

	// stream follows flows from the source chosen below.
	var stream func(ctx context.Context, namespace, verdict string, cb func(networking.FlowRecord)) error
	var src flowStream
	if remote {
		// Registered before the source opens, so a cluster removed while
		// the relay tunnel is still being set up ends this stream too.
		remove := s.flowStreams.add(flowsClusterID, cancel)
		defer remove()
		// The opener checks the user may list pods in the namespace on
		// that cluster before any tunnel opens (R10).
		var err error
		src, err = openRemote(ctx, flowsClusterID, user, filter.Namespace)
		if err == nil && ctx.Err() != nil {
			src.Close()
		}
		if ctx.Err() != nil {
			s.endFlowStream(conn, ctx)
			return
		}
		if err != nil {
			s.refuseRemoteFlows(conn, r, reqCtx, user, flowsClusterID, filter.Namespace, err)
			return
		}
		defer src.Close()
		s.auditRemoteFlowStream(r, reqCtx, user, flowsClusterID, filter.Namespace, audit.ResultSuccess, "remote flow stream opened")
		stream = src.StreamFlows
	} else {
		// RBAC check — flow visibility = pod observability (SelfSubjectAccessReview, cached 60s).
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
		stream = hc.StreamFlows
	}

	// Confirm subscription
	conn.WriteJSON(map[string]any{
		"type":      "subscribed",
		"namespace": filter.Namespace,
		"verdict":   filter.Verdict,
	})

	s.Logger.Info("flow stream started",
		"user", user.Username,
		"cluster", flowsClusterID,
		"namespace", filter.Namespace,
		"verdict", filter.Verdict,
	)

	// Ping/pong keepalive + read pump
	ticker := wsStartKeepalive(conn, func() { cancel(nil) })
	defer ticker.Stop()

	var relayDone <-chan struct{}
	if remote {
		relayDone = src.Done()
		s.watchRemoteFlowStream(ctx, cancel, flowsClusterID, user, filter.Namespace, src.RelayNamespace())
	}

	// Stream flows from gRPC → WebSocket
	flowCh := make(chan networking.FlowRecord, 64)
	streamErr := make(chan error, 1)

	go func() {
		err := errFlowPumpPanicked
		recoverutil.Safe(s.Logger, "ws flow pump", func() {
			err = stream(ctx, filter.Namespace, filter.Verdict, func(flow networking.FlowRecord) {
				select {
				case flowCh <- flow:
				default:
					// Drop flow if channel full — client is slow, flows are ephemeral
				}
			})
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
			if ctx.Err() != nil {
				s.endFlowStream(conn, ctx)
				return
			}
			switch {
			case errors.Is(err, errFlowPumpPanicked):
				conn.WriteJSON(map[string]any{"type": "error", "message": "flow stream interrupted"})
			case remote:
				s.Logger.Warn("remote hubble flow stream ended", "cluster", flowsClusterID, "namespace", filter.Namespace, "error", err)
				s.writeRelayEnded(conn, src)
			case err != nil:
				s.Logger.Warn("hubble flow stream error", "error", err,
					"namespace", filter.Namespace)
				conn.WriteJSON(map[string]any{
					"type":    "error",
					"message": "flow stream interrupted",
				})
			}
			return

		case <-relayDone:
			s.writeRelayEnded(conn, src)
			return

		case <-ctx.Done():
			s.endFlowStream(conn, ctx)
			return
		}
	}
}

// watchRemoteFlowStream ends a live remote stream at its token's expiry and
// when revalidation finds its cluster removed or the user's access revoked
// (KTD11). Both stop when ctx ends.
func (s *Server) watchRemoteFlowStream(ctx context.Context, cancel context.CancelCauseFunc, clusterID string, user *auth.User, namespace, relayNamespace string) {
	if exp, ok := wsTokenExpiry(ctx); ok {
		expiry := time.AfterFunc(time.Until(exp), func() { cancel(endTokenExpired) })
		context.AfterFunc(ctx, func() { expiry.Stop() })
	}
	interval := flowRevalidateInterval
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var end *flowStreamEnd
			recoverutil.Tick(ctx, s.Logger, "ws flow revalidate", func(ctx context.Context) {
				end = s.revalidateFlowStream(ctx, clusterID, user, namespace, relayNamespace)
			})
			if end != nil {
				cancel(end)
				return
			}
		}
	}()
}

// endFlowStream sends the final frame of a stream whose context ended. A
// stream the client closed gets none.
func (s *Server) endFlowStream(conn *websocket.Conn, ctx context.Context) {
	var end *flowStreamEnd
	if errors.As(context.Cause(ctx), &end) {
		conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
		conn.WriteJSON(end.frame())
	}
}

// writeRelayEnded sends the final frame of a remote stream the relay ended:
// relay_restarted, which the viewer retries. A source this handler closed
// itself sends nothing.
func (s *Server) writeRelayEnded(conn *websocket.Conn, src flowStream) {
	if !src.EndedByRelay() {
		return
	}
	conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	conn.WriteJSON(endRelayRestarted.frame())
}

// refuseRemoteFlows answers a remote stream whose source could not open,
// with its reason and a fixed message, and audits the refusal.
func (s *Server) refuseRemoteFlows(conn *websocket.Conn, r *http.Request, ctx context.Context, user *auth.User, clusterID, namespace string, err error) {
	frame := map[string]any{"type": "error", "message": "the flow stream could not be opened", "reason": string(k8s.ReasonUnreachable)}
	detail := "remote flow stream refused"
	var fe *networking.FlowSourceError
	if errors.As(err, &fe) {
		frame["message"], frame["reason"] = fe.Message, fe.Reason
		if fe.Retryable {
			frame["retryable"] = true
		}
		detail += ": " + fe.Reason
	}
	s.Logger.Warn("remote flow stream refused", "user", user.Username, "cluster", clusterID, "namespace", namespace, "error", err)
	s.auditRemoteFlowStream(r, ctx, user, clusterID, namespace, audit.ResultFailure, detail)
	conn.WriteJSON(frame)
}

// auditRemoteFlowStream writes the audit row of a remote stream open: the
// user, the remote cluster and the namespace.
func (s *Server) auditRemoteFlowStream(r *http.Request, ctx context.Context, user *auth.User, clusterID, namespace string, result audit.Result, detail string) {
	if s.AuditLogger == nil {
		return
	}
	entry := s.newAuditEntry(r, user.Username, flowsWSRoute.auditAction, result)
	entry.ClusterID = s.auditClusterID(clusterID)
	entry.ResourceKind = flowsWSRoute.auditKind
	entry.ResourceNamespace = namespace
	entry.Detail = detail
	s.AuditLogger.Log(ctx, entry)
}
