package server

import (
	"bufio"
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	corev1 "k8s.io/api/core/v1"
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

// handleWSLogs handles WebSocket connections for real-time pod log streaming.
// Protocol: client sends auth message (JWT), then filter message, then receives log lines.
// Uses direct per-client k8s log stream → WS pipe (Pattern B, same as flows).
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

	conn, user := s.wsAuthAndUpgrade(w, r)
	if conn == nil {
		return
	}
	defer conn.Close()

	logWSCount.Add(1)
	defer logWSCount.Add(-1)

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

	// RBAC check — get on pods/log subresource
	allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
		r.Context(), user.KubernetesUsername, user.KubernetesGroups,
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

	// Confirm subscription
	conn.WriteJSON(map[string]any{
		"type":      "subscribed",
		"pod":       pod,
		"container": filter.Container,
		"namespace": ns,
	})

	// Audit
	entry := s.newAuditEntry(r, user.Username, audit.ActionReadLogs, audit.ResultSuccess)
	entry.ResourceKind = "Pod"
	entry.ResourceNamespace = ns
	entry.ResourceName = pod
	s.AuditLogger.Log(r.Context(), entry)

	s.Logger.Info("log stream started",
		"user", user.Username,
		"namespace", ns,
		"pod", pod,
		"container", filter.Container,
	)

	// Create impersonating client for the log stream
	rh := s.ResourceHandler
	cs, err := rh.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "message": "failed to create client"})
		return
	}

	// Set up context that cancels when WS closes
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

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
		conn.WriteJSON(map[string]any{"type": "error", "message": "failed to open log stream"})
		s.Logger.Warn("log stream open failed", "error", err, "pod", pod, "namespace", ns)
		return
	}
	defer stream.Close()

	// Ping/pong keepalive + read pump
	ticker := wsStartKeepalive(conn, cancel)
	defer ticker.Stop()

	// Log reader goroutine: scan lines from k8s stream into bounded channel
	logCh := make(chan string, logChannelSize)
	var droppedCount atomic.Int64
	streamErr := make(chan error, 1)

	go func() {
		defer close(logCh)
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
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			streamErr <- err
		}
	}()

	// Drop notification ticker (batch every 5s)
	dropTicker := time.NewTicker(logDropBatchEvery)
	defer dropTicker.Stop()

	// Write loop: send log lines, pings, and batched drop notifications
	for {
		select {
		case line, ok := <-logCh:
			if !ok {
				// Stream ended (container stopped, EOF)
				conn.WriteJSON(map[string]any{"type": "end"})
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

		case err := <-streamErr:
			if err != nil {
				s.Logger.Warn("log stream error", "error", err,
					"namespace", ns, "pod", pod)
				conn.WriteJSON(map[string]any{
					"type":    "error",
					"message": "log stream interrupted",
				})
			}
			return

		case <-ctx.Done():
			return
		}
	}
}
