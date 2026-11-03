package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// logOpenTimeout bounds how long a log stream may take to open: the access
// check, the client and the log request together. It stays under
// wsPongWait, whose read deadline would otherwise end a stalled open with no
// frame. Tests shorten it.
var logOpenTimeout = 30 * time.Second

// errLogOpenTimeout is the cancel cause of an open that ran past
// logOpenTimeout. It wraps context.DeadlineExceeded, which
// k8s.ClassifyTargetErr reports as unreachable.
var errLogOpenTimeout = fmt.Errorf("log stream open timed out: %w", context.DeadlineExceeded)

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

	// The stream's context ends when the WebSocket closes, or with a
	// *streamEnd cause when a remote stream must end.
	ctx, cancel := context.WithCancelCause(reqCtx)
	defer cancel(nil)

	// The keepalive, the lifecycle watch and the open bound all start before
	// the stream opens, so an open that stalls still ends when the client
	// leaves, at token expiry, on revocation or removal, or at the bound.
	ticker := wsStartKeepalive(conn, func() { cancel(nil) })
	defer ticker.Stop()
	if remote {
		remove := s.logStreams.add(clusterID, cancel)
		defer remove()
		s.watchStream(ctx, cancel, logStreamEnds, "ws log revalidate", logRevalidateInterval, func(ctx context.Context) *streamEnd {
			return s.revalidateLogStream(ctx, clusterID, user, ns)
		})
	}
	openBound := time.AfterFunc(logOpenTimeout, func() { cancel(errLogOpenTimeout) })

	// failOpen answers an open that did not complete. A stream whose context
	// ended sends its end. A remote cluster that could not be reached is
	// refused with a reason and a failure audit row, never with a word the
	// mobile client treats as fatal (stream_lifecycle.go).
	failOpen := func(err error, message string, reachFailure bool) {
		openBound.Stop()
		if errors.Is(context.Cause(ctx), errLogOpenTimeout) {
			err, reachFailure = errLogOpenTimeout, true
		} else if ctx.Err() != nil {
			writeStreamEnd(conn, ctx)
			return
		}
		if remote && reachFailure {
			s.refuseRemoteLogs(conn, r, reqCtx, user, clusterID, ns, pod, err)
			return
		}
		s.Logger.Warn("log stream open failed", "error", err, "cluster", clusterID, "pod", pod, "namespace", ns)
		if remote {
			s.auditLogStream(r, reqCtx, user, clusterID, ns, pod, audit.ResultFailure, "remote log stream refused: "+message)
		}
		conn.WriteJSON(map[string]any{"type": "error", "message": message})
	}

	// RBAC check — get on pods/log subresource, asked of the target cluster.
	// On a remote cluster the check is the first call to reach it, so its
	// error is a reach failure, not a denial.
	allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
		ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups,
		"get", "pods/log", ns,
	)
	if err != nil || ctx.Err() != nil {
		failOpen(err, "permission check failed", true)
		return
	}
	if !allowed {
		failOpen(nil, "no permission to view logs in this namespace", false)
		return
	}

	// Create impersonating client for the log stream
	var cs kubernetes.Interface
	if remote {
		cs, err = s.remoteLogClient(ctx, clusterID, user)
	} else {
		// nolint:cluster-routing carve-out: the local log stream reads through the local factory; a remote one routes through ClusterRouter above.
		cs, err = s.ResourceHandler.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	}
	if err != nil || ctx.Err() != nil {
		failOpen(err, "failed to create client", true)
		return
	}

	opts := &corev1.PodLogOptions{
		Container:  filter.Container,
		Follow:     true,
		TailLines:  &filter.TailLines,
		Previous:   filter.Previous,
		Timestamps: filter.Timestamps,
	}

	// Open the log stream. An error the cluster answered with (no such pod
	// or container) is not a reach failure.
	stream, err := cs.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err == nil && !openBound.Stop() {
		// The bound fired as the open returned: the stream is already
		// cancelled, so it ends as a timed-out open.
		stream.Close()
		stream, err = nil, errLogOpenTimeout
	}
	if err != nil || ctx.Err() != nil {
		if stream != nil {
			stream.Close()
		}
		var status apierrors.APIStatus
		failOpen(err, "failed to open log stream", !errors.As(err, &status))
		return
	}
	defer stream.Close()

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
	var access networking.NamespaceAccess
	switch {
	case s.logAccess != nil:
		access = s.logAccess
	case s.ResourceHandler != nil && s.ResourceHandler.AccessChecker != nil:
		access = s.ResourceHandler.AccessChecker
	default:
		return nil
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

// refuseRemoteLogs answers a remote log stream whose cluster could not be
// reached or whose client could not be built: a fixed message, the reason
// k8s.ClassifyTargetErr gives err, and a failure audit row.
func (s *Server) refuseRemoteLogs(conn *websocket.Conn, r *http.Request, ctx context.Context, user *auth.User, clusterID, namespace, pod string, err error) {
	reason := string(k8s.ClassifyTargetErr(err))
	s.Logger.Warn("remote log stream refused", "user", user.Username, "cluster", clusterID, "reason", reason, "error", err)
	s.auditLogStream(r, ctx, user, clusterID, namespace, pod, audit.ResultFailure, "remote log stream refused: "+reason)
	conn.WriteJSON(map[string]any{
		"type":    "error",
		"message": "failed to reach the selected cluster",
		"reason":  reason,
	})
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
