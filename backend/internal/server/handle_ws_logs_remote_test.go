package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	k8sfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// wsLogsTestServer wires a minimal Server with a single WS log route behind
// the production WSClusterContext middleware, so a test names its cluster the
// way a real client does: an X-Cluster-ID header (mobile) or the auth frame
// (web). It returns the test HTTP server plus a JWT factory function.
func wsLogsTestServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Stub k8s factory + router (no real cluster needed — gate fires before API call)
	stubCS := &kubernetes.Clientset{}
	stubDyn := k8sfake.NewSimpleDynamicClient(scheme.Scheme)
	factory := k8s.NewTestClientFactoryWithDynamic(stubCS, stubDyn)
	clusterRouter := k8s.NewClusterRouter(factory, nil, "", logger)

	ac := resources.NewAlwaysAllowAccessChecker()
	rh := &resources.Handler{
		K8sClient:     factory,
		ClusterRouter: clusterRouter,
		AccessChecker: ac,
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
		TaskManager:   resources.NewTaskManager(),
	}

	// Use the package-level testServer helper (Config, TokenManager, etc.)
	// and swap in our stub ResourceHandler.
	srv := testServer(t)
	srv.ResourceHandler = rh

	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws/logs/{namespace}/{pod}/{container}", srv.handleWSLogs)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Admin, so a remote cluster passes the auth helper's admin check and
	// reaches the handler's own remote gate.
	issueToken := func() string { return issueWSToken(t, srv, true) }

	return ts, issueToken
}

// wsDialAndAuth dials the WS URL, sending header as X-Cluster-ID when it is
// non-empty, then sends an auth message naming clusterID in-band when that is
// non-empty, and returns the connection.
func wsDialAndAuth(t *testing.T, wsURL, header, clusterID, token string) *websocket.Conn {
	t.Helper()
	h := http.Header{"Origin": {"http://localhost"}}
	if header != "" {
		h.Set("X-Cluster-ID", header)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, h)
	if err != nil {
		if resp != nil {
			t.Fatalf("WS dial failed (HTTP %d): %v", resp.StatusCode, err)
		}
		t.Fatalf("WS dial failed: %v", err)
	}
	authMsg := map[string]any{"type": "auth", "token": token}
	if clusterID != "" {
		authMsg["clusterId"] = clusterID
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		conn.Close()
		t.Fatalf("WriteJSON auth: %v", err)
	}
	return conn
}

// readWSMessagesUntilError reads WS messages with a 5s deadline and returns
// the first error-type message, or ("", false) if the connection closed cleanly.
func readWSMessagesUntilError(t *testing.T, conn *websocket.Conn) (message string, found bool) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return "", false
		}
		msgType, _ := msg["type"].(string)
		if msgType == "error" {
			m, _ := msg["message"].(string)
			return m, true
		}
	}
}

// TestHandleWSLogs_RemoteCluster_Returns501Error verifies that handleWSLogs
// sends a JSON error frame for a remote cluster named by header, the way the
// mobile app names it (Finding P2-5).
func TestHandleWSLogs_RemoteCluster_Returns501Error(t *testing.T) {
	ts, issueToken := wsLogsTestServer(t)
	token := issueToken()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/my-pod/app"
	conn := wsDialAndAuth(t, wsURL, "remote-cluster-99", "", token)
	defer conn.Close()

	// Send filter message (required before cluster gate check in handleWSLogs)
	if err := conn.WriteJSON(map[string]any{
		"container":  "app",
		"tailLines":  100,
		"previous":   false,
		"timestamps": true,
	}); err != nil {
		t.Fatalf("WriteJSON filter: %v", err)
	}

	message, found := readWSMessagesUntilError(t, conn)
	if !found {
		t.Fatal("WS connection closed without sending an error message")
	}
	if !strings.Contains(message, "remote clusters") {
		t.Errorf("expected 'remote clusters' in error message, got: %q", message)
	}
}

// TestHandleWSLogs_LocalCluster_DoesNotTriggerRemoteGate verifies that
// handleWSLogs does NOT send a remote-cluster error for local cluster requests.
// The stream will fail (no real pod) but the gate itself must not fire.
func TestHandleWSLogs_LocalCluster_DoesNotTriggerRemoteGate(t *testing.T) {
	ts, issueToken := wsLogsTestServer(t)
	token := issueToken()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/my-pod/app"
	conn := wsDialAndAuth(t, wsURL, "", "local", token)
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"container":  "app",
		"tailLines":  100,
		"previous":   false,
		"timestamps": true,
	}); err != nil {
		t.Fatalf("WriteJSON filter: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			// Connection closed after stream failure — gate did not fire
			return
		}
		msgType, _ := msg["type"].(string)
		if msgType == "error" {
			message, _ := msg["message"].(string)
			if strings.Contains(message, "remote clusters") {
				t.Errorf("local cluster request triggered remote gate: %q", message)
			}
			// Other errors (failed to open log stream on stub) are expected
			return
		}
		if msgType == "subscribed" {
			// Gate passed — stream subscription was attempted
			return
		}
	}
}

// TestHandleWSLogs_RemoteInBandCluster_Refused verifies that a remote cluster
// named in the auth frame is refused on the logs route before any filter is
// read, so no log stream starts (#608 KTD10).
func TestHandleWSLogs_RemoteInBandCluster_Refused(t *testing.T) {
	ts, issueToken := wsLogsTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/my-pod/app"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"http://localhost"}})
	if err != nil {
		t.Fatalf("WS dial failed: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{
		"type": "auth", "token": issueToken(), "clusterId": "remote-cluster-99",
	}); err != nil {
		t.Fatalf("WriteJSON auth: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg["type"] != "error" || msg["reason"] != "unsupported_platform" {
		t.Fatalf("got %v, want an unsupported_platform refusal", msg)
	}
	// The server closed the connection: nothing else is ever sent.
	if err := conn.ReadJSON(&msg); err == nil {
		t.Fatalf("got a second frame %v after the refusal", msg)
	}
}

// Covers AE13: a web log stream that names no cluster, in-band or by header,
// is refused rather than served from the local cluster. Browsers cannot set
// X-Cluster-ID, so before this a remote selection streamed the local pod.
func TestHandleWSLogs_NoCluster_Refused(t *testing.T) {
	ts, issueToken := wsLogsTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/my-pod/app"
	conn := wsDialAndAuth(t, wsURL, "", "", issueToken())
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg["type"] != "error" || msg["reason"] != "cluster_required" {
		t.Fatalf("got %v, want a cluster_required refusal", msg)
	}
}

// TestHandleWSLogs_LocalHeader_Accepted verifies the mobile shape: a local
// cluster named only by the X-Cluster-ID header still reaches the stream.
func TestHandleWSLogs_LocalHeader_Accepted(t *testing.T) {
	ts, issueToken := wsLogsTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/my-pod/app"
	conn := wsDialAndAuth(t, wsURL, "local", "", issueToken())
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"container": "app", "tailLines": 10}); err != nil {
		t.Fatalf("WriteJSON filter: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg["reason"] == "cluster_required" {
		t.Fatalf("a header-named local cluster was refused: %v", msg)
	}
}

// TestLogWSRoutes_RequireCluster pins the route contract for both log
// streams: each must name its cluster and neither accepts a remote one
// in-band yet. The Loki tail cannot be dialed past its pre-upgrade Loki
// check without a live Loki, so its contract is asserted here directly.
func TestLogWSRoutes_RequireCluster(t *testing.T) {
	for name, route := range map[string]wsRoute{"logs": logsWSRoute, "logs-search": logsSearchWSRoute} {
		if !route.requireCluster {
			t.Errorf("%s route does not require a cluster", name)
		}
		if route.acceptsRemoteInBand {
			t.Errorf("%s route accepts a remote cluster in-band", name)
		}
	}
}
