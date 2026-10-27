package server

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/gorilla/websocket"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	k8sfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// echoExecutor stands in for a container shell: it writes back whatever
// arrives on stdin until stdin closes.
type echoExecutor struct{}

func (e echoExecutor) Stream(o remotecommand.StreamOptions) error {
	return e.StreamWithContext(context.Background(), o)
}

func (echoExecutor) StreamWithContext(_ context.Context, o remotecommand.StreamOptions) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := o.Stdin.Read(buf)
		if n > 0 {
			if _, werr := o.Stdout.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			return nil
		}
	}
}

// execTestPath is the pod every exec test targets.
const execTestPath = "/ws/exec/default/web-0/app"

// wsExecTest is a Server serving the production exec route, with a fake
// executor that counts how many executors were built.
type wsExecTest struct {
	srv    *Server
	base   string // ws://host, without a path
	url    string // base + execTestPath
	audit  *captureAudit
	builds *atomic.Int64
}

func newWSExecTest(t *testing.T, ac *resources.AccessChecker) *wsExecTest {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	factory := k8s.NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, k8sfake.NewSimpleDynamicClient(scheme.Scheme))

	lg := &captureAudit{}
	var builds atomic.Int64
	srv := testServer(t)
	srv.AuditLogger = lg
	srv.ResourceHandler = &resources.Handler{
		K8sClient:     factory,
		ClusterRouter: k8s.NewClusterRouter(factory, nil, "", logger),
		AccessChecker: ac,
		AuditLogger:   lg,
		Logger:        logger,
		TaskManager:   resources.NewTaskManager(),
		ExecExecutor: func(_, _, _, _ string) (remotecommand.Executor, error) {
			builds.Add(1)
			return echoExecutor{}, nil
		},
	}

	mux := chi.NewRouter()
	mux.Get("/ws/exec/{namespace}/{name}/{container}", srv.wsExecHandler().ServeHTTP)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	base := "ws" + strings.TrimPrefix(ts.URL, "http")
	return &wsExecTest{srv: srv, base: base, url: base + execTestPath, audit: lg, builds: &builds}
}

// dialWS opens a WebSocket that the test closes on cleanup.
func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://localhost"}})
	if err != nil {
		if resp != nil {
			t.Fatalf("WS dial failed (HTTP %d): %v", resp.StatusCode, err)
		}
		t.Fatalf("WS dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// expectShell reads the first frame and requires it to announce a shell.
func expectShell(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read shell frame: %v", err)
	}
	if msg["type"] != "shell" {
		t.Fatalf("first frame = %v, want a shell frame", msg)
	}
}

// expectEcho sends input as an input frame and requires the fake shell to
// write all of it back.
func expectEcho(t *testing.T, conn *websocket.Conn, input []byte) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"type": "input", "data": base64.StdEncoding.EncodeToString(input)}); err != nil {
		t.Fatalf("write input: %v", err)
	}
	var echoed []byte
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for len(echoed) < len(input) {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read echo after %d bytes: %v", len(echoed), err)
		}
		if kind == websocket.BinaryMessage {
			echoed = append(echoed, data...)
		}
	}
	if !bytes.Equal(echoed, input) {
		t.Fatalf("stdin received %d bytes, want the %d-byte input intact", len(echoed), len(input))
	}
}

// openShell authenticates in-band naming local and waits for the shell frame.
func (e *wsExecTest) openShell(t *testing.T) *websocket.Conn {
	t.Helper()
	conn := dialWS(t, e.url)
	token := issueWSToken(t, e.srv, true)
	if err := conn.WriteJSON(map[string]any{"type": "auth", "token": token, "clusterId": "local"}); err != nil {
		t.Fatalf("WriteJSON auth: %v", err)
	}
	expectShell(t, conn)
	return conn
}

// waitAudit returns the first Pod/exec audit entry with the given result,
// waiting briefly because the session writes its row after the shell frame.
func (e *wsExecTest) waitAudit(t *testing.T, result audit.Result) audit.Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.audit.mu.Lock()
		for _, entry := range e.audit.entries {
			if entry.Result == result && entry.ResourceKind == "Pod/exec" {
				e.audit.mu.Unlock()
				return entry
			}
		}
		e.audit.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s audit row for Pod/exec; got %+v", result, e.audit.entries)
	return audit.Entry{}
}

// Covers R21: an in-band auth frame naming local opens a shell and writes
// the success audit row for that cluster and pod.
func TestWSExec_InBandLocal_OpensShell(t *testing.T) {
	e := newWSExecTest(t, resources.NewAlwaysAllowAccessChecker())
	e.openShell(t)

	entry := e.waitAudit(t, audit.ResultSuccess)
	if entry.Action != audit.ActionCreate || entry.ClusterID != "local" ||
		entry.ResourceNamespace != "default" || entry.ResourceName != "web-0" {
		t.Fatalf("success audit row = %+v, want create on local default/web-0", entry)
	}
}

// The permission check is create on pods/exec in the pod's namespace, on the
// cluster the stream named.
func TestWSExec_ChecksCreatePodsExec(t *testing.T) {
	var mu sync.Mutex
	var checks []string
	ac := resources.NewRecordingAccessChecker(func(clusterID, _ string, _ []string, verb, resource, namespace string) {
		mu.Lock()
		defer mu.Unlock()
		checks = append(checks, strings.Join([]string{clusterID, verb, resource, namespace}, " "))
	})
	e := newWSExecTest(t, ac)
	e.openShell(t)

	mu.Lock()
	defer mu.Unlock()
	if len(checks) != 1 || checks[0] != "local create pods/exec default" {
		t.Fatalf("access checks = %q, want one check: local create pods/exec default", checks)
	}
}

// The in-band path arrives with the auth helper's 4 KiB read limit; the
// session raises it so a large paste still reaches stdin.
func TestWSExec_InBandLargeInputReachesStdin(t *testing.T) {
	e := newWSExecTest(t, resources.NewAlwaysAllowAccessChecker())
	conn := e.openShell(t)
	expectEcho(t, conn, bytes.Repeat([]byte("k"), 10*1024))
}

// The auth helper leaves its auth-frame read deadline on the connection. The
// session clears it, or an idle shell would end when that deadline passed.
// The test sets a short deadline itself rather than waiting out the helper's.
func TestRunExecSession_ClearsAuthReadDeadline(t *testing.T) {
	e := newWSExecTest(t, resources.NewAlwaysAllowAccessChecker())
	user := &auth.User{Username: "alice", KubernetesUsername: "alice"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		ctx := middleware.WithClusterID(r.Context(), "local")
		e.srv.ResourceHandler.RunExecSession(r.WithContext(ctx), conn, user, "default", "web-0", "app")
	}))
	t.Cleanup(ts.Close)

	conn := dialWS(t, "ws"+strings.TrimPrefix(ts.URL, "http"))
	expectShell(t, conn) // shell detection takes 1s, well past the deadline
	expectEcho(t, conn, []byte("still here"))
}

// Many connections can pass the pre-upgrade capacity check while their auth
// frames are outstanding. Authenticating them all at once must still start
// no more sessions than the limit allows.
func TestWSExec_DelayedAuthCannotExceedCap(t *testing.T) {
	e := newWSExecTest(t, resources.NewAlwaysAllowAccessChecker())
	token := issueWSToken(t, e.srv, true)

	const n = 60 // above maxExecConnections (50)
	conns := make([]*websocket.Conn, n)
	for i := range conns {
		conns[i] = dialWS(t, e.url)
	}
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
		// Later tests need free slots: wait for the sessions to wind down.
		deadline := time.Now().Add(5 * time.Second)
		for resources.ExecAtCapacity() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	})
	for _, c := range conns {
		if err := c.WriteJSON(map[string]any{"type": "auth", "token": token, "clusterId": "local"}); err != nil {
			t.Fatalf("WriteJSON auth: %v", err)
		}
	}

	var shells, refused atomic.Int64
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Go(func() {
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			var msg map[string]any
			if err := c.ReadJSON(&msg); err != nil {
				return
			}
			switch {
			case msg["type"] == "shell":
				shells.Add(1)
			case msg["message"] == "too many exec connections":
				refused.Add(1)
			}
		})
	}
	wg.Wait()

	if refused.Load() == 0 || shells.Load()+refused.Load() != n || !resources.ExecAtCapacity() {
		t.Fatalf("shells=%d refused=%d atCapacity=%v, want sessions capped at the limit and the rest refused",
			shells.Load(), refused.Load(), resources.ExecAtCapacity())
	}
}

// Each refusal is reasoned, and none builds an executor.
func TestWSExec_InBandRefusals(t *testing.T) {
	allow := resources.NewAlwaysAllowAccessChecker()
	cases := []struct {
		name      string
		ac        *resources.AccessChecker
		admin     bool
		path      string
		header    string
		clusterID string
		// wantReason is the frame's reason; when empty, wantMessage is
		// matched instead.
		wantReason  string
		wantMessage string
		// wantAuditPod expects a failure audit row naming the pod; the
		// auth helper's own refusals audit without it.
		wantAudit    bool
		wantAuditPod bool
	}{
		{name: "remote in-band cluster", ac: allow, admin: true, clusterID: "remote-1",
			wantReason: string(k8s.ReasonUnsupportedPlatform), wantAudit: true},
		{name: "remote header, no in-band cluster", ac: allow, admin: true, header: "remote-1",
			wantReason: string(k8s.ReasonUnsupportedPlatform), wantAudit: true, wantAuditPod: true},
		{name: "no cluster named", ac: allow, admin: true,
			wantReason: wsReasonClusterRequired},
		{name: "no create pods/exec", ac: resources.NewAlwaysDenyAccessChecker(), clusterID: "local",
			wantReason: string(k8s.ReasonForbidden), wantAudit: true, wantAuditPod: true},
		{name: "permission check fails", ac: resources.NewErroringAccessChecker(errors.New("SAR unavailable")), clusterID: "local",
			wantMessage: "failed to check permissions"},
		{name: "invalid container name", ac: allow, admin: true, clusterID: "local", path: "/ws/exec/default/web-0/Bad_Name",
			wantMessage: "invalid container name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newWSExecTest(t, tc.ac)
			url := e.url
			if tc.path != "" {
				url = e.base + tc.path
			}
			auth := map[string]any{"token": issueWSToken(t, e.srv, tc.admin)}
			if tc.clusterID != "" {
				auth["clusterId"] = tc.clusterID
			}
			msg := wsFirstFrame(t, url, tc.header, auth)
			if msg["type"] != "error" {
				t.Fatalf("frame = %v, want an error frame", msg)
			}
			if tc.wantReason != "" && msg["reason"] != tc.wantReason {
				t.Fatalf("frame = %v, want reason %s", msg, tc.wantReason)
			}
			if tc.wantReason == "" && msg["message"] != tc.wantMessage {
				t.Fatalf("frame = %v, want message %q", msg, tc.wantMessage)
			}
			if tc.wantAudit {
				entry := e.waitAudit(t, audit.ResultFailure)
				if !strings.Contains(entry.Detail, tc.wantReason) {
					t.Fatalf("failure audit detail = %q, want it to name %s", entry.Detail, tc.wantReason)
				}
				if tc.wantAuditPod && (entry.ResourceNamespace != "default" || entry.ResourceName != "web-0") {
					t.Fatalf("failure audit row = %+v, want it to name default/web-0", entry)
				}
			}
			if n := e.builds.Load(); n != 0 {
				t.Fatalf("built %d executors for a refused request, want 0", n)
			}
		})
	}
}

// A valid Authorization header no longer authenticates exec: the request still
// has to send an auth frame first, and input in its place opens no shell.
func TestWSExec_AuthorizationHeaderWithoutAuthFrameRefused(t *testing.T) {
	e := newWSExecTest(t, resources.NewAlwaysAllowAccessChecker())
	token := issueWSToken(t, e.srv, true)

	h := http.Header{"Origin": {"http://localhost"}, "X-Cluster-ID": {"local"}}
	h.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(e.url, h)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{"type": "input", "data": "id\n"}); err != nil {
		t.Fatalf("WriteJSON input: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg["type"] != "error" || msg["message"] != "invalid auth message" {
		t.Fatalf("frame = %v, want the invalid auth message error", msg)
	}
	if n := e.builds.Load(); n != 0 {
		t.Fatalf("built %d executors without an auth frame, want 0", n)
	}
}
