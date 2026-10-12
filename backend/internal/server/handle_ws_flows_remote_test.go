package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/loki"
	"github.com/kubecenter/kubecenter/internal/networking"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	k8sfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// wsFlowsTestServer serves the flows route through the real WSClusterContext
// middleware, so the X-Cluster-ID header's presence reaches the auth helper
// as it does in production. NetworkingHandler carries a non-nil HubbleClient
// pointer and no remote clients: every test here is refused before any
// HubbleClient method runs, so a zero value is safe.
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

// TestHandleWSFlows_RemoteWithoutRemoteFlows_Refused verifies that a remote
// cluster, named by header or in-band, is refused with a reasoned frame and
// a failure audit row carrying that cluster when remote flows are not wired.
// It is never served from the local Hubble client, which this server has
// (F#1 round-3, #608 KTD10).
func TestHandleWSFlows_RemoteWithoutRemoteFlows_Refused(t *testing.T) {
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
			h := http.Header{"Origin": {"http://localhost"}}
			if tc.header != "" {
				h.Set("X-Cluster-ID", tc.header)
			}
			conn, _, err := websocket.DefaultDialer.Dial(url, h)
			if err != nil {
				t.Fatalf("WS dial failed: %v", err)
			}
			defer conn.Close()
			tc.authMsg["type"] = "auth"
			if err := conn.WriteJSON(tc.authMsg); err != nil {
				t.Fatalf("WriteJSON auth: %v", err)
			}
			if err := conn.WriteJSON(map[string]any{"namespace": "default"}); err != nil {
				t.Fatalf("WriteJSON filter: %v", err)
			}
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				t.Fatalf("read: %v", err)
			}

			if msg["type"] != "error" || msg["reason"] != "unsupported_platform" {
				t.Fatalf("got %v, want an unsupported_platform refusal", msg)
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

// --- Live remote streams and their lifecycle (#608 slice 5, U11) ---

const flowsRemoteCluster = "remote-flows-1"

// fakeFlowSource is a remote flow source that streams web-1 flows until its
// context ends. endAfterFirst ends the stream after one flow, as a relay
// restart does; panics makes StreamFlows panic.
type fakeFlowSource struct {
	endAfterFirst bool
	panics        bool
	done          chan struct{}
	once          sync.Once
	closed        atomic.Bool
}

func newFakeFlowSource() *fakeFlowSource { return &fakeFlowSource{done: make(chan struct{})} }

func (f *fakeFlowSource) StreamFlows(ctx context.Context, _, _ string, cb func(networking.FlowRecord)) error {
	if f.panics {
		panic("relay pump bug")
	}
	for {
		cb(networking.FlowRecord{SrcNamespace: "default", SrcPod: "web-1"})
		if f.endAfterFirst {
			return errors.New("rpc error: transport is closing")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (f *fakeFlowSource) Done() <-chan struct{} { return f.done }
func (f *fakeFlowSource) CloseReason() string {
	if f.closed.Load() {
		return ""
	}
	return networking.ReasonRelayRestarted
}
func (f *fakeFlowSource) RelayNamespace() string { return "kube-system" }
func (f *fakeFlowSource) Close() {
	f.closed.Store(true)
	f.once.Do(func() { close(f.done) })
}

// toggleAccess allows every check except the resources in denied, which a
// test may change while a stream runs.
type toggleAccess struct {
	mu     sync.Mutex
	denied map[string]bool
}

func (a *toggleAccess) deny(resource string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.denied[resource] = true
}

func (a *toggleAccess) CanAccess(_ context.Context, _, _ string, _ []string, _, resource, _ string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.denied[resource], nil
}

type remoteFlowsEnv struct {
	url    string
	srv    *Server
	audit  *captureAudit
	access *toggleAccess
	source *fakeFlowSource
}

// newRemoteFlowsEnv serves the flows route with no local Hubble client and a
// stubbed remote relay. registered says whether flowsRemoteCluster is in the
// cluster registry revalidation reads.
func newRemoteFlowsEnv(t *testing.T, registered bool) *remoteFlowsEnv {
	t.Helper()
	env := &remoteFlowsEnv{
		srv:    testServer(t),
		audit:  &captureAudit{},
		access: &toggleAccess{denied: map[string]bool{}},
		source: newFakeFlowSource(),
	}
	env.srv.AuditLogger = env.audit
	env.srv.flowAccess = env.access
	records := map[string]*store.ClusterRecord{}
	if registered {
		records[flowsRemoteCluster] = &store.ClusterRecord{ID: flowsRemoteCluster}
	}
	env.srv.metricsClusters = &fakeClusterRegistry{records: records}
	env.srv.openRemoteFlows = func(_ context.Context, clusterID string, _ *auth.User, namespace string) (flowStream, error) {
		if clusterID != flowsRemoteCluster || namespace != "default" {
			return nil, &networking.FlowSourceError{Reason: "forbidden", Message: "unexpected target"}
		}
		return env.source, nil
	}

	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws/flows", env.srv.handleWSFlows)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	env.url = "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/flows"
	return env
}

// start opens a remote stream for default with token and sends the filter.
func (env *remoteFlowsEnv) start(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(env.url, http.Header{"Origin": {"http://localhost"}})
	if err != nil {
		t.Fatalf("WS dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]any{"type": "auth", "token": token, "clusterId": flowsRemoteCluster}); err != nil {
		t.Fatalf("WriteJSON auth: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"namespace": "default"}); err != nil {
		t.Fatalf("WriteJSON filter: %v", err)
	}
	return conn
}

// dial starts a stream and returns it after the subscribed frame.
func (env *remoteFlowsEnv) dial(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	conn := env.start(t, token)
	if msg := readFrame(t, conn); msg["type"] != "subscribed" {
		t.Fatalf("first frame = %v, want subscribed", msg)
	}
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

// readUntilError reads past flow frames and returns the first error frame.
func readUntilError(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	for {
		msg := readFrame(t, conn)
		if msg["type"] == "error" {
			return msg
		}
	}
}

// waitStreamsDone waits for every flow handler to return.
func waitStreamsDone(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for flowWSCount.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d flow streams still running", flowWSCount.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func setRevalidateInterval(t *testing.T, d time.Duration) {
	t.Helper()
	prev := flowRevalidateInterval
	flowRevalidateInterval = d
	t.Cleanup(func() { flowRevalidateInterval = prev })
}

// Covers AE1: an in-band remote cluster streams from that cluster's relay
// while this server has no local Hubble client at all.
func TestHandleWSFlows_RemoteStreamsWithoutLocalHubble(t *testing.T) {
	env := newRemoteFlowsEnv(t, true)
	if env.srv.NetworkingHandler != nil {
		t.Fatal("test server has a networking handler")
	}
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	msg := readFrame(t, conn)
	data, _ := msg["data"].(map[string]any)
	if msg["type"] != "flow" || data["srcPod"] != "web-1" {
		t.Fatalf("frame = %v, want a web-1 flow from the remote relay", msg)
	}
	conn.Close()
	waitStreamsDone(t)
	if !env.source.closed.Load() {
		t.Error("flow source not closed after the stream ended")
	}

	env.audit.mu.Lock()
	defer env.audit.mu.Unlock()
	if len(env.audit.entries) != 1 {
		t.Fatalf("audit entries = %+v, want one stream-open row", env.audit.entries)
	}
	e := env.audit.entries[0]
	if e.User != "alice" || e.ClusterID != flowsRemoteCluster || e.ResourceNamespace != "default" || e.Result != audit.ResultSuccess {
		t.Errorf("audit row = %+v, want alice's successful open on %s/default", e, flowsRemoteCluster)
	}
}

// Covers AE5, with the pump, keepalive, revalidation and expiry timer all
// running while the evict hook fires: the hook must not block, and the
// stream must end with cluster_removed rather than hang.
func TestHandleWSFlows_RemoteEvictEndsStreamWithoutDeadlock(t *testing.T) {
	setRevalidateInterval(t, 5*time.Millisecond)
	env := newRemoteFlowsEnv(t, true)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	readFrame(t, conn) // streaming

	start := time.Now()
	env.srv.flowStreams.CancelCluster(flowsRemoteCluster)
	env.srv.flowStreams.CancelCluster(flowsRemoteCluster)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("evict hook took %v, want it non-blocking", elapsed)
	}
	if msg := readUntilError(t, conn); msg["reason"] != "cluster_removed" || msg["retryable"] != nil {
		t.Fatalf("final frame = %v, want a non-retryable cluster_removed", msg)
	}
	waitStreamsDone(t)
}

func TestHandleWSFlows_RemoteRevalidationEndsStream(t *testing.T) {
	for name, tc := range map[string]struct {
		registered bool
		revoke     string
		want       string
	}{
		"namespace access revoked":   {registered: true, revoke: "pods", want: "access_revoked"},
		"port-forward revoked":       {registered: true, revoke: "pods/portforward", want: "access_revoked"},
		"cluster gone from registry": {registered: false, want: "cluster_removed"},
	} {
		t.Run(name, func(t *testing.T) {
			setRevalidateInterval(t, 20*time.Millisecond)
			env := newRemoteFlowsEnv(t, tc.registered)
			conn := env.dial(t, issueWSToken(t, env.srv, true))
			if tc.revoke != "" {
				env.access.deny(tc.revoke)
			}
			if msg := readUntilError(t, conn); msg["reason"] != tc.want {
				t.Fatalf("final frame = %v, want %s", msg, tc.want)
			}
			waitStreamsDone(t)
		})
	}
}

func TestHandleWSFlows_RemoteStreamEndsAtTokenExpiry(t *testing.T) {
	env := newRemoteFlowsEnv(t, true)
	// A token that expires in two seconds; access stays allowed throughout.
	claims := auth.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "test-user",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Second)),
		},
		Username:           "alice",
		KubernetesUsername: "alice",
		Roles:              []string{"admin"},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-signing-key-minimum-32-bytes"))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	conn := env.dial(t, token)
	msg := readUntilError(t, conn)
	if msg["reason"] != "token_expired" || msg["retryable"] != true {
		t.Fatalf("final frame = %v, want a retryable token_expired", msg)
	}
	waitStreamsDone(t)
}

func TestHandleWSFlows_RemoteRelayRestartIsRetryable(t *testing.T) {
	env := newRemoteFlowsEnv(t, true)
	env.source.endAfterFirst = true
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	msg := readUntilError(t, conn)
	if msg["reason"] != "relay_restarted" || msg["retryable"] != true {
		t.Fatalf("final frame = %v, want a retryable relay_restarted", msg)
	}
	waitStreamsDone(t)
}

func TestHandleWSFlows_PumpPanicSendsErrorFrame(t *testing.T) {
	env := newRemoteFlowsEnv(t, true)
	env.source.panics = true
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	msg := readUntilError(t, conn)
	if msg["message"] != "flow stream interrupted" {
		t.Fatalf("final frame = %v, want the interrupted error", msg)
	}
	waitStreamsDone(t)
}

func TestHandleWSFlows_RemoteRefusalCarriesReason(t *testing.T) {
	env := newRemoteFlowsEnv(t, true)
	env.srv.openRemoteFlows = func(context.Context, string, *auth.User, string) (flowStream, error) {
		return nil, &networking.FlowSourceError{Reason: networking.ReasonTunnelLimit, Message: "too many Hubble connections", Retryable: true}
	}
	conn := env.start(t, issueWSToken(t, env.srv, true))
	msg := readFrame(t, conn)
	if msg["reason"] != networking.ReasonTunnelLimit || msg["retryable"] != true {
		t.Fatalf("frame = %v, want a retryable tunnel_limit", msg)
	}
	env.audit.mu.Lock()
	defer env.audit.mu.Unlock()
	if len(env.audit.entries) != 1 || env.audit.entries[0].Result != audit.ResultFailure || env.audit.entries[0].ClusterID != flowsRemoteCluster {
		t.Errorf("audit entries = %+v, want one failure on %s", env.audit.entries, flowsRemoteCluster)
	}
}
