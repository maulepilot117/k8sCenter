package resources

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"
)

const kindPod = "pods"

var validContainerName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// execWSCount tracks the number of concurrent exec WebSocket connections.
var execWSCount atomic.Int64

const maxExecConnections = 50

// shellCandidates is the ordered list of shells to try in a container.
var shellCandidates = []string{"/bin/bash", "/bin/sh", "/bin/ash"}

func (h *Handler) HandleListPods(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*corev1.Pod
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindPod, params.Namespace) {
			return
		}
		all, err = h.Informers.Pods().Pods(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindPod, "") {
			return
		}
		all, err = h.Informers.Pods().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "Pod", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetPod(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindPod, ns) {
		return
	}
	obj, err := h.Informers.Pods().Pods(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Pod", ns, name)
		return
	}
	writeData(w, obj)
}

// HandlePodLogs returns the last N lines of a pod's container logs.
// GET /api/v1/resources/pods/{namespace}/{name}/logs?container=X&tailLines=500&previous=false&timestamps=true
func (h *Handler) HandlePodLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	// RBAC: check get on pods/log subresource
	if !h.checkAccess(w, r, user, "get", "pods/log", ns) {
		return
	}

	q := r.URL.Query()
	container := q.Get("container")

	// F7: Validate container name
	if container != "" && !validContainerName.MatchString(container) {
		writeError(w, http.StatusBadRequest, "invalid container name", "")
		return
	}

	tailLines := int64(500)
	if tl := q.Get("tailLines"); tl != "" {
		if v, err := strconv.ParseInt(tl, 10, 64); err == nil && v > 0 {
			tailLines = v
		}
	}
	if tailLines > 10000 {
		tailLines = 10000
	}

	previous := q.Get("previous") == "true"
	timestamps := q.Get("timestamps") != "false" // default true
	limitBytes := int64(5 * 1024 * 1024)          // 5 MB max response

	opts := &corev1.PodLogOptions{
		Container:  container,
		TailLines:  &tailLines,
		Previous:   previous,
		Timestamps: timestamps,
		LimitBytes: &limitBytes,
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", "")
		return
	}

	stream, err := cs.CoreV1().Pods(ns).GetLogs(name, opts).Stream(r.Context())
	if err != nil {
		mapK8sError(w, err, "get", "Pod logs", ns, name)
		return
	}
	defer stream.Close()

	var lines []string
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		// F6: Check context cancellation periodically (every 100 lines)
		if len(lines)%100 == 0 {
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
		lines = append(lines, scanner.Text())
	}

	truncated := false
	if err := scanner.Err(); err != nil {
		truncated = true
	}

	// F5: Audit log the log access
	h.auditWrite(r, user, audit.ActionReadLogs, "Pod", ns, name, audit.ResultSuccess)

	writeData(w, map[string]any{
		"lines":     lines,
		"container": container,
		"pod":       name,
		"namespace": ns,
		"count":     len(lines),
		"truncated": truncated,
	})
}

// HandlePodExec upgrades to WebSocket and opens an exec session to a pod container.
// WS /api/v1/ws/exec/{namespace}/{name}/{container}
func (h *Handler) HandlePodExec(w http.ResponseWriter, r *http.Request) {
	// Connection limit check
	if execWSCount.Load() >= maxExecConnections {
		writeError(w, http.StatusServiceUnavailable, "too many exec connections", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	container := chi.URLParam(r, "container")

	if !h.checkAccess(w, r, user, "create", "pods/exec", ns) {
		return
	}

	if container != "" && !validContainerName.MatchString(container) {
		writeError(w, http.StatusBadRequest, "invalid container name", "")
		return
	}

	// Validate WebSocket origin
	if h.OriginValidator != nil && !h.OriginValidator(w, r) {
		return // origin validator already wrote the HTTP error
	}

	// Upgrade to WebSocket
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true }, // origin already validated above
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.Logger.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// Limit incoming message size (16KB — sufficient for input + resize JSON)
	conn.SetReadLimit(16384)

	// Track connection count
	execWSCount.Add(1)
	defer execWSCount.Add(-1)

	// Create impersonating client
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"failed to create client"}`))
		return
	}

	cfg := h.K8sClient.BaseConfig()
	cfg.Impersonate.UserName = user.KubernetesUsername
	cfg.Impersonate.Groups = user.KubernetesGroups

	// Set up stdin pipe and terminal size queue
	sizeQueue := newTermSizeQueue()
	defer sizeQueue.Stop()

	stdinReader, stdinWriter := io.Pipe()
	defer stdinWriter.Close()

	ws := newWSStream(conn, stdinWriter, sizeQueue)

	// Note: readPump is started AFTER shell detection succeeds (below).
	// This ensures no stdin data is consumed during the detection phase,
	// preventing data loss if a shell candidate fails after reading from the pipe.

	// Shell detection: try each shell candidate
	var foundShell string
	for _, shell := range shellCandidates {
		execReq := cs.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(name).
			Namespace(ns).
			SubResource("exec").
			Param("container", container).
			Param("stdin", "true").
			Param("stdout", "true").
			Param("stderr", "true").
			Param("tty", "true").
			Param("command", shell)

		executor, execErr := remotecommand.NewSPDYExecutor(cfg, "POST", execReq.URL())
		if execErr != nil {
			h.Logger.Debug("SPDY executor creation failed", "shell", shell, "error", execErr)
			continue
		}

		// Try running the shell with a short timeout to detect failure
		shellCtx, shellCancel := context.WithCancel(r.Context())
		streamDone := make(chan error, 1)
		go func() {
			streamDone <- executor.StreamWithContext(shellCtx, remotecommand.StreamOptions{
				Stdin:             stdinReader,
				Stdout:            ws,
				Stderr:            ws,
				Tty:               true,
				TerminalSizeQueue: sizeQueue,
			})
		}()

		// Wait briefly to see if the shell fails immediately
		select {
		case streamErr := <-streamDone:
			// Shell exited immediately — try next candidate
			shellCancel()
			h.Logger.Debug("shell exited immediately", "shell", shell, "error", streamErr)
			continue
		case <-time.After(1 * time.Second):
			// Shell is running — this is the one
			foundShell = shell

			// Start the read pump now that we have a working shell.
			// This ensures no stdin data is consumed during shell detection.
			go ws.readPump()

			// Notify client of successful shell
			shellMsg, _ := json.Marshal(map[string]string{"type": "shell", "name": shell})
			conn.WriteMessage(websocket.TextMessage, shellMsg)

			h.auditWrite(r, user, audit.ActionCreate, "Pod/exec", ns, name, audit.ResultSuccess)

			// Wait for stream to finish
			streamErr := <-streamDone
			shellCancel()
			if streamErr != nil {
				h.Logger.Debug("exec session ended", "error", streamErr, "pod", name, "shell", shell)
			}
			return
		}
	}

	// All shells failed
	if foundShell == "" {
		errMsg, _ := json.Marshal(map[string]string{"type": "error", "message": "no shell found in container"})
		conn.WriteMessage(websocket.TextMessage, errMsg)
		h.Logger.Warn("no shell found in container", "pod", name, "namespace", ns, "container", container)
	}
}

// termSizeQueue implements remotecommand.TerminalSizeQueue.
type termSizeQueue struct {
	ch   chan remotecommand.TerminalSize
	done chan struct{}
}

func newTermSizeQueue() *termSizeQueue {
	return &termSizeQueue{
		ch:   make(chan remotecommand.TerminalSize, 1),
		done: make(chan struct{}),
	}
}

// Next blocks until a new terminal size is available or the queue is stopped.
func (q *termSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.ch:
		return &size
	case <-q.done:
		return nil
	}
}

// Send sends a terminal size update without blocking.
func (q *termSizeQueue) Send(width, height uint16) {
	select {
	case q.ch <- remotecommand.TerminalSize{Width: width, Height: height}:
	default:
	}
}

// Stop closes the queue, causing Next to return nil.
func (q *termSizeQueue) Stop() {
	select {
	case <-q.done:
	default:
		close(q.done)
	}
}

// wsClientMsg is the JSON message format sent by the client.
type wsClientMsg struct {
	Type string `json:"type"`
	Data string `json:"data"` // base64-encoded stdin data (for type="input")
	Cols int    `json:"cols"` // terminal columns (for type="resize")
	Rows int    `json:"rows"` // terminal rows (for type="resize")
}

// wsStream bridges a gorilla WebSocket connection to io.Reader/io.Writer
// for use with remotecommand SPDY streams.
type wsStream struct {
	conn        *websocket.Conn
	stdinWriter *io.PipeWriter
	sizeQueue   *termSizeQueue
}

func newWSStream(conn *websocket.Conn, stdinWriter *io.PipeWriter, sizeQueue *termSizeQueue) *wsStream {
	return &wsStream{
		conn:        conn,
		stdinWriter: stdinWriter,
		sizeQueue:   sizeQueue,
	}
}

// readPump reads JSON messages from the WebSocket, dispatches resize events
// to the terminal size queue, and writes decoded stdin data to the pipe.
func (s *wsStream) readPump() {
	defer s.stdinWriter.Close()
	for {
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			return
		}

		var m wsClientMsg
		if err := json.Unmarshal(msg, &m); err != nil {
			// Ignore malformed messages
			continue
		}

		switch m.Type {
		case "input":
			data, err := base64.StdEncoding.DecodeString(m.Data)
			if err != nil {
				continue
			}
			if _, err := s.stdinWriter.Write(data); err != nil {
				return
			}
		case "resize":
			if m.Cols > 0 && m.Cols <= 500 && m.Rows > 0 && m.Rows <= 500 {
				s.sizeQueue.Send(uint16(m.Cols), uint16(m.Rows))
			}
		}
	}
}

// Write sends raw binary data to the WebSocket (stdout/stderr output).
func (s *wsStream) Write(p []byte) (int, error) {
	err := s.conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (h *Handler) HandleDeletePod(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindPod, ns) {
		return
	}
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().Pods(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Pod", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Pod", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Pod", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
