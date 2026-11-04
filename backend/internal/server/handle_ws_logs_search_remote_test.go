package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/loki"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// --- Loki live tail per cluster (remote logs and shells U12) ---

const lokiTailRemote = "remote-loki-1"

// fakeLokiTail is a Loki tail endpoint. A live one sends a remote line every
// 20ms; a silent one sends one line and then neither writes nor reads, so it
// never answers a ping.
type fakeLokiTail struct {
	*httptest.Server
	hosts chan string
}

func newFakeLokiTail(t *testing.T, silent bool) *fakeLokiTail {
	t.Helper()
	ft := &fakeLokiTail{hosts: make(chan string, 16)}
	upgrader := websocket.Upgrader{}
	ft.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			return
		}
		if r.URL.Path != "/loki/api/v1/tail" {
			http.NotFound(w, r)
			return
		}
		ft.hosts <- r.Host
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		line := []byte(`{"streams":[{"stream":{"namespace":"shop"},"values":[["1","remote-line"]]}]}`)
		if silent {
			_ = c.WriteMessage(websocket.TextMessage, line)
			<-r.Context().Done()
			return
		}
		for {
			if err := c.WriteMessage(websocket.TextMessage, line); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	t.Cleanup(ft.Close)
	return ft
}

// tailTunnel dials target for every connection until it is closed.
type tailTunnel struct {
	target string
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool
}

func (tt *tailTunnel) DialContext(ctx context.Context) (net.Conn, error) {
	select {
	case <-tt.done:
		return nil, k8s.ErrTunnelClosed
	default:
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", tt.target)
}

func (tt *tailTunnel) Done() <-chan struct{} { return tt.done }

func (tt *tailTunnel) Close() error {
	tt.closed.Store(true)
	tt.once.Do(func() { close(tt.done) })
	return nil
}

// tailForwarder opens a tailTunnel to target for each port-forward. A
// stalling one opens nothing and waits until its context ends.
type tailForwarder struct {
	target  string
	stall   bool
	stalled chan struct{}
	mu      sync.Mutex
	opened  []*tailTunnel
}

func (f *tailForwarder) PortForward(ctx context.Context, _, _ string, _ []string, _, _ string, _ int32) (k8s.Tunnel, error) {
	if f.stall {
		close(f.stalled)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	tt := &tailTunnel{target: f.target, done: make(chan struct{})}
	f.opened = append(f.opened, tt)
	return tt, nil
}

func (f *tailForwarder) tunnels() []*tailTunnel {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*tailTunnel(nil), f.opened...)
}

type tailClients struct{ cs kubernetes.Interface }

func (c tailClients) ClientForCluster(context.Context, string, string, []string) (kubernetes.Interface, error) {
	return c.cs, nil
}

// remoteLokiObjects is the remote cluster's loki-gateway in monitoring and
// its ready pod.
func remoteLokiObjects() []kruntime.Object {
	selector := map[string]string{"app": "loki-gateway"}
	return []kruntime.Object{
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "loki-gateway"},
			Spec: corev1.ServiceSpec{
				Selector: selector,
				Ports:    []corev1.ServicePort{{Name: "http-metrics", Port: 80, TargetPort: intstr.FromString("http-metrics")}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "loki-gateway-0", Labels: selector},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:  "gateway",
				Ports: []corev1.ContainerPort{{Name: "http-metrics", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
			}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{{
					Type: corev1.PodReady, Status: corev1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Hour)),
				}},
			},
		},
	}
}

type lokiTailEnv struct {
	srv    *Server
	url    string
	fwd    *tailForwarder
	access *recordingAccess
	audit  *captureAudit
}

// newLokiTailEnv serves the tail route. The local cluster has no Loki; the
// remote cluster serves objs, and its tunnels reach lokiTail.
func newLokiTailEnv(t *testing.T, lokiTail *fakeLokiTail, objs ...kruntime.Object) *lokiTailEnv {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	env := &lokiTailEnv{
		srv:    testServer(t),
		fwd:    &tailForwarder{target: lokiTail.Listener.Addr().String()},
		access: &recordingAccess{denied: map[string]bool{}},
		audit:  &captureAudit{},
	}
	disc := loki.NewDiscoverer(nil, config.LokiConfig{}, logger)
	env.srv.LokiHandler = &loki.Handler{
		Discoverer: disc,
		Logger:     logger,
		Resolver:   loki.NewResolverWith(disc, nil, tailClients{cs: kfake.NewClientset(objs...)}, env.fwd, logger),
	}
	env.srv.AuditLogger = env.audit
	env.srv.logAccess = env.access
	env.srv.metricsClusters = &fakeClusterRegistry{records: map[string]*store.ClusterRecord{lokiTailRemote: {ID: lokiTailRemote}}}

	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws/logs-search", env.srv.handleWSLogsSearch)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	env.url = "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/logs-search"
	return env
}

// open starts a tail naming cluster in-band and subscribes to shop's logs.
func (env *lokiTailEnv) open(t *testing.T, token, cluster string) *websocket.Conn {
	t.Helper()
	conn := wsDialAndAuth(t, env.url, "", cluster, token)
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "query": `{namespace="shop"}`}); err != nil {
		t.Fatalf("WriteJSON subscribe: %v", err)
	}
	return conn
}

// dial opens a remote tail and returns it once a remote line arrived.
func (env *lokiTailEnv) dial(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	conn := env.open(t, token, lokiTailRemote)
	if msg := readFrame(t, conn); msg["type"] != "subscribed" {
		t.Fatalf("first frame = %v, want subscribed", msg)
	}
	if msg := readFrame(t, conn); msg["type"] != "log" || !strings.Contains(stringOf(msg["payload"]), "remote-line") {
		t.Fatalf("frame = %v, want a remote log line", msg)
	}
	return conn
}

func stringOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func (env *lokiTailEnv) auditEntries() []audit.Entry {
	env.audit.mu.Lock()
	defer env.audit.mu.Unlock()
	return append([]audit.Entry(nil), env.audit.entries...)
}

// waitLokiTailsDone waits for every Loki tail handler to return.
func waitLokiTailsDone(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for logSearchWSCount.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d Loki tails still running", logSearchWSCount.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A remote tail streams the remote Loki's lines over one tunnel while the
// local cluster has no Loki at all, and the tunnel closes when it ends.
func TestHandleWSLogsSearch_RemoteStreamsWithoutLocalLoki(t *testing.T) {
	lokiTail := newFakeLokiTail(t, false)
	env := newLokiTailEnv(t, lokiTail, remoteLokiObjects()...)
	conn := env.dial(t, issueWSToken(t, env.srv, true))

	if host := <-lokiTail.hosts; host != "loki-gateway.monitoring.svc:80" {
		t.Fatalf("tail Host = %q, want the Service's in-cluster name", host)
	}
	if n := len(env.fwd.tunnels()); n != 1 {
		t.Fatalf("tunnels opened = %d, want one for the tail's life", n)
	}
	var opened bool
	for _, e := range env.auditEntries() {
		if e.Result == audit.ResultSuccess && e.ClusterID == lokiTailRemote && e.ResourceKind == "LokiTail" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("no success audit row for the remote tail: %+v", env.auditEntries())
	}

	conn.Close()
	waitLokiTailsDone(t)
	if !env.fwd.tunnels()[0].closed.Load() {
		t.Fatal("the tail's tunnel was left open")
	}
}

// The local cluster still tails its own Loki, with no tunnel.
func TestHandleWSLogsSearch_LocalStreamsFromLocalLoki(t *testing.T) {
	lokiTail := newFakeLokiTail(t, false)
	env := newLokiTailEnv(t, lokiTail)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	disc := loki.NewDiscoverer(nil, config.LokiConfig{URL: lokiTail.URL}, logger)
	disc.Discover(context.Background())
	env.srv.LokiHandler.Discoverer = disc
	env.srv.LokiHandler.Resolver = loki.NewResolverWith(disc, nil, nil, nil, logger)

	conn := env.open(t, issueWSToken(t, env.srv, true), "local")
	if msg := readFrame(t, conn); msg["type"] != "subscribed" {
		t.Fatalf("first frame = %v, want subscribed", msg)
	}
	if msg := readFrame(t, conn); msg["type"] != "log" {
		t.Fatalf("frame = %v, want a log line", msg)
	}
	if len(env.fwd.tunnels()) != 0 {
		t.Fatal("a local tail opened a tunnel")
	}
}

// Covers AE1 for the tail: a remote cluster with no Loki is refused in-band
// with loki_not_found, never subscribed and never served locally.
func TestHandleWSLogsSearch_RemoteWithoutLokiRefused(t *testing.T) {
	env := newLokiTailEnv(t, newFakeLokiTail(t, false))
	conn := env.open(t, issueWSToken(t, env.srv, true), lokiTailRemote)
	msg := readFrame(t, conn)
	if msg["type"] != "error" || msg["reason"] != loki.ReasonLokiNotFound {
		t.Fatalf("got %v, want a loki_not_found refusal", msg)
	}
	waitLokiTailsDone(t)
	var refused bool
	for _, e := range env.auditEntries() {
		if e.Result == audit.ResultFailure && e.ClusterID == lokiTailRemote {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("no failure audit row for the refused tail: %+v", env.auditEntries())
	}
}

// A non-admin naming a remote cluster is refused before anything resolves.
func TestHandleWSLogsSearch_RemoteNonAdminRefused(t *testing.T) {
	env := newLokiTailEnv(t, newFakeLokiTail(t, false), remoteLokiObjects()...)
	conn := wsDialAndAuth(t, env.url, "", lokiTailRemote, issueWSToken(t, env.srv, false))
	defer conn.Close()
	if msg := readFrame(t, conn); msg["type"] != "error" || msg["reason"] != string(k8s.ReasonForbidden) {
		t.Fatalf("got %v, want a forbidden refusal", msg)
	}
	if len(env.fwd.tunnels()) != 0 {
		t.Fatal("a tunnel was opened for a non-admin")
	}
}

// Removing the cluster ends its tails at once with cluster_removed.
func TestHandleWSLogsSearch_RemoteEvictEndsTail(t *testing.T) {
	env := newLokiTailEnv(t, newFakeLokiTail(t, false), remoteLokiObjects()...)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	env.srv.logStreams.CancelCluster(lokiTailRemote)
	if msg := readLogsUntilError(t, conn); msg["reason"] != "cluster_removed" {
		t.Fatalf("got %v, want cluster_removed", msg)
	}
	waitLokiTailsDone(t)
	if !env.fwd.tunnels()[0].closed.Load() {
		t.Fatal("the evicted tail's tunnel was left open")
	}
}

// A tail ends at its token's expiry with a retryable token_expired.
func TestHandleWSLogsSearch_RemoteTailEndsAtTokenExpiry(t *testing.T) {
	env := newLokiTailEnv(t, newFakeLokiTail(t, false), remoteLokiObjects()...)
	conn := env.dial(t, shortLivedAdminToken(t, 1500*time.Millisecond))
	msg := readLogsUntilError(t, conn)
	if msg["reason"] != "token_expired" || msg["retryable"] != true {
		t.Fatalf("got %v, want a retryable token_expired", msg)
	}
}

// Revalidation ends a port-forwarded tail once the user's
// pods/portforward grant in Loki's namespace is revoked.
func TestHandleWSLogsSearch_RemoteRevalidationEndsTail(t *testing.T) {
	prev := lokiTailRevalidateInterval
	lokiTailRevalidateInterval = 50 * time.Millisecond
	t.Cleanup(func() { lokiTailRevalidateInterval = prev })

	env := newLokiTailEnv(t, newFakeLokiTail(t, false), remoteLokiObjects()...)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	env.access.deny("pods/portforward")
	if msg := readLogsUntilError(t, conn); msg["reason"] != "access_revoked" {
		t.Fatalf("got %v, want access_revoked", msg)
	}
	want := accessCheck{lokiTailRemote, "create", "pods/portforward", "monitoring"}
	var asked bool
	for _, c := range env.access.askedChecks() {
		if c == want {
			asked = true
		}
	}
	if !asked {
		t.Fatalf("checks = %+v, want %+v", env.access.askedChecks(), want)
	}
}

// revalidateLokiTail asks for create pods/portforward in Loki's namespace
// only on the port-forward path: a configured URL, and a tail still being
// resolved, need no Kubernetes grant. A cluster no longer registered ends
// the tail on every path.
func TestRevalidateLokiTail(t *testing.T) {
	env := newLokiTailEnv(t, newFakeLokiTail(t, false))
	env.access.deny("pods/portforward")
	user := &auth.User{Username: "alice", KubernetesUsername: "alice"}
	forwarded := &lokiTailTarget{source: loki.SourcePortForward, namespace: "monitoring"}
	ctx := context.Background()

	if end := env.srv.revalidateLokiTail(ctx, lokiTailRemote, user, &lokiTailTarget{source: loki.SourceBinding}); end != nil {
		t.Fatalf("binding tail ended with %v, want it kept: it holds no Kubernetes grant", end)
	}
	if end := env.srv.revalidateLokiTail(ctx, lokiTailRemote, user, nil); end != nil {
		t.Fatalf("tail still resolving ended with %v, want it kept", end)
	}
	if n := len(env.access.askedChecks()); n != 0 {
		t.Fatalf("asked %d access checks without a port-forward, want none", n)
	}
	if end := env.srv.revalidateLokiTail(ctx, lokiTailRemote, user, forwarded); end != logStreamEnds.accessRevoked {
		t.Fatalf("port-forwarded tail end = %v, want access_revoked", end)
	}
	want := accessCheck{lokiTailRemote, "create", "pods/portforward", "monitoring"}
	if got := env.access.askedChecks(); len(got) != 1 || got[0] != want {
		t.Fatalf("checks = %+v, want exactly %+v", got, want)
	}

	delete(env.srv.metricsClusters.(*fakeClusterRegistry).records, lokiTailRemote)
	for _, target := range []*lokiTailTarget{nil, {source: loki.SourceBinding}, forwarded} {
		if end := env.srv.revalidateLokiTail(ctx, lokiTailRemote, user, target); end != logStreamEnds.clusterRemoved {
			t.Fatalf("target %+v on a removed cluster ended with %v, want cluster_removed", target, end)
		}
	}
}

// A token that expires while the tail's port-forward is still opening ends
// the tail at expiry, and no tunnel is opened as the user afterwards.
func TestHandleWSLogsSearch_TokenExpiryDuringResolve(t *testing.T) {
	env := newLokiTailEnv(t, newFakeLokiTail(t, false), remoteLokiObjects()...)
	env.fwd.stall, env.fwd.stalled = true, make(chan struct{})
	conn := env.open(t, shortLivedAdminToken(t, 1500*time.Millisecond), lokiTailRemote)
	select {
	case <-env.fwd.stalled:
	case <-time.After(5 * time.Second):
		t.Fatal("the tail never started its port-forward")
	}
	msg := readLogsUntilError(t, conn)
	if msg["reason"] != "token_expired" || msg["retryable"] != true {
		t.Fatalf("got %v, want a retryable token_expired", msg)
	}
	waitLokiTailsDone(t)
	if n := len(env.fwd.tunnels()); n != 0 {
		t.Fatalf("tunnels opened = %d after the token expired, want none", n)
	}
}

// A Loki that keeps sending lines keeps its tail alive past the idle bound:
// each line refreshes it.
func TestHandleWSLogsSearch_LinesKeepTailAlive(t *testing.T) {
	prev := lokiTailIdleTimeout
	lokiTailIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { lokiTailIdleTimeout = prev })

	env := newLokiTailEnv(t, newFakeLokiTail(t, false), remoteLokiObjects()...)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	until := time.Now().Add(4 * lokiTailIdleTimeout)
	for time.Now().Before(until) {
		if msg := readFrame(t, conn); msg["type"] != "log" {
			t.Fatalf("frame = %v while Loki was sending lines, want only log lines", msg)
		}
	}
}

// A tunnel that ends under a tail ends it as retryable loki_unreachable.
func TestHandleWSLogsSearch_RemoteTunnelEndIsRetryable(t *testing.T) {
	env := newLokiTailEnv(t, newFakeLokiTail(t, false), remoteLokiObjects()...)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	_ = env.fwd.tunnels()[0].Close()
	msg := readLogsUntilError(t, conn)
	if msg["reason"] != loki.ReasonLokiUnreachable || msg["retryable"] != true {
		t.Fatalf("got %v, want a retryable loki_unreachable", msg)
	}
}

// A Loki that goes silent, answering no ping, ends the tail rather than
// holding it and its tunnel open.
func TestHandleWSLogsSearch_SilentLokiEndsTail(t *testing.T) {
	prev := lokiTailIdleTimeout
	lokiTailIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { lokiTailIdleTimeout = prev })

	env := newLokiTailEnv(t, newFakeLokiTail(t, true), remoteLokiObjects()...)
	conn := env.dial(t, issueWSToken(t, env.srv, true))
	msg := readLogsUntilError(t, conn)
	if msg["reason"] != loki.ReasonLokiUnreachable || msg["retryable"] != true {
		t.Fatalf("got %v, want a retryable loki_unreachable", msg)
	}
	waitLokiTailsDone(t)
	if !env.fwd.tunnels()[0].closed.Load() {
		t.Fatal("the silent tail's tunnel was left open")
	}
}
