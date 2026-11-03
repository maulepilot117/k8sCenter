package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

// --- Live log tails, local and remote, and their lifecycle (U7) ---

const logsRemoteCluster = "remote-logs-1"

// fakeLogAPI is a Kubernetes API server that follows default/web-1's log and
// answers any other pod with 404.
type fakeLogAPI struct {
	endAfter int  // lines to write before EOF; 0 writes until the request ends
	stall    bool // hold the response, unsent, until the request ends
	longLine bool // write one line longer than logMaxLineBuffer, then EOF
}

func (f fakeLogAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/namespaces/default/pods/web-1/log" || r.URL.Query().Get("follow") != "true" {
		http.NotFound(w, r)
		return
	}
	if f.stall {
		<-r.Context().Done()
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	if f.longLine {
		w.Write([]byte(strings.Repeat("a", logMaxLineBuffer+1024)))
		return
	}
	for i := 1; f.endAfter <= 0 || i <= f.endAfter; i++ {
		fmt.Fprintf(w, "line %d\n", i)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// recordingAccess answers revalidation: it records the cluster of every
// check and denies the resources in denied, which a test may change while a
// stream runs.
type recordingAccess struct {
	mu       sync.Mutex
	denied   map[string]bool
	err      error
	clusters []string
}

func (a *recordingAccess) deny(resource string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.denied[resource] = true
}

func (a *recordingAccess) CanAccess(_ context.Context, clusterID, _ string, _ []string, _, resource, _ string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clusters = append(a.clusters, clusterID)
	if a.err != nil {
		return false, a.err
	}
	return !a.denied[resource], nil
}

func (a *recordingAccess) askedClusters() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.clusters...)
}

type logsEnv struct {
	url      string
	srv      *Server
	audit    *captureAudit
	access   *recordingAccess
	registry *fakeClusterRegistry
	client   kubernetes.Interface

	mu        sync.Mutex
	clients   []string // the cluster and user of every remote client built
	openAsked []string // the cluster of every open-time access check
}

// newLogsEnv serves the log route with api as both the local cluster and
// logsRemoteCluster. registered says whether logsRemoteCluster is in the
// cluster registry revalidation reads.
func newLogsEnv(t *testing.T, api fakeLogAPI, registered bool) *logsEnv {
	t.Helper()
	apiServer := httptest.NewServer(api)
	t.Cleanup(apiServer.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: apiServer.URL})
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}

	env := &logsEnv{
		srv:    testServer(t),
		audit:  &captureAudit{},
		access: &recordingAccess{denied: map[string]bool{}},
		client: cs,
	}
	env.srv.AuditLogger = env.audit
	env.srv.ResourceHandler = &resources.Handler{
		K8sClient: k8s.NewTestClientFactoryWithDynamic(cs, k8sfake.NewSimpleDynamicClient(scheme.Scheme)),
		AccessChecker: resources.NewRecordingAccessChecker(func(clusterID, _ string, _ []string, _, _, _ string) {
			env.mu.Lock()
			defer env.mu.Unlock()
			env.openAsked = append(env.openAsked, clusterID)
		}),
	}
	env.srv.logAccess = env.access
	records := map[string]*store.ClusterRecord{}
	if registered {
		records[logsRemoteCluster] = &store.ClusterRecord{ID: logsRemoteCluster}
	}
	env.registry = &fakeClusterRegistry{records: records}
	env.srv.metricsClusters = env.registry
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
	env.url = "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs/default/"
	return env
}

// open starts a tail of default/<pod>, naming the cluster by header and
// in-band as given, and sends the filter.
func (env *logsEnv) open(t *testing.T, token, header, inBand, pod string) *websocket.Conn {
	t.Helper()
	conn := wsDialAndAuth(t, env.url+pod+"/app", header, inBand, token)
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]any{"tailLines": 10}); err != nil {
		t.Fatalf("WriteJSON filter: %v", err)
	}
	return conn
}

// start opens a remote tail of web-1, naming the cluster in-band (web) or,
// with byHeader, by X-Cluster-ID (mobile).
func (env *logsEnv) start(t *testing.T, token string, byHeader bool) *websocket.Conn {
	t.Helper()
	if byHeader {
		return env.open(t, token, logsRemoteCluster, "", "web-1")
	}
	return env.open(t, token, "", logsRemoteCluster, "web-1")
}

// dial starts an in-band remote tail and returns it after the subscribed
// frame.
func (env *logsEnv) dial(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	conn := env.start(t, token, false)
	if msg := readFrame(t, conn); msg["type"] != "subscribed" {
		t.Fatalf("first frame = %v, want subscribed", msg)
	}
	return conn
}

func (env *logsEnv) builtClients() []string {
	env.mu.Lock()
	defer env.mu.Unlock()
	return append([]string(nil), env.clients...)
}

func (env *logsEnv) openChecks() []string {
	env.mu.Lock()
	defer env.mu.Unlock()
	return append([]string(nil), env.openAsked...)
}

func (env *logsEnv) auditEntries() []audit.Entry {
	env.audit.mu.Lock()
	defer env.audit.mu.Unlock()
	return append([]audit.Entry(nil), env.audit.entries...)
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
		switch msg["type"] {
		case "error":
			return msg
		case "end":
			t.Fatalf("got end, want an error frame")
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

func setLogOpenTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := logOpenTimeout
	logOpenTimeout = d
	t.Cleanup(func() { logOpenTimeout = prev })
}

// shortLivedAdminToken signs an admin access token for alice that expires
// after d.
func shortLivedAdminToken(t *testing.T, d time.Duration) string {
	t.Helper()
	claims := auth.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "test-user",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(d)),
		},
		Username:           "alice",
		KubernetesUsername: "alice",
		Roles:              []string{"admin"},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-signing-key-minimum-32-bytes"))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return token
}

// Covers AE13: a web log stream that names no cluster, in-band or by header,
// is refused rather than served from the local cluster. Browsers cannot set
// X-Cluster-ID, so before this a remote selection streamed the local pod.
func TestHandleWSLogs_NoCluster_Refused(t *testing.T) {
	env := newLogsEnv(t, fakeLogAPI{}, true)
	conn := wsDialAndAuth(t, env.url+"web-1/app", "", "", issueWSToken(t, env.srv, true))
	defer conn.Close()
	if msg := readFrame(t, conn); msg["type"] != "error" || msg["reason"] != "cluster_required" {
		t.Fatalf("got %v, want a cluster_required refusal", msg)
	}
}

// A local tail, named in-band (web) or by header (mobile), streams through
// the local client factory, never builds a remote client, never registers
// for cluster eviction, and is audited against the configured cluster id.
func TestHandleWSLogs_LocalStreams(t *testing.T) {
	for name, tc := range map[string]struct{ header, inBand string }{
		"in-band": {inBand: "local"},
		"header":  {header: "local"},
	} {
		t.Run(name, func(t *testing.T) {
			env := newLogsEnv(t, fakeLogAPI{}, true)
			conn := env.open(t, issueWSToken(t, env.srv, false), tc.header, tc.inBand, "web-1")
			if msg := readFrame(t, conn); msg["type"] != "subscribed" {
				t.Fatalf("first frame = %v, want subscribed", msg)
			}
			if msg := readFrame(t, conn); msg["type"] != "log" || msg["data"] != "line 1" {
				t.Fatalf("frame = %v, want line 1 from the local pod", msg)
			}
			env.srv.logStreams.mu.Lock()
			registered := len(env.srv.logStreams.streams)
			env.srv.logStreams.mu.Unlock()
			if registered != 0 {
				t.Errorf("a local tail registered for cluster eviction")
			}
			conn.Close()
			waitLogStreamsDone(t)

			if got := env.builtClients(); len(got) != 0 {
				t.Errorf("remote clients built for a local tail: %v", got)
			}
			if got := env.openChecks(); len(got) != 1 || !k8s.IsLocalClusterID(got[0]) {
				t.Errorf("open-time checks asked of %v, want the local cluster once", got)
			}
			entries := env.auditEntries()
			if len(entries) != 1 || entries[0].ClusterID != env.srv.Config.ClusterID || entries[0].Result != audit.ResultSuccess {
				t.Errorf("audit entries = %+v, want one success on %s", entries, env.srv.Config.ClusterID)
			}
		})
	}
}

// A remote cluster named in-band (web) or by header (mobile) streams from
// that cluster through a client built as the user. The access check is
// asked of that cluster, and the open is audited against it.
func TestHandleWSLogs_RemoteStreams(t *testing.T) {
	for name, byHeader := range map[string]bool{"in-band": false, "header": true} {
		t.Run(name, func(t *testing.T) {
			env := newLogsEnv(t, fakeLogAPI{}, true)
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
			if got := env.openChecks(); len(got) != 1 || got[0] != logsRemoteCluster {
				t.Errorf("open-time checks asked of %v, want %s once", got, logsRemoteCluster)
			}
			entries := env.auditEntries()
			if len(entries) != 1 {
				t.Fatalf("audit entries = %+v, want one stream-open row", entries)
			}
			e := entries[0]
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
	env := newLogsEnv(t, fakeLogAPI{endAfter: 2}, true)
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

// A read that fails mid-stream (here a line past the scanner's limit) ends
// the stream as interrupted, never as a clean end.
func TestHandleWSLogs_ReaderErrorIsInterrupted(t *testing.T) {
	env := newLogsEnv(t, fakeLogAPI{longLine: true}, true)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	if msg := readLogsUntilError(t, conn); msg["message"] != "log stream interrupted" {
		t.Fatalf("final frame = %v, want log stream interrupted", msg)
	}
	waitLogStreamsDone(t)
}

// Covers AE4: a remote tail ends at its token's expiry with a retryable
// token_expired, while access stays allowed throughout.
func TestHandleWSLogs_RemoteStreamEndsAtTokenExpiry(t *testing.T) {
	env := newLogsEnv(t, fakeLogAPI{}, true)
	conn := env.dial(t, shortLivedAdminToken(t, 2*time.Second))
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
			env := newLogsEnv(t, fakeLogAPI{}, tc.registered)
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
			for _, c := range env.access.askedClusters() {
				if c != logsRemoteCluster {
					t.Errorf("revalidation asked of cluster %q, want %s", c, logsRemoteCluster)
				}
			}
		})
	}
}

// A revalidation that cannot be answered leaves the stream running; the
// token's expiry still bounds it.
func TestHandleWSLogs_RemoteRevalidationFailureKeepsStream(t *testing.T) {
	setLogRevalidateInterval(t, 10*time.Millisecond)
	env := newLogsEnv(t, fakeLogAPI{}, true)
	env.registry.err = errors.New("registry read failed")
	env.access.err = errors.New("access review failed")
	conn := env.dial(t, issueWSToken(t, env.srv, true))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	until := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(until) {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg["type"] != "log" {
			t.Fatalf("frame = %v, want the stream to keep running", msg)
		}
	}
	if len(env.access.askedClusters()) == 0 {
		t.Fatal("revalidation never ran")
	}
}

// Covers AE5 for logs, with the reader, keepalive, revalidation and expiry
// timer all running while the evict hook fires: the hook must not block,
// and the stream must end with cluster_removed rather than hang.
func TestHandleWSLogs_RemoteEvictEndsStreamWithoutDeadlock(t *testing.T) {
	setLogRevalidateInterval(t, 5*time.Millisecond)
	env := newLogsEnv(t, fakeLogAPI{}, true)
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
	env := newLogsEnv(t, fakeLogAPI{}, true)
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

// errUnreachable is how a dead remote cluster surfaces: a wrapped
// transport error.
var errUnreachable = fmt.Errorf("SelfSubjectAccessReview: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})

// A remote cluster that cannot be reached is refused with reason
// unreachable, a failure audit row naming it, and no word the mobile client
// treats as fatal, whether the access check or the client build fails. The
// access check is the first call to reach the cluster, so it is where an
// outage shows first.
func TestHandleWSLogs_RemoteUnreachableRefused(t *testing.T) {
	for name, breakIt := range map[string]func(env *logsEnv){
		"access check": func(env *logsEnv) {
			env.srv.ResourceHandler.AccessChecker = resources.NewErroringAccessChecker(errUnreachable)
		},
		"client build": func(env *logsEnv) {
			env.srv.logClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
				return nil, errUnreachable
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newLogsEnv(t, fakeLogAPI{}, true)
			breakIt(env)
			conn := env.start(t, issueWSToken(t, env.srv, true), true)
			msg := readFrame(t, conn)
			if msg["type"] != "error" || msg["reason"] != "unreachable" {
				t.Fatalf("first frame = %v, want an unreachable refusal", msg)
			}
			for _, word := range []string{"auth", "token", "permission"} {
				if strings.Contains(strings.ToLower(msg["message"].(string)), word) {
					t.Errorf("message %q contains %q, which mobile treats as fatal", msg["message"], word)
				}
			}
			waitLogStreamsDone(t)
			entries := env.auditEntries()
			if len(entries) != 1 || entries[0].Result != audit.ResultFailure || entries[0].ClusterID != logsRemoteCluster {
				t.Errorf("audit entries = %+v, want one failure on %s", entries, logsRemoteCluster)
			}
		})
	}
}

// A local access check that errors keeps its existing answer.
func TestHandleWSLogs_LocalAccessCheckErrorUnchanged(t *testing.T) {
	env := newLogsEnv(t, fakeLogAPI{}, true)
	env.srv.ResourceHandler.AccessChecker = resources.NewErroringAccessChecker(errUnreachable)
	conn := env.open(t, issueWSToken(t, env.srv, true), "", "local", "web-1")
	if msg := readFrame(t, conn); msg["message"] != "permission check failed" || msg["reason"] != nil {
		t.Fatalf("first frame = %v, want the local permission check failure", msg)
	}
	waitLogStreamsDone(t)
}

// A remote denial of get pods/log refuses the tail before any client is
// built, and the refusal is audited.
func TestHandleWSLogs_RemoteDeniedBeforeClient(t *testing.T) {
	env := newLogsEnv(t, fakeLogAPI{}, true)
	env.srv.ResourceHandler.AccessChecker = resources.NewDenyResourcesAccessChecker("pods/log")
	conn := env.start(t, issueWSToken(t, env.srv, true), false)
	if msg := readFrame(t, conn); msg["message"] != "no permission to view logs in this namespace" {
		t.Fatalf("first frame = %v, want the namespace denial", msg)
	}
	waitLogStreamsDone(t)
	if got := env.builtClients(); len(got) != 0 {
		t.Errorf("clients built = %v, want none", got)
	}
	if entries := env.auditEntries(); len(entries) != 1 || entries[0].Result != audit.ResultFailure {
		t.Errorf("audit entries = %+v, want one failure", entries)
	}
}

// A remote cluster that answers the log request with an error (no such pod)
// was reached: the refusal names the open, not the cluster's reachability.
func TestHandleWSLogs_RemoteMissingPodIsNotAReachFailure(t *testing.T) {
	env := newLogsEnv(t, fakeLogAPI{}, true)
	conn := env.open(t, issueWSToken(t, env.srv, true), "", logsRemoteCluster, "missing")
	if msg := readFrame(t, conn); msg["message"] != "failed to open log stream" || msg["reason"] != nil {
		t.Fatalf("first frame = %v, want a reasonless open failure", msg)
	}
	waitLogStreamsDone(t)
	if entries := env.auditEntries(); len(entries) != 1 || entries[0].Result != audit.ResultFailure || entries[0].ClusterID != logsRemoteCluster {
		t.Errorf("audit entries = %+v, want one failure on %s", entries, logsRemoteCluster)
	}
}

// A remote API server that accepts the log request and never answers holds
// the open. The open still ends, and frees its connection slot, at the open
// bound, at token expiry, and when the client leaves.
func TestHandleWSLogs_RemoteStalledOpenEnds(t *testing.T) {
	t.Run("open bound", func(t *testing.T) {
		setLogOpenTimeout(t, 200*time.Millisecond)
		env := newLogsEnv(t, fakeLogAPI{stall: true}, true)
		conn := env.start(t, issueWSToken(t, env.srv, true), false)
		if msg := readLogsUntilError(t, conn); msg["reason"] != "unreachable" {
			t.Fatalf("final frame = %v, want an unreachable refusal", msg)
		}
		waitLogStreamsDone(t)
		if entries := env.auditEntries(); len(entries) != 1 || entries[0].Result != audit.ResultFailure {
			t.Errorf("audit entries = %+v, want one failure", entries)
		}
	})
	t.Run("token expiry", func(t *testing.T) {
		env := newLogsEnv(t, fakeLogAPI{stall: true}, true)
		conn := env.start(t, shortLivedAdminToken(t, 2*time.Second), false)
		if msg := readLogsUntilError(t, conn); msg["reason"] != "token_expired" || msg["retryable"] != true {
			t.Fatalf("final frame = %v, want a retryable token_expired", msg)
		}
		waitLogStreamsDone(t)
	})
	t.Run("client leaves", func(t *testing.T) {
		env := newLogsEnv(t, fakeLogAPI{stall: true}, true)
		conn := env.start(t, issueWSToken(t, env.srv, true), false)
		time.Sleep(100 * time.Millisecond)
		conn.Close()
		waitLogStreamsDone(t)
	})
}

// A non-admin naming a remote cluster, in-band or by header, is refused
// before any remote client is built.
func TestHandleWSLogs_RemoteNonAdminRefusedBeforeClient(t *testing.T) {
	for name, byHeader := range map[string]bool{"in-band": false, "header": true} {
		t.Run(name, func(t *testing.T) {
			env := newLogsEnv(t, fakeLogAPI{}, true)
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
