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
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/loki"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	k8sfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// wsFlowsTestServer serves the flows route through the real WSClusterContext
// middleware, so the X-Cluster-ID header's presence reaches the auth helper
// as it does in production. NetworkingHandler carries a non-nil HubbleClient
// pointer: every test here is refused before any HubbleClient method runs,
// so a zero value is safe.
func wsFlowsTestServer(t *testing.T) (url string, lg *captureAudit, issueToken func(admin bool) string) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

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

	lg = &captureAudit{}
	srv := testServer(t)
	srv.AuditLogger = lg
	srv.ResourceHandler = rh
	srv.NetworkingHandler = &networking.Handler{
		HubbleClient: &networking.HubbleClient{},
	}

	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws/flows", srv.handleWSFlows)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/flows", lg, func(admin bool) string {
		return issueWSToken(t, srv, admin)
	}
}

// issueWSToken signs an access token for alice, as an admin or a viewer.
func issueWSToken(t *testing.T, srv *Server, admin bool) string {
	t.Helper()
	roles := []string{"viewer"}
	if admin {
		roles = []string{"admin"}
	}
	tok, err := srv.TokenManager.IssueAccessToken(&auth.User{
		ID:                 "test-user",
		Username:           "alice",
		KubernetesUsername: "alice",
		KubernetesGroups:   []string{"dev"},
		Roles:              roles,
	})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return tok
}

// wsFirstFrame dials url with an optional X-Cluster-ID header, sends authMsg
// as the auth frame and returns the first frame the server sends back.
func wsFirstFrame(t *testing.T, url, header string, authMsg map[string]any) map[string]any {
	t.Helper()
	h := http.Header{"Origin": {"http://localhost"}}
	if header != "" {
		h.Set("X-Cluster-ID", header)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		if resp != nil {
			t.Fatalf("WS dial failed (HTTP %d): %v", resp.StatusCode, err)
		}
		t.Fatalf("WS dial failed: %v", err)
	}
	defer conn.Close()
	authMsg["type"] = "auth"
	if err := conn.WriteJSON(authMsg); err != nil {
		t.Fatalf("WriteJSON auth: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

// wsLogsSearchTestServer wires a minimal Server with a single WS logs-search
// route. Uses a real Discoverer with a nil internal client so Client() returns
// nil; that puts us at the pre-upgrade Loki-availability boundary, which is
// where the F#1 gate matters most (any remote X-Cluster-ID must be rejected
// before any Loki dial).
func wsLogsSearchTestServer(t *testing.T, clusterID string) (*httptest.Server, func() string) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	srv := testServer(t)
	disc := loki.NewDiscoverer(nil, config.LokiConfig{}, logger)
	srv.LokiHandler = &loki.Handler{
		Discoverer: disc,
		Logger:     logger,
	}

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := middleware.WithClusterID(r.Context(), clusterID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	mux.Get("/ws/logs-search", srv.handleWSLogsSearch)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	issueToken := func() string { return issueWSToken(t, srv, false) }

	return ts, issueToken
}

// TestHandleWSFlows_RemoteCluster_Rejects verifies that handleWSFlows
// refuses a remote cluster, named by header or in-band, with a reasoned frame
// and a failure audit row carrying that cluster (F#1 round-3, #608 KTD10).
// The gate fires BEFORE any Hubble stream is opened, so a remote cluster
// cannot drive the local cluster's flow data while being attributed to the
// remote cluster (classic confused-deputy). U11 replaces it with the remote
// relay.
func TestHandleWSFlows_RemoteCluster_Rejects(t *testing.T) {
	for name, tc := range map[string]struct {
		header  string
		authMsg map[string]any
	}{
		"header":  {header: "remote-cluster-99", authMsg: map[string]any{}},
		"in-band": {authMsg: map[string]any{"clusterId": "remote-cluster-99"}},
	} {
		t.Run(name, func(t *testing.T) {
			url, lg, issueToken := wsFlowsTestServer(t)
			tc.authMsg["token"] = issueToken(true)
			msg := wsFirstFrame(t, url, tc.header, tc.authMsg)

			if msg["type"] != "error" || msg["reason"] != "unsupported_platform" {
				t.Fatalf("got %v, want an unsupported_platform refusal", msg)
			}
			if m, _ := msg["message"].(string); !strings.Contains(m, "remote clusters") {
				t.Errorf("expected 'remote clusters' in error message, got: %q", m)
			}
			lg.mu.Lock()
			defer lg.mu.Unlock()
			if len(lg.entries) != 1 || lg.entries[0].ClusterID != "remote-cluster-99" ||
				lg.entries[0].Result != audit.ResultFailure {
				t.Errorf("audit entries = %+v, want one failure naming remote-cluster-99", lg.entries)
			}
		})
	}
}

// Covers AE1: a flows connection that names no cluster, in-band or by a sent
// header, is refused rather than served from the local cluster.
func TestHandleWSFlows_NoCluster_Refused(t *testing.T) {
	url, _, issueToken := wsFlowsTestServer(t)
	msg := wsFirstFrame(t, url, "", map[string]any{"token": issueToken(true)})
	if msg["type"] != "error" || msg["reason"] != "cluster_required" {
		t.Fatalf("got %v, want a cluster_required refusal", msg)
	}
}

// TestHandleWSFlows_RouteMountedWithoutHubble verifies that the production
// router mounts the flows route even when the local cluster has no Hubble,
// and that a local stream is then answered in-band with hubble_unavailable.
func TestHandleWSFlows_RouteMountedWithoutHubble(t *testing.T) {
	srv := testServer(t) // no NetworkingHandler, so no Hubble client
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws/flows"
	msg := wsFirstFrame(t, url, "", map[string]any{
		"token": issueWSToken(t, srv, false), "clusterId": "local",
	})
	if msg["type"] != "error" || msg["reason"] != "hubble_unavailable" {
		t.Fatalf("got %v, want a hubble_unavailable answer", msg)
	}
}

// TestHandleWSLogsSearch_RemoteCluster_NilLokiReturns503 confirms the
// pre-upgrade boundary: when Loki is unavailable (nil client), a remote
// X-Cluster-ID never advances past the 503. This is the outer safety
// envelope for F#1; the post-upgrade gate inside handleWSLogsSearch
// covers the case where Loki IS available.
func TestHandleWSLogsSearch_RemoteCluster_NilLokiReturns503(t *testing.T) {
	ts, _ := wsLogsSearchTestServer(t, "remote-cluster-99")

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("handler panicked on nil Loki client with remote cluster: %v", r)
		}
	}()

	resp, err := http.Get(ts.URL + "/ws/logs-search")
	if err != nil {
		t.Fatalf("GET /ws/logs-search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("nil-Loki pre-upgrade boundary returned %d, want 503", resp.StatusCode)
	}
}
