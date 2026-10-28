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
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"
)

var validContainerName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// execWSCount tracks the number of concurrent exec WebSocket connections.
var execWSCount atomic.Int64

const maxExecConnections = 50

// shellCandidates is the ordered list of shells to try in a container.
var shellCandidates = []string{"/bin/bash", "/bin/sh", "/bin/ash"}

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
	limitBytes := int64(5 * 1024 * 1024)         // 5 MB max response

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

// ExecAtCapacity reports whether the concurrent exec connection limit is
// reached. The exec handler checks it before upgrading.
func ExecAtCapacity() bool {
	return execWSCount.Load() >= maxExecConnections
}

// ValidContainerName reports whether a container name from a URL is safe to
// pass to the exec subresource.
func ValidContainerName(name string) bool {
	return validContainerName.MatchString(name)
}

// ExecExecutorFunc builds the executor that runs one shell candidate in a
// container.
type ExecExecutorFunc func(ns, name, container, shell string) (remotecommand.Executor, error)

// RunExecSession runs a shell over an upgraded connection until either side
// ends it. The caller has authenticated user, checked create pods/exec,
// validated the names and refused a remote cluster; r's context carries the
// effective cluster for the client and the audit row. The caller closes conn.
func (h *Handler) RunExecSession(r *http.Request, conn *websocket.Conn, user *auth.User, ns, name, container string) {
	// Limit incoming message size (16KB — sufficient for input + resize JSON).
	// The connection arrives with the auth frame's smaller limit and read
	// deadline, so both are reset here.
	conn.SetReadLimit(16384)
	conn.SetReadDeadline(time.Time{})

	// Reserve a connection slot. The caller's ExecAtCapacity check runs
	// before the upgrade; the handler then waits on the client's auth
	// frame, so many connections can pass that check before any of them
	// holds a slot.
	if execWSCount.Add(1) > maxExecConnections {
		execWSCount.Add(-1)
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"too many exec connections"}`))
		return
	}
	defer execWSCount.Add(-1)

	newExecutor := h.ExecExecutor
	if newExecutor == nil {
		var err error
		newExecutor, err = h.spdyExecExecutor(r, user)
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"failed to create client"}`))
			return
		}
	}

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
		executor, execErr := newExecutor(ns, name, container, shell)
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

			// Notify client of successful shell. The shell may already be
			// writing its prompt, so this goes through the stream's lock.
			ws.writeJSON(map[string]string{"type": "shell", "name": shell})

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
		ws.writeJSON(map[string]string{"type": "error", "message": "no shell found in container"})
		h.Logger.Warn("no shell found in container", "pod", name, "namespace", ns, "container", container)
	}
}

// spdyExecExecutor returns an ExecExecutorFunc that builds SPDY executors
// impersonating user against the local API server.
func (h *Handler) spdyExecExecutor(r *http.Request, user *auth.User) (ExecExecutorFunc, error) {
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		return nil, err
	}

	// nolint:cluster-routing local path: pod exec is local-only; the exec handler refuses a remote cluster before the session starts
	cfg := h.K8sClient.BaseConfig()
	cfg.Impersonate.UserName = user.KubernetesUsername
	cfg.Impersonate.Groups = user.KubernetesGroups

	return func(ns, name, container, shell string) (remotecommand.Executor, error) {
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

		// nolint:cluster-routing local path: pod exec is local-only; the exec handler refuses a remote cluster before the session starts
		return remotecommand.NewSPDYExecutor(cfg, "POST", execReq.URL())
	}, nil
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
	// writeMu serializes writes: gorilla allows one concurrent writer, and
	// the stdout and stderr copies run alongside the session's own frames.
	writeMu sync.Mutex
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// writeJSON sends a control frame to the client, serialized with output.
func (s *wsStream) writeJSON(v any) {
	msg, _ := json.Marshal(v)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.conn.WriteMessage(websocket.TextMessage, msg)
}
