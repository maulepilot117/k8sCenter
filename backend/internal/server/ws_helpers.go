package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// Shared WebSocket constants used by log and flow stream handlers.
const (
	wsWriteWait   = 10 * time.Second
	wsPongWait    = 60 * time.Second
	wsPingPeriod  = (wsPongWait * 9) / 10 // 54s
	wsMaxReadSize = 4096                  // only expect small auth/filter messages
)

// wsRoute describes how a WebSocket route takes its target cluster and how
// its refusals are audited (#608 KTD10).
type wsRoute struct {
	// acceptsRemoteInBand lets the auth message name a remote cluster. Only
	// the flows stream sets it; elsewhere a remote in-band value is refused.
	acceptsRemoteInBand bool
	// requireCluster refuses a connection that names no cluster in-band and
	// sent no X-Cluster-ID header, rather than defaulting to local.
	requireCluster bool
	// auditAction and auditKind label the failure audit row of a refusal.
	auditAction audit.Action
	auditKind   string
}

// wsAuthMessage is the first frame a WebSocket client sends.
type wsAuthMessage struct {
	Type  string `json:"type"`
	Token string `json:"token"`
	// ClusterID and Generation name the target cluster in-band: the browser
	// cannot set headers on a WebSocket, and the frontend proxy forwards
	// frames opaquely (#608 KTD10).
	ClusterID  string `json:"clusterId,omitempty"`
	Generation string `json:"generation,omitempty"`
}

// Reasons a WebSocket connection is refused before its stream starts, beyond
// the shared k8s.ReasonCode vocabulary.
const (
	wsReasonClusterRequired    = "cluster_required"
	wsReasonClusterInvalid     = "cluster_invalid"
	wsReasonPinMismatch        = "cluster_pin_mismatch"
	wsReasonGenerationMismatch = "cluster_generation_mismatch"
)

// wsAuthAndUpgradeFor upgrades the connection, authenticates the first frame
// and resolves the effective cluster, failing closed (#608 KTD10). It returns
// the connection, the user and a context derived from r's that carries the
// effective cluster id; callers use that context, never r.Context(), for the
// access checker, the router and audit. On a refusal the error frame is
// already sent, the connection is closed and conn is nil.
//
// After the token check, it runs these checks in order:
//  1. the in-band id is validated like the header (length cap; "" and
//     "local" mean local);
//  2. a route that requires a cluster refuses a connection naming none;
//  3. a remote in-band id is refused on a route that does not accept one;
//  4. a header that was sent and names another cluster is refused;
//  5. for a remote effective cluster, a non-admin is refused before any
//     cluster lookup, so the helper is no existence oracle;
//  6. a generation naming another registration is refused.
func (s *Server) wsAuthAndUpgradeFor(w http.ResponseWriter, r *http.Request, route wsRoute) (*websocket.Conn, *auth.User, context.Context) {
	if !s.validateWSOrigin(w, r) {
		return nil, nil, nil
	}

	up := upgrader
	up.CheckOrigin = func(r *http.Request) bool { return true }
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		s.Logger.Error("ws upgrade failed", "error", err)
		return nil, nil, nil
	}

	conn.SetReadLimit(wsMaxReadSize)

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var msg wsAuthMessage
	if err := conn.ReadJSON(&msg); err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "auth required"})
		conn.Close()
		return nil, nil, nil
	}
	if msg.Type != "auth" || msg.Token == "" {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid auth message"})
		conn.Close()
		return nil, nil, nil
	}
	claims, err := s.TokenManager.ValidateAccessToken(msg.Token)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid token"})
		conn.Close()
		return nil, nil, nil
	}
	user := auth.UserFromClaims(claims)

	ctx := r.Context()
	headerPresent := middleware.ClusterHeaderPresent(ctx)
	headerID := k8s.NormalizedClusterID(middleware.ClusterIDFromContext(ctx))

	// refuse ends the connection with a reasoned frame, auditing the
	// cluster the client asked for when there is one to name.
	refuse := func(requested, reason, message string) (*websocket.Conn, *auth.User, context.Context) {
		s.Logger.Warn("ws connection refused", "user", user.Username, "cluster", requested, "reason", reason)
		if requested != "" && s.AuditLogger != nil {
			entry := s.newAuditEntry(r, user.Username, route.auditAction, audit.ResultFailure)
			entry.ClusterID = s.auditClusterID(requested)
			entry.ResourceKind = route.auditKind
			entry.Detail = "ws connection refused: " + reason
			s.AuditLogger.Log(ctx, entry)
		}
		conn.WriteJSON(map[string]any{"type": "error", "message": message, "reason": reason})
		conn.Close()
		return nil, nil, nil
	}

	if len(msg.ClusterID) > middleware.MaxClusterIDLength {
		return refuse("", wsReasonClusterInvalid, "invalid cluster id")
	}
	if msg.ClusterID == "" && !headerPresent && route.requireCluster {
		return refuse("", wsReasonClusterRequired, "this stream must name its cluster; reload the page")
	}

	effective := headerID
	if msg.ClusterID != "" {
		inBand := k8s.NormalizedClusterID(msg.ClusterID)
		if !k8s.IsLocalClusterID(inBand) && !route.acceptsRemoteInBand {
			return refuse(inBand, string(k8s.ReasonUnsupportedPlatform), "this stream is not available on remote clusters")
		}
		if headerPresent && headerID != inBand {
			return refuse(inBand, wsReasonPinMismatch, "the requested cluster does not match the X-Cluster-ID header")
		}
		effective = inBand
	}

	if !k8s.IsLocalClusterID(effective) && !auth.IsAdmin(user) {
		return refuse(effective, string(k8s.ReasonForbidden), "admin role required for remote cluster access")
	}

	if msg.Generation != "" {
		if reason, message := s.checkWSGeneration(ctx, effective, msg.Generation); reason != "" {
			return refuse(effective, reason, message)
		}
	}

	return conn, user, middleware.WithClusterID(ctx, effective)
}

// checkWSGeneration compares a client's generation with the registration it
// names, returning an empty reason when they match. The local cluster's
// generation is the literal "local". A remote one is the registration's
// creation instant, compared as a time: the client's createdAt and the
// server's RFC3339Nano string format the same instant differently.
func (s *Server) checkWSGeneration(ctx context.Context, clusterID, generation string) (reason, message string) {
	const mismatchMsg = "the cluster's registration changed; reload the page"
	if k8s.IsLocalClusterID(clusterID) {
		if generation != k8s.LocalGeneration {
			return wsReasonGenerationMismatch, mismatchMsg
		}
		return "", ""
	}
	sent, err := time.Parse(time.RFC3339Nano, generation)
	if err != nil {
		return wsReasonGenerationMismatch, mismatchMsg
	}
	records := s.metricsClusterGetter()
	if records == nil {
		return string(k8s.ReasonClusterUnknown), "cluster not found"
	}
	rec, err := records.Get(ctx, clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return string(k8s.ReasonClusterUnknown), "cluster not found"
	}
	if err != nil {
		s.Logger.Warn("ws cluster lookup failed", "cluster", clusterID, "error", err)
		return string(k8s.ReasonDBUnavailable), "the cluster registry could not be read"
	}
	if !rec.CreatedAt.Equal(sent) {
		return wsReasonGenerationMismatch, mismatchMsg
	}
	return "", ""
}

// auditClusterID is an audit row's cluster id: the configured id for the
// local cluster, else the remote cluster's own id.
func (s *Server) auditClusterID(clusterID string) string {
	if k8s.IsLocalClusterID(clusterID) {
		return s.Config.ClusterID
	}
	return clusterID
}

// wsStartKeepalive sets up ping/pong handlers and starts a background read pump
// that cancels the provided context when the connection closes or errors.
// Returns a ping ticker that the caller should use in their write loop and defer Stop().
func wsStartKeepalive(conn *websocket.Conn, cancel context.CancelFunc) *time.Ticker {
	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	// Read pump: detect WS close/errors (runs in background)
	go func() {
		defer cancel()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	return time.NewTicker(wsPingPeriod)
}
