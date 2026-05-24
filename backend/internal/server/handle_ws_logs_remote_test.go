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
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	k8sfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// wsLogsTestServer wires a minimal Server with a single WS log route and
// returns the test HTTP server plus a JWT factory function.
func wsLogsTestServer(t *testing.T, clusterID string) (*httptest.Server, func() string) {
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

	// Build a chi mux that injects the test cluster ID and routes to handleWSLogs.
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := middleware.WithClusterID(r.Context(), clusterID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	mux.Get("/ws/logs/{namespace}/{pod}/{container}", srv.handleWSLogs)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	issueToken := func() string {
		t.Helper()
		u := &auth.User{
			ID:                 "test-user",
			Username:           "alice",
			KubernetesUsername: "alice",
			KubernetesGroups:   []string{"dev"},
			Roles:              []string{"viewer"},
		}
		tok, err := srv.TokenManager.IssueAccessToken(u)
		if err != nil {
			t.Fatalf("IssueAccessToken: %v", err)
		}
		return tok
	}

	return ts, issueToken
}

// wsDialAndAuth dials the WS URL, sends the auth message, and returns the connection.
func wsDialAndAuth(t *testing.T, wsURL, token string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Origin": {"http://localhost"},
	})
	if err != nil {
		if resp != nil {
			t.Fatalf("WS dial failed (HTTP %d): %v", resp.StatusCode, err)
		}
		t.Fatalf("WS dial failed: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "auth", "token": token}); err != nil {
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
// sends a JSON error frame for non-local cluster IDs (Finding P2-5).
func TestHandleWSLogs_RemoteCluster_Returns501Error(t *testing.T) {
	ts, issueToken := wsLogsTestServer(t, "remote-cluster-99")
	token := issueToken()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/my-pod/app"
	conn := wsDialAndAuth(t, wsURL, token)
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
	ts, issueToken := wsLogsTestServer(t, "local")
	token := issueToken()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/my-pod/app"
	conn := wsDialAndAuth(t, wsURL, token)
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
