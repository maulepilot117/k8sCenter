package server

// The Loki live tail (remote logs and shells U12). A tail reads the Loki of
// the cluster it names, resolved after in-band auth: the local discovered
// Loki, a remote cluster's configured Loki URL, or a remote cluster's
// in-cluster Loki reached through one port-forward tunnel held for the
// tail's life (loki/resolver.go). A remote tail is never served from the
// local Loki. It registers with the shared stream lifecycle, so it ends at
// token expiry, on cluster removal, and, on the port-forward path, when
// revalidation finds the user's create pods/portforward grant in Loki's
// namespace revoked.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/loki"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
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

// logsSearchWSRoute is the Loki tail's cluster contract: it must name its
// cluster, and may name a remote one in-band.
var logsSearchWSRoute = wsRoute{
	acceptsRemoteInBand: true,
	requireCluster:      true,
	auditAction:         audit.ActionReadLogs,
	auditKind:           "LokiTail",
}

// lokiTailRevalidateInterval is how often a live remote Loki tail rechecks
// its cluster and, on the port-forward path, the user's grant to the tunnel.
var lokiTailRevalidateInterval = 5 * time.Minute

// lokiTailIdleTimeout ends a tail whose Loki connection shows no sign of
// life: no line, ping or pong. Loki pings its tail clients, and the tail
// pings Loki on every keepalive tick, so only a connection that went silent
// reaches it.
var lokiTailIdleTimeout = 2 * time.Minute

// endLokiTailLost ends a remote tail whose connection to Loki ended: the
// tunnel or the connection broke, or Loki went silent. The viewer retries.
var endLokiTailLost = &streamEnd{reason: loki.ReasonLokiUnreachable, message: "the connection to Loki on the selected cluster ended; reconnecting", retryable: true}

// errLokiTailPumpPanicked is the pump result when the Loki reader panicked;
// the panic itself is recovered and logged.
var errLokiTailPumpPanicked = errors.New("loki tail pump panicked")

// handleWSLogsSearch handles WebSocket connections for real-time Loki log tailing.
// Protocol: client sends auth message (JWT), then subscribe message with LogQL query,
// then receives log stream messages from Loki.
func (s *Server) handleWSLogsSearch(w http.ResponseWriter, r *http.Request) {
	if logSearchWSCount.Load() >= maxLogSearchConnections {
		http.Error(w, "too many log search connections", http.StatusServiceUnavailable)
		return
	}

	// The helper refuses a tail naming no cluster, and a non-admin naming a
	// remote one, before anything is resolved.
	conn, user, reqCtx := s.wsAuthAndUpgradeFor(w, r, logsSearchWSRoute)
	if conn == nil {
		return
	}
	defer conn.Close()

	clusterID := middleware.ClusterIDFromContext(reqCtx)
	remote := !k8s.IsLocalClusterID(clusterID)
	if s.LokiHandler == nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "log search is not available", "reason": loki.ReasonLokiNotFound})
		return
	}

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

	// Enforce namespace scoping on the LogQL query. A non-admin can only be
	// here for the local cluster: the helper refused a remote one.
	var allowedNamespaces []string
	if !auth.IsAdmin(user) {
		if sub.Namespace == "" {
			conn.WriteJSON(map[string]any{"type": "error", "message": "namespace required for non-admin users"})
			return
		}

		// P1-4 fix: validate namespace against Kubernetes RBAC (same pattern as handleWSFlows).
		if s.ResourceHandler != nil && s.ResourceHandler.AccessChecker != nil {
			allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
				reqCtx, clusterID, user.KubernetesUsername, user.KubernetesGroups,
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

	// The stream's context ends when the WebSocket closes, or with a
	// *streamEnd cause when a remote tail must end.
	ctx, cancel := context.WithCancelCause(reqCtx)
	defer cancel(nil)

	// The keepalive, the registration and the lifecycle watch all start
	// before Loki is resolved, so a resolve that stalls while opening a
	// tunnel still ends when the client leaves, the cluster is removed or
	// the token expires: no tunnel is opened as the user after expiry. The
	// grant check needs the resolved Loki, so until resolution finishes the
	// watch checks only that the cluster is still registered.
	ticker := wsStartKeepalive(conn, func() { cancel(nil) })
	defer ticker.Stop()
	var target atomic.Pointer[lokiTailTarget]
	if remote {
		remove := s.logStreams.add(clusterID, cancel)
		defer remove()
		s.watchStream(ctx, cancel, logStreamEnds, "ws loki tail revalidate", lokiTailRevalidateInterval, func(ctx context.Context) *streamEnd {
			return s.revalidateLokiTail(ctx, clusterID, user, target.Load())
		})
	}

	res, err := s.LokiHandler.ResolveFor(ctx, clusterID, user)
	if ctx.Err() != nil {
		res.Close() // nil-safe; a resolve that won the race still holds a tunnel
		writeStreamEnd(conn, ctx)
		return
	}
	if err != nil {
		s.refuseLokiTail(conn, r, reqCtx, user, clusterID, err)
		return
	}
	defer res.Close()
	target.Store(&lokiTailTarget{source: res.Source, namespace: res.Namespace})

	// Confirm subscription
	conn.WriteJSON(map[string]any{
		"type":  "subscribed",
		"query": enforcedQuery,
	})

	s.Logger.Info("log search stream started",
		"user", user.Username,
		"cluster", clusterID,
		"query", enforcedQuery,
	)

	// Connect to Loki's tail through the resolved client: the local Loki,
	// the strict dialer for a configured URL, or the held tunnel.
	limit := sub.Limit
	if limit <= 0 {
		limit = 30
	}
	lokiConn, err := res.Client.DialTail(ctx, enforcedQuery, sub.StartNano, limit)
	if err != nil {
		if ctx.Err() != nil {
			writeStreamEnd(conn, ctx)
			return
		}
		s.failLokiTailDial(conn, r, reqCtx, user, clusterID, res, err)
		return
	}
	defer lokiConn.Close()
	if remote {
		s.auditLokiTail(r, reqCtx, user, clusterID, audit.ResultSuccess, "remote loki tail opened via "+string(res.Source))
	}

	// Loki's connection stays alive while anything arrives on it.
	alive := func() { _ = lokiConn.SetReadDeadline(time.Now().Add(lokiTailIdleTimeout)) }
	alive()
	lokiConn.SetPongHandler(func(string) error { alive(); return nil })
	lokiConn.SetPingHandler(func(data string) error {
		alive()
		err := lokiConn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(wsWriteWait))
		if errors.Is(err, websocket.ErrCloseSent) {
			return nil
		}
		return err
	})

	// Bridge: Loki tail → client WebSocket
	lokiCh := make(chan json.RawMessage, 64)
	lokiErr := make(chan error, 1)

	go func() {
		err := errLokiTailPumpPanicked
		recoverutil.Safe(s.Logger, "ws loki tail pump", func() {
			for {
				_, message, readErr := lokiConn.ReadMessage()
				if readErr != nil {
					err = readErr
					return
				}
				alive()
				select {
				case lokiCh <- json.RawMessage(message):
				default:
					// Drop if channel full — client is slow
				}
			}
		})
		lokiErr <- err
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
			_ = lokiConn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait))

		case err := <-lokiErr:
			if ctx.Err() != nil {
				writeStreamEnd(conn, ctx)
				return
			}
			s.Logger.Warn("loki tail stream ended", "cluster", clusterID, "error", err)
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if remote {
				conn.WriteJSON(endLokiTailLost.frame())
			} else {
				conn.WriteJSON(map[string]any{"type": "error", "message": "log stream disconnected"})
			}
			return

		case <-res.Done():
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			conn.WriteJSON(endLokiTailLost.frame())
			return

		case <-ctx.Done():
			writeStreamEnd(conn, ctx)
			return
		}
	}
}

// refuseLokiTail answers a tail whose Loki could not be resolved, with the
// reason's fixed message, and audits a remote refusal.
func (s *Server) refuseLokiTail(conn *websocket.Conn, r *http.Request, ctx context.Context, user *auth.User, clusterID string, err error) {
	frame := map[string]any{"type": "error", "message": "log search is not available on the selected cluster", "reason": loki.ReasonLokiUnreachable}
	reason := loki.ReasonLokiUnreachable
	var ue *loki.UnavailableError
	if errors.As(err, &ue) {
		reason = ue.Reason
		frame["message"], frame["reason"] = ue.Message, ue.Reason
		if ue.Retryable {
			frame["retryable"] = true
		}
	}
	s.Logger.Warn("loki tail refused", "user", user.Username, "cluster", clusterID, "reason", reason, "error", err)
	if !k8s.IsLocalClusterID(clusterID) {
		s.auditLokiTail(r, ctx, user, clusterID, audit.ResultFailure, "remote loki tail refused: "+reason)
	}
	conn.WriteJSON(frame)
}

// failLokiTailDial answers a tail whose connection to Loki's tail endpoint
// failed: with the unavailable reason when there is one, else a fixed
// message.
func (s *Server) failLokiTailDial(conn *websocket.Conn, r *http.Request, ctx context.Context, user *auth.User, clusterID string, res *loki.Resolved, err error) {
	frame := map[string]any{"type": "error", "message": "failed to connect to log stream"}
	detail := "remote loki tail could not connect"
	if ue := res.Failure(err); ue != nil {
		frame["message"], frame["reason"] = ue.Message, ue.Reason
		if ue.Retryable {
			frame["retryable"] = true
		}
		detail += ": " + ue.Reason
	}
	s.Logger.Warn("loki tail connect failed", "user", user.Username, "cluster", clusterID, "error", err)
	if !k8s.IsLocalClusterID(clusterID) {
		s.auditLokiTail(r, ctx, user, clusterID, audit.ResultFailure, detail)
	}
	conn.WriteJSON(frame)
}

// lokiTailTarget is the Loki a remote tail resolved to, which its
// revalidation needs once resolution has finished.
type lokiTailTarget struct {
	source    loki.Source
	namespace string
}

// revalidateLokiTail rechecks a live remote tail: the cluster is still
// registered and, on the port-forward path, the user still holds create
// pods/portforward in Loki's namespace, the grant that opened the tunnel. A
// configured URL needs no Kubernetes grant, and target is nil while Loki is
// still being resolved. A check that cannot be answered leaves the tail
// running; the token's expiry still bounds it.
func (s *Server) revalidateLokiTail(ctx context.Context, clusterID string, user *auth.User, target *lokiTailTarget) *streamEnd {
	if records := s.metricsClusterGetter(); records != nil {
		if _, err := records.Get(ctx, clusterID); errors.Is(err, pgx.ErrNoRows) {
			return logStreamEnds.clusterRemoved
		}
	}
	if target == nil || target.source != loki.SourcePortForward {
		return nil
	}
	access := s.logStreamAccess()
	if access == nil {
		return nil
	}
	allowed, err := access.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "create", "pods/portforward", target.namespace)
	if err != nil {
		s.Logger.Warn("loki tail revalidation failed", "cluster", clusterID, "user", user.Username, "error", err)
		return nil
	}
	if !allowed {
		return logStreamEnds.accessRevoked
	}
	return nil
}

// auditLokiTail writes the audit row of a remote Loki tail open.
func (s *Server) auditLokiTail(r *http.Request, ctx context.Context, user *auth.User, clusterID string, result audit.Result, detail string) {
	if s.AuditLogger == nil {
		return
	}
	entry := s.newAuditEntry(r, user.Username, logsSearchWSRoute.auditAction, result)
	entry.ClusterID = s.auditClusterID(clusterID)
	entry.ResourceKind = logsSearchWSRoute.auditKind
	entry.Detail = detail
	s.AuditLogger.Log(ctx, entry)
}
