package server

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// logSubRequest is the filter message sent by the client after auth.
type logSubRequest struct {
	Container  string `json:"container"`
	TailLines  int64  `json:"tailLines"`
	Previous   bool   `json:"previous"`
	Timestamps bool   `json:"timestamps"`
}

const (
	maxLogConnections = 100        // concurrent log stream WS connections
	logMaxLineBuffer  = 256 * 1024 // 256KB max line — lines exceeding this are truncated by bufio.Scanner
	logChannelSize    = 256        // bounded channel for backpressure
	logDropBatchEvery = 5 * time.Second
)

// logWSCount tracks active log stream WebSocket connections for DoS protection.
var logWSCount atomic.Int64

// logsWSRoute is the log stream's cluster contract: it must name its cluster,
// by header or in-band, and may name a remote one either way (KTD5). Without
// requireCluster a browser, which cannot send X-Cluster-ID, would be served
// the local cluster's logs under a remote selection.
var logsWSRoute = wsRoute{
	acceptsRemoteInBand: true,
	requireCluster:      true,
	auditAction:         audit.ActionReadLogs,
	auditKind:           "Pod",
}

// logRevalidateInterval is how often a live remote log stream rechecks its
// cluster and the user's access, matching flowRevalidateInterval.
var logRevalidateInterval = 5 * time.Minute

// logStreamEnds are the lifecycle ends of a live log stream
// (stream_lifecycle.go).
var logStreamEnds = newStreamEnds("the log stream", "these logs")

// errLogReaderPanicked is the reader result when the log reader panicked;
// the panic itself is recovered and logged.
var errLogReaderPanicked = errors.New("log reader panicked")

// handleWSLogs handles WebSocket connections for real-time pod log streaming.
// Protocol: client sends auth message (JWT), then filter message, then receives log lines.
// Uses direct per-client k8s log stream → WS pipe (Pattern B, same as flows).
//
// The local cluster streams through the local client factory. A remote
// cluster streams through ClusterRouter as the user, and the stream ends
// with a stated reason at token expiry, when revalidation finds the user's
// access revoked or the cluster gone, and at once when this replica removes
// the cluster (stream_lifecycle.go).
func (s *Server) handleWSLogs(w http.ResponseWriter, r *http.Request) {
	if s.ResourceHandler == nil {
		http.Error(w, "resource handler not available", http.StatusServiceUnavailable)
		return
	}

	// Connection limit
	if logWSCount.Load() >= maxLogConnections {
		http.Error(w, "too many log stream connections", http.StatusServiceUnavailable)
		return
	}

	// The helper refuses a stream naming no cluster, and a non-admin naming
	// a remote one, before any client is built.
	conn, user, reqCtx := s.wsAuthAndUpgradeFor(w, r, logsWSRoute)
	if conn == nil {
		return
	}
	defer conn.Close()

	ns := chi.URLParam(r, "namespace")
	pod := chi.URLParam(r, "pod")
	container := chi.URLParam(r, "container")

	// Validate URL params
	if !resources.ValidateK8sName(ns) || !resources.ValidateK8sName(pod) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid namespace or pod name"})
		return
	}
	if container != "" && !resources.ValidateK8sName(container) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid container name"})
		return
	}

	// Read filter/options message
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var filter logSubRequest
	if err := conn.ReadJSON(&filter); err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "filter message required"})
		return
	}

	// Override container from URL if not set in filter
	if filter.Container == "" {
		filter.Container = container
	}
	if filter.Container != "" && !resources.ValidateK8sName(filter.Container) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid container name"})
		return
	}

	// Defaults
	if filter.TailLines <= 0 {
		filter.TailLines = 500
	}
	if filter.TailLines > 10000 {
		filter.TailLines = 10000
	}

	clusterID := middleware.ClusterIDFromContext(reqCtx)
	remote := !k8s.IsLocalClusterID(clusterID)

	logWSCount.Add(1)
	defer logWSCount.Add(-1)

	// RBAC check — get on pods/log subresource, asked of the target cluster.
	allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
		reqCtx, clusterID, user.KubernetesUsername, user.KubernetesGroups,
		"get", "pods/log", ns,
	)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "permission check failed"})
		return
	}
	if !allowed {
		conn.WriteJSON(map[string]any{"type": "error", "message": "no permission to view logs in this namespace"})
		return
	}

	// The stream's context ends when the WebSocket closes, or with a
	// *streamEnd cause when a remote stream must end.
	ctx, cancel := context.WithCancelCause(reqCtx)
	defer cancel(nil)

	// Create impersonating client for the log stream
	var cs kubernetes.Interface
	if remote {
		// Registered before the client is built, so a cluster removed while
		// the stream is still opening ends it too.
		remove := s.logStreams.add(clusterID, cancel)
		defer remove()
		cs, err = s.remoteLogClient(ctx, clusterID, user)
		if ctx.Err() != nil {
			writeStreamEnd(conn, ctx)
			return
		}
		if err != nil {
			s.Logger.Warn("remote log stream refused", "user", user.Username, "cluster", clusterID, "error", err)
			s.auditLogStream(r, reqCtx, user, clusterID, ns, pod, audit.ResultFailure, "remote log stream refused: cluster unreachable")
			conn.WriteJSON(map[string]any{
				"type":    "error",
				"message": "failed to reach the selected cluster",
				"reason":  string(k8s.ReasonUnreachable),
			})
			return
		}
	} else {
		// nolint:cluster-routing carve-out: the local log stream reads through the local factory; a remote one routes through ClusterRouter above.
		cs, err = s.ResourceHandler.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
		if err != nil {
			conn.WriteJSON(map[string]any{"type": "error", "message": "failed to create client"})
			return
		}
	}

	// Confirm subscription
	conn.WriteJSON(map[string]any{
		"type":      "subscribed",
		"pod":       pod,
		"container": filter.Container,
		"namespace": ns,
	})

	s.auditLogStream(r, reqCtx, user, clusterID, ns, pod, audit.ResultSuccess, "")

	s.Logger.Info("log stream started",
		"user", user.Username,
		"cluster", clusterID,
		"namespace", ns,
		"pod", pod,
		"container", filter.Container,
	)

	// Build log options
	opts := &corev1.PodLogOptions{
		Container:  filter.Container,
		Follow:     true,
		TailLines:  &filter.TailLines,
		Previous:   filter.Previous,
		Timestamps: filter.Timestamps,
	}

	// Open the log stream
	stream, err := cs.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		if ctx.Err() != nil {
			writeStreamEnd(conn, ctx)
			return
		}
		conn.WriteJSON(map[string]any{"type": "error", "message": "failed to open log stream"})
		s.Logger.Warn("log stream open failed", "error", err, "cluster", clusterID, "pod", pod, "namespace", ns)
		return
	}
	defer stream.Close()

	// Ping/pong keepalive + read pump
	ticker := wsStartKeepalive(conn, func() { cancel(nil) })
	defer ticker.Stop()

	if remote {
		s.watchStream(ctx, cancel, logStreamEnds, "ws log revalidate", logRevalidateInterval, func(ctx context.Context) *streamEnd {
			return s.revalidateLogStream(ctx, clusterID, user, ns)
		})
	}

	// Log reader goroutine: scan lines from k8s stream into bounded channel.
	// readErr is written before logCh closes, so the write loop may read it
	// once it sees the channel closed.
	logCh := make(chan string, logChannelSize)
	var droppedCount atomic.Int64
	var readErr error

	go func() {
		defer close(logCh)
		readErr = errLogReaderPanicked
		recoverutil.Safe(s.Logger, "ws log reader", func() {
			scanner := bufio.NewScanner(stream)
			scanner.Buffer(make([]byte, 0, 64*1024), logMaxLineBuffer)
			for scanner.Scan() {
				select {
				case logCh <- scanner.Text():
				default:
					// Channel full — drop line (client is slow)
					droppedCount.Add(1)
				}
			}
			readErr = scanner.Err()
		})
	}()

	// Drop notification ticker (batch every 5s)
	dropTicker := time.NewTicker(logDropBatchEvery)
	defer dropTicker.Stop()

	// Write loop: send log lines, pings, and batched drop notifications
	for {
		select {
		case line, ok := <-logCh:
			if !ok {
				switch {
				case ctx.Err() != nil:
					writeStreamEnd(conn, ctx)
				case readErr != nil:
					s.Logger.Warn("log stream error", "error", readErr,
						"cluster", clusterID, "namespace", ns, "pod", pod)
					conn.WriteJSON(map[string]any{
						"type":    "error",
						"message": "log stream interrupted",
					})
				default:
					// Stream ended (container stopped, EOF)
					conn.WriteJSON(map[string]any{"type": "end"})
				}
				return
			}
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteJSON(map[string]any{
				"type": "log",
				"data": line,
			}); err != nil {
				s.Logger.Debug("log ws write failed", "error", err)
				return
			}

		case <-dropTicker.C:
			dropped := droppedCount.Swap(0)
			if dropped > 0 {
				conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
				conn.WriteJSON(map[string]any{
					"type":  "dropped",
					"count": dropped,
				})
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-ctx.Done():
			writeStreamEnd(conn, ctx)
			return
		}
	}
}

// remoteLogClient resolves the impersonating clientset a remote log stream
// reads through. ClusterRouter has no local fallback on failure.
func (s *Server) remoteLogClient(ctx context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error) {
	if s.logClient != nil {
		return s.logClient(ctx, clusterID, user)
	}
	if s.ClusterRouter == nil {
		return nil, errors.New("no cluster router wired")
	}
	return s.ClusterRouter.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
}

// revalidateLogStream rechecks a live remote log stream: the cluster is
// still registered and the user can still get pods/log in the namespace. It
// returns the end to apply, or nil to continue. A check that cannot be
// answered leaves the stream running; the token's expiry still bounds it.
func (s *Server) revalidateLogStream(ctx context.Context, clusterID string, user *auth.User, namespace string) *streamEnd {
	if records := s.metricsClusterGetter(); records != nil {
		if _, err := records.Get(ctx, clusterID); errors.Is(err, pgx.ErrNoRows) {
			return logStreamEnds.clusterRemoved
		}
	}
	var access networking.NamespaceAccess = s.ResourceHandler.AccessChecker
	if s.logAccess != nil {
		access = s.logAccess
	}
	allowed, err := access.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "get", "pods/log", namespace)
	if err != nil {
		s.Logger.Warn("log stream revalidation failed", "cluster", clusterID, "user", user.Username, "error", err)
		return nil
	}
	if !allowed {
		return logStreamEnds.accessRevoked
	}
	return nil
}

// auditLogStream writes the audit row of a log stream open: the user, the
// cluster and the pod.
func (s *Server) auditLogStream(r *http.Request, ctx context.Context, user *auth.User, clusterID, namespace, pod string, result audit.Result, detail string) {
	if s.AuditLogger == nil {
		return
	}
	entry := s.newAuditEntry(r, user.Username, logsWSRoute.auditAction, result)
	entry.ClusterID = s.auditClusterID(clusterID)
	entry.ResourceKind = logsWSRoute.auditKind
	entry.ResourceNamespace = namespace
	entry.ResourceName = pod
	entry.Detail = detail
	s.AuditLogger.Log(ctx, entry)
}
