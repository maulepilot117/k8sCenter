package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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

// wsFlowsTestServer wires a minimal Server with a single WS flows route and
// returns the test HTTP server plus a JWT factory function. NetworkingHandler
// is wired with a non-nil HubbleClient pointer so the handler reaches
// wsAuthAndUpgrade — the F#1 gate fires before any HubbleClient method is
// called, so a zero-value pointer is safe.
func wsFlowsTestServer(t *testing.T, clusterID string) (*httptest.Server, func() string) {
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

	srv := testServer(t)
	srv.ResourceHandler = rh
	srv.NetworkingHandler = &networking.Handler{
		HubbleClient: &networking.HubbleClient{},
	}

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := middleware.WithClusterID(r.Context(), clusterID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	mux.Get("/ws/flows", srv.handleWSFlows)

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

// TestHandleWSFlows_RemoteCluster_Rejects verifies that handleWSFlows
// sends a JSON error frame for non-local cluster IDs (F#1 round-3). The
// gate must fire BEFORE any Hubble stream is opened so a remote
// X-Cluster-ID cannot drive the local cluster's flow data while being
// attributed to the remote cluster (classic confused-deputy).
func TestHandleWSFlows_RemoteCluster_Rejects(t *testing.T) {
	ts, issueToken := wsFlowsTestServer(t, "remote-cluster-99")
	token := issueToken()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/flows"
	conn := wsDialAndAuth(t, wsURL, token)
	defer conn.Close()

	message, found := readWSMessagesUntilError(t, conn)
	if !found {
		t.Fatal("WS connection closed without sending an error message")
	}
	if !strings.Contains(message, "remote clusters") {
		t.Errorf("expected 'remote clusters' in error message, got: %q", message)
	}
}

// (No local-cluster control test for flows: the stub HubbleClient cannot
// be safely driven past the gate without a real gRPC connection. The
// remote-rejection test above is the F#1 ask. Local-path coverage is
// indirect — the gate's source code lives BEFORE any HubbleClient
// dereference, so a local clusterID flows through to the existing
// handler logic that's already exercised by the e2e suite.)

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
