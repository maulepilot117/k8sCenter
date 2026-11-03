package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	k8sfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// wsLogsTestServer wires a minimal Server with a single WS log route behind
// the production WSClusterContext middleware, so a test names its cluster the
// way a real client does: an X-Cluster-ID header (mobile) or the auth frame
// (web). It returns the test HTTP server plus a JWT factory function.
func wsLogsTestServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Stub k8s factory + router (no real cluster needed — the stub's log
	// stream fails after the subscribed frame)
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

	// The handler confirms with "subscribed" once auth, the cluster check and
	// RBAC have all passed, before it opens the (stub) log stream.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg["type"] != "subscribed" {
		t.Fatalf("got %v, want the stream admitted with a subscribed frame", msg)
	}
}

// TestHandleWSLogsSearch_NoCluster_Refused is the Loki tail's half of AE13:
// a tail that names no cluster is refused before any Loki dial. A fake Loki
// that answers /ready gets the handler past its pre-upgrade availability
// check, so the route is exercised through the real handler.
func TestHandleWSLogsSearch_NoCluster_Refused(t *testing.T) {
	fakeLoki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fakeLoki.Close)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	disc := loki.NewDiscoverer(nil, config.LokiConfig{URL: fakeLoki.URL}, logger)
	disc.Discover(context.Background())
	if disc.Client() == nil {
		t.Fatal("fake Loki was not discovered; the test cannot reach the cluster check")
	}

	srv := testServer(t)
	srv.LokiHandler = &loki.Handler{Discoverer: disc, Logger: logger}
	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws/logs-search", srv.handleWSLogsSearch)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs-search"
	conn := wsDialAndAuth(t, wsURL, "", "", issueWSToken(t, srv, true))
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

// --- Live remote log tails and their lifecycle (U7) ---

const logsRemoteCluster = "remote-logs-1"

type remoteLogsEnv struct {
	url    string
	srv    *Server
	audit  *captureAudit
	access *toggleAccess
	client kubernetes.Interface

	mu      sync.Mutex
	clients []string // the cluster and user of every remote client built
}

// newRemoteLogsEnv serves the log route with a stubbed remote API server
// that follows default/web-1, writing "line N" every few milliseconds until
// the request ends, or until it has written endAfter lines when endAfter is
// positive. registered says whether logsRemoteCluster is in the cluster
// registry revalidation reads.
func newRemoteLogsEnv(t *testing.T, registered bool, endAfter int) *remoteLogsEnv {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/pods/web-1/log" || r.URL.Query().Get("follow") != "true" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		for i := 1; endAfter <= 0 || i <= endAfter; i++ {
			fmt.Fprintf(w, "line %d\n", i)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(api.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: api.URL})
	if err != nil {
		t.Fatalf("remote clientset: %v", err)
	}

	env := &remoteLogsEnv{
		srv:    testServer(t),
		audit:  &captureAudit{},
		access: &toggleAccess{denied: map[string]bool{}},
		client: cs,
	}
	env.srv.AuditLogger = env.audit
	env.srv.ResourceHandler = &resources.Handler{AccessChecker: resources.NewAlwaysAllowAccessChecker()}
	env.srv.logAccess = env.access
	records := map[string]*store.ClusterRecord{}
	if registered {
		records[logsRemoteCluster] = &store.ClusterRecord{ID: logsRemoteCluster}
	}
	env.srv.metricsClusters = &fakeClusterRegistry{records: records}
	env.srv.logClient = func(_ context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error) {
		env.mu.Lock()
		env.clients = append(env.clients, clusterID+"/"+user.KubernetesUsername)
		env.mu.Unlock()
		return env.client, nil
	}

	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws/logs/{namespace}/{pod}/{container}", env.srv.handleWSLogs)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	env.url = "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/web-1/app"
	return env
}

// start opens a remote tail of default/web-1, naming the cluster in-band
// (web) or, with byHeader, by X-Cluster-ID (mobile), and sends the filter.
func (env *remoteLogsEnv) start(t *testing.T, token string, byHeader bool) *websocket.Conn {
	t.Helper()
	var conn *websocket.Conn
	if byHeader {
		conn = wsDialAndAuth(t, env.url, logsRemoteCluster, "", token)
	} else {
		conn = wsDialAndAuth(t, env.url, "", logsRemoteCluster, token)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]any{"tailLines": 10}); err != nil {
		t.Fatalf("WriteJSON filter: %v", err)
	}
	return conn
}

// dial starts an in-band tail and returns it after the subscribed frame.
func (env *remoteLogsEnv) dial(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	conn := env.start(t, token, false)
	if msg := readFrame(t, conn); msg["type"] != "subscribed" {
		t.Fatalf("first frame = %v, want subscribed", msg)
	}
	return conn
}

func (env *remoteLogsEnv) builtClients() []string {
	env.mu.Lock()
	defer env.mu.Unlock()
	return append([]string(nil), env.clients...)
}

// readLogsUntilError reads past log frames and returns the first error
// frame. Its deadline covers the whole read, not each frame: lines arrive
// every few milliseconds, so a per-frame deadline would never fire.
func readLogsUntilError(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("no error frame: %v", err)
		}
		if msg["type"] == "error" {
			return msg
		}
	}
}

// waitLogStreamsDone waits for every log handler to return.
func waitLogStreamsDone(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for logWSCount.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d log streams still running", logWSCount.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func setLogRevalidateInterval(t *testing.T, d time.Duration) {
	t.Helper()
	prev := logRevalidateInterval
	logRevalidateInterval = d
	t.Cleanup(func() { logRevalidateInterval = prev })
}

// A remote cluster named in-band (web) or by header (mobile) streams from
// that cluster through a client built as the user, and the open is audited
// against the remote cluster.
func TestHandleWSLogs_RemoteStreams(t *testing.T) {
	for name, byHeader := range map[string]bool{"in-band": false, "header": true} {
		t.Run(name, func(t *testing.T) {
			env := newRemoteLogsEnv(t, true, 0)
			conn := env.start(t, issueWSToken(t, env.srv, true), byHeader)
			if msg := readFrame(t, conn); msg["type"] != "subscribed" {
				t.Fatalf("first frame = %v, want subscribed", msg)
			}
			if msg := readFrame(t, conn); msg["type"] != "log" || msg["data"] != "line 1" {
				t.Fatalf("frame = %v, want line 1 from the remote pod", msg)
			}
			conn.Close()
			waitLogStreamsDone(t)

			if got := env.builtClients(); len(got) != 1 || got[0] != logsRemoteCluster+"/alice" {
				t.Errorf("clients built = %v, want one for alice on %s", got, logsRemoteCluster)
			}
			env.audit.mu.Lock()
			defer env.audit.mu.Unlock()
			if len(env.audit.entries) != 1 {
				t.Fatalf("audit entries = %+v, want one stream-open row", env.audit.entries)
			}
			e := env.audit.entries[0]
			if e.User != "alice" || e.ClusterID != logsRemoteCluster || e.ResourceNamespace != "default" ||
				e.ResourceName != "web-1" || e.Action != audit.ActionReadLogs || e.Result != audit.ResultSuccess {
				t.Errorf("audit row = %+v, want alice's successful open of %s/default/web-1", e, logsRemoteCluster)
			}
		})
	}
}

// A container that stops ends the stream with the end frame, after the
// lines it wrote.
func TestHandleWSLogs_RemoteStreamEndsAtEOF(t *testing.T) {
	env := newRemoteLogsEnv(t, true, 2)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	for _, want := range []string{"line 1", "line 2"} {
		if msg := readFrame(t, conn); msg["data"] != want {
			t.Fatalf("frame = %v, want %s", msg, want)
		}
	}
	if msg := readFrame(t, conn); msg["type"] != "end" {
		t.Fatalf("frame = %v, want end", msg)
	}
	waitLogStreamsDone(t)
}

// Covers AE4: a remote tail ends at its token's expiry with a retryable
// token_expired, while access stays allowed throughout.
func TestHandleWSLogs_RemoteStreamEndsAtTokenExpiry(t *testing.T) {
	env := newRemoteLogsEnv(t, true, 0)
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
	msg := readLogsUntilError(t, conn)
	if msg["reason"] != "token_expired" || msg["retryable"] != true {
		t.Fatalf("final frame = %v, want a retryable token_expired", msg)
	}
	waitLogStreamsDone(t)
}

func TestHandleWSLogs_RemoteRevalidationEndsStream(t *testing.T) {
	for name, tc := range map[string]struct {
		registered bool
		revoke     bool
		want       string
	}{
		"pods/log revoked":           {registered: true, revoke: true, want: "access_revoked"},
		"cluster gone from registry": {registered: false, want: "cluster_removed"},
	} {
		t.Run(name, func(t *testing.T) {
			setLogRevalidateInterval(t, 20*time.Millisecond)
			env := newRemoteLogsEnv(t, tc.registered, 0)
			conn := env.dial(t, issueWSToken(t, env.srv, true))
			if tc.revoke {
				env.access.deny("pods/log")
			}
			msg := readLogsUntilError(t, conn)
			if msg["reason"] != tc.want || msg["retryable"] != nil {
				t.Fatalf("final frame = %v, want a non-retryable %s", msg, tc.want)
			}
			if !strings.Contains(msg["message"].(string), "the log stream") {
				t.Errorf("message %q does not name the log stream", msg["message"])
			}
			waitLogStreamsDone(t)
		})
	}
}

// Covers AE5 for logs, with the reader, keepalive, revalidation and expiry
// timer all running while the evict hook fires: the hook must not block,
// and the stream must end with cluster_removed rather than hang.
func TestHandleWSLogs_RemoteEvictEndsStreamWithoutDeadlock(t *testing.T) {
	setLogRevalidateInterval(t, 5*time.Millisecond)
	env := newRemoteLogsEnv(t, true, 0)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	readFrame(t, conn) // streaming

	start := time.Now()
	env.srv.logStreams.CancelCluster(logsRemoteCluster)
	env.srv.logStreams.CancelCluster(logsRemoteCluster)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("evict hook took %v, want it non-blocking", elapsed)
	}
	if msg := readLogsUntilError(t, conn); msg["reason"] != "cluster_removed" || msg["retryable"] != nil {
		t.Fatalf("final frame = %v, want a non-retryable cluster_removed", msg)
	}
	waitLogStreamsDone(t)
}

// A cluster removed while the remote client is being built ends the stream
// before it subscribes.
func TestHandleWSLogs_RemoteEvictDuringOpenEndsStream(t *testing.T) {
	env := newRemoteLogsEnv(t, true, 0)
	env.srv.logClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		env.srv.logStreams.CancelCluster(logsRemoteCluster)
		return env.client, nil
	}
	conn := env.start(t, issueWSToken(t, env.srv, true), false)
	if msg := readFrame(t, conn); msg["reason"] != "cluster_removed" {
		t.Fatalf("first frame = %v, want cluster_removed", msg)
	}
	waitLogStreamsDone(t)
}

// A remote cluster the router cannot reach is answered with unreachable and
// a failure audit row, never with another cluster's logs.
func TestHandleWSLogs_RemoteUnreachableRefused(t *testing.T) {
	env := newRemoteLogsEnv(t, true, 0)
	env.srv.logClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return nil, errors.New("dial tcp: connection refused")
	}
	conn := env.start(t, issueWSToken(t, env.srv, true), false)
	if msg := readFrame(t, conn); msg["type"] != "error" || msg["reason"] != "unreachable" {
		t.Fatalf("first frame = %v, want an unreachable refusal", msg)
	}
	waitLogStreamsDone(t)
	env.audit.mu.Lock()
	defer env.audit.mu.Unlock()
	if len(env.audit.entries) != 1 || env.audit.entries[0].Result != audit.ResultFailure || env.audit.entries[0].ClusterID != logsRemoteCluster {
		t.Errorf("audit entries = %+v, want one failure on %s", env.audit.entries, logsRemoteCluster)
	}
}

// A non-admin naming a remote cluster, in-band or by header, is refused
// before any remote client is built.
func TestHandleWSLogs_RemoteNonAdminRefusedBeforeClient(t *testing.T) {
	for name, byHeader := range map[string]bool{"in-band": false, "header": true} {
		t.Run(name, func(t *testing.T) {
			env := newRemoteLogsEnv(t, true, 0)
			conn := env.start(t, issueWSToken(t, env.srv, false), byHeader)
			if msg := readFrame(t, conn); msg["type"] != "error" || msg["reason"] != "forbidden" {
				t.Fatalf("first frame = %v, want a forbidden refusal", msg)
			}
			if got := env.builtClients(); len(got) != 0 {
				t.Errorf("clients built = %v, want none", got)
			}
		})
	}
}
