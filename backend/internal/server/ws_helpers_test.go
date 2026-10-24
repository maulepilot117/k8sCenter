package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// countingClusterRecords is a cluster registry that counts its lookups, so a
// test can prove a refusal happened before any of them.
type countingClusterRecords struct {
	mu      sync.Mutex
	records map[string]*store.ClusterRecord
	calls   int
}

func (c *countingClusterRecords) Get(_ context.Context, id string) (*store.ClusterRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	rec, ok := c.records[id]
	if !ok {
		return nil, fmt.Errorf("getting cluster %s: %w", id, pgx.ErrNoRows)
	}
	return rec, nil
}

// remoteCreatedAt is the registration instant of the fake remote cluster.
var remoteCreatedAt = time.Date(2026, 10, 1, 12, 30, 0, 123456789, time.UTC)

type wsHelperFixture struct {
	url     string
	srv     *Server
	audit   *captureAudit
	records *countingClusterRecords
}

// newWSHelperFixture serves one WebSocket route through the real
// WSClusterContext middleware and wsAuthAndUpgradeFor. A connection that
// passes answers {"type":"ok","cluster":<effective id>}.
func newWSHelperFixture(t *testing.T, route wsRoute) *wsHelperFixture {
	t.Helper()
	f := &wsHelperFixture{
		srv:   testServer(t),
		audit: &captureAudit{},
		records: &countingClusterRecords{records: map[string]*store.ClusterRecord{
			"remote-1": {ID: "remote-1", CreatedAt: remoteCreatedAt},
		}},
	}
	f.srv.AuditLogger = f.audit
	f.srv.metricsClusters = f.records

	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, _, ctx := f.srv.wsAuthAndUpgradeFor(w, r, route)
		if conn == nil {
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]any{"type": "ok", "cluster": middleware.ClusterIDFromContext(ctx)})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	f.url = "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	return f
}

func (f *wsHelperFixture) token(t *testing.T, admin bool) string {
	t.Helper()
	roles := []string{"viewer"}
	if admin {
		roles = []string{"admin"}
	}
	tok, err := f.srv.TokenManager.IssueAccessToken(&auth.User{
		ID: "u1", Username: "alice", KubernetesUsername: "alice", Roles: roles,
	})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return tok
}

// connect dials with an optional X-Cluster-ID header, sends the auth frame
// and returns the first frame the server answers with.
func (f *wsHelperFixture) connect(t *testing.T, header string, authMsg map[string]any) map[string]any {
	t.Helper()
	h := http.Header{"Origin": {"http://localhost"}}
	if header != "" {
		h.Set("X-Cluster-ID", header)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(f.url, h)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial: HTTP %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	authMsg["type"] = "auth"
	if err := conn.WriteJSON(authMsg); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

func expectRefused(t *testing.T, msg map[string]any, reason string) {
	t.Helper()
	if msg["type"] != "error" || msg["reason"] != reason {
		t.Fatalf("got %v, want an error frame with reason %q", msg, reason)
	}
}

func expectCluster(t *testing.T, msg map[string]any, cluster string) {
	t.Helper()
	if msg["type"] != "ok" || msg["cluster"] != cluster {
		t.Fatalf("got %v, want an accepted connection on cluster %q", msg, cluster)
	}
}

var logsRoute = wsRoute{auditAction: audit.ActionReadLogs, auditKind: "Pod"}

// Covers AE1: a flows connection naming no cluster is never served as local.
func TestWSAuth_FlowsWithoutAnyClusterIsRefused(t *testing.T) {
	f := newWSHelperFixture(t, flowsWSRoute)
	msg := f.connect(t, "", map[string]any{"token": f.token(t, true)})
	expectRefused(t, msg, wsReasonClusterRequired)
}

func TestWSAuth_FlowsAcceptsLocalInBandOrHeader(t *testing.T) {
	f := newWSHelperFixture(t, flowsWSRoute)
	expectCluster(t, f.connect(t, "", map[string]any{"token": f.token(t, false), "clusterId": "local"}), "local")
	expectCluster(t, f.connect(t, "local", map[string]any{"token": f.token(t, false)}), "local")
	expectCluster(t, f.connect(t, "remote-1", map[string]any{"token": f.token(t, true)}), "remote-1")
}

func TestWSAuth_LogsRefuseRemoteInBand(t *testing.T) {
	f := newWSHelperFixture(t, logsRoute)
	msg := f.connect(t, "", map[string]any{"token": f.token(t, true), "clusterId": "remote-1"})
	expectRefused(t, msg, "unsupported_platform")

	// Without the requirement, a connection naming nothing is local.
	expectCluster(t, f.connect(t, "", map[string]any{"token": f.token(t, false)}), "local")
}

func TestWSAuth_HeaderConflictIsRefusedAndAudited(t *testing.T) {
	f := newWSHelperFixture(t, flowsWSRoute)
	msg := f.connect(t, "local", map[string]any{"token": f.token(t, true), "clusterId": "remote-1"})
	expectRefused(t, msg, wsReasonPinMismatch)

	f.audit.mu.Lock()
	defer f.audit.mu.Unlock()
	if len(f.audit.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(f.audit.entries))
	}
	e := f.audit.entries[0]
	if e.Result != audit.ResultFailure || e.ClusterID != "remote-1" || e.ResourceKind != "Flow" {
		t.Errorf("audit entry = %+v, want a Flow failure naming remote-1", e)
	}
}

func TestWSAuth_NonAdminRemoteRefusedBeforeLookup(t *testing.T) {
	f := newWSHelperFixture(t, flowsWSRoute)
	msg := f.connect(t, "", map[string]any{
		"token":      f.token(t, false),
		"clusterId":  "remote-1",
		"generation": remoteCreatedAt.Format(time.RFC3339Nano),
	})
	expectRefused(t, msg, "forbidden")

	if f.records.calls != 0 {
		t.Errorf("cluster lookups = %d, want 0 before the admin check", f.records.calls)
	}
	f.audit.mu.Lock()
	defer f.audit.mu.Unlock()
	if len(f.audit.entries) != 1 || f.audit.entries[0].ClusterID != "remote-1" {
		t.Errorf("audit entries = %+v, want one naming remote-1", f.audit.entries)
	}
}

func TestWSAuth_GenerationComparesInstants(t *testing.T) {
	f := newWSHelperFixture(t, flowsWSRoute)
	tok := f.token(t, true)

	// The same instant written with a non-UTC offset is the same registration.
	sameInstant := remoteCreatedAt.In(time.FixedZone("UTC+2", 2*60*60)).Format(time.RFC3339Nano)
	expectCluster(t, f.connect(t, "", map[string]any{"token": tok, "clusterId": "remote-1", "generation": sameInstant}), "remote-1")

	other := remoteCreatedAt.Add(time.Second).Format(time.RFC3339Nano)
	expectRefused(t, f.connect(t, "", map[string]any{"token": tok, "clusterId": "remote-1", "generation": other}), wsReasonGenerationMismatch)

	expectRefused(t, f.connect(t, "", map[string]any{"token": tok, "clusterId": "gone", "generation": other}), "cluster_unknown")

	// A missing generation is allowed.
	expectCluster(t, f.connect(t, "", map[string]any{"token": tok, "clusterId": "remote-1"}), "remote-1")

	// The local generation is the literal "local".
	expectCluster(t, f.connect(t, "", map[string]any{"token": tok, "clusterId": "local", "generation": "local"}), "local")
	expectRefused(t, f.connect(t, "", map[string]any{"token": tok, "clusterId": "local", "generation": other}), wsReasonGenerationMismatch)
}

func TestWSAuth_OverlongInBandIDIsRefused(t *testing.T) {
	f := newWSHelperFixture(t, flowsWSRoute)
	msg := f.connect(t, "", map[string]any{"token": f.token(t, true), "clusterId": strings.Repeat("a", 65)})
	expectRefused(t, msg, wsReasonClusterInvalid)
}

// failingClusterRecords is a cluster registry whose reads fail.
type failingClusterRecords struct{}

func (failingClusterRecords) Get(context.Context, string) (*store.ClusterRecord, error) {
	return nil, errors.New("connection refused")
}

// A generation that cannot be checked is refused: with no registry the
// cluster is unknown, and a registry that cannot be read is reported as such.
func TestWSAuth_GenerationLookupFailures(t *testing.T) {
	gen := remoteCreatedAt.Format(time.RFC3339Nano)

	t.Run("no registry", func(t *testing.T) {
		f := newWSHelperFixture(t, flowsWSRoute)
		f.srv.metricsClusters = nil
		f.srv.ClusterStore = nil
		msg := f.connect(t, "", map[string]any{"token": f.token(t, true), "clusterId": "remote-1", "generation": gen})
		expectRefused(t, msg, "cluster_unknown")
	})
	t.Run("registry unreadable", func(t *testing.T) {
		f := newWSHelperFixture(t, flowsWSRoute)
		f.srv.metricsClusters = failingClusterRecords{}
		msg := f.connect(t, "", map[string]any{"token": f.token(t, true), "clusterId": "remote-1", "generation": gen})
		expectRefused(t, msg, "db_unavailable")
	})
}

// The frame sizes below are literals rather than the constants, so a change
// to either limit has to change these tests too.
const (
	testAuthFrameLimit  = 16 * 1024
	testLaterFrameLimit = 4 * 1024
)

// largeAuthFrame returns an auth frame for a user in enough IdP groups that
// the frame is at least minLen bytes, so it outgrows the limit later frames
// get. The groups are what make a real token this large.
func largeAuthFrame(t *testing.T, srv *Server, minLen int) []byte {
	t.Helper()
	var groups []string
	for i := 0; ; i++ {
		groups = append(groups, fmt.Sprintf("oidc:%08d-7f3c-4a1e-9b2d-5c8e0f6a1b2c", i))
		if i%10 != 0 {
			continue
		}
		tok, err := srv.TokenManager.IssueAccessToken(&auth.User{
			ID: "u1", Username: "alice", KubernetesUsername: "alice",
			KubernetesGroups: groups, Roles: []string{"viewer"},
		})
		if err != nil {
			t.Fatalf("IssueAccessToken: %v", err)
		}
		frame, _ := json.Marshal(map[string]any{"type": "auth", "token": tok, "clusterId": "local"})
		if len(frame) >= minLen {
			return frame
		}
	}
}

// A user in many groups has a token larger than later frames may be. An
// auth frame just under the auth limit is still accepted.
func TestWSAuth_LargeTokenIsAccepted(t *testing.T) {
	f := newWSHelperFixture(t, flowsWSRoute)
	frame := largeAuthFrame(t, f.srv, testAuthFrameLimit-700)
	if len(frame) > testAuthFrameLimit {
		t.Fatalf("frame is %d bytes, want it under %d", len(frame), testAuthFrameLimit)
	}

	conn, _, err := websocket.DefaultDialer.Dial(f.url, http.Header{"Origin": {"http://localhost"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	expectCluster(t, msg, "local")
}

// The auth frame's allowance has its own bound, and frames after auth are
// held to the small limit again.
func TestWSAuth_FrameLimits(t *testing.T) {
	srv := testServer(t)
	mux := chi.NewRouter()
	mux.Use(middleware.WSClusterContext)
	mux.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := srv.wsAuthAndUpgradeFor(w, r, flowsWSRoute)
		if conn == nil {
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]any{"type": "ok"})
		if _, _, err := conn.ReadMessage(); err == nil {
			conn.WriteJSON(map[string]any{"type": "read"})
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	// authed dials and authenticates with a token over the later-frame limit.
	authed := func(t *testing.T) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://localhost"}})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		conn.WriteMessage(websocket.TextMessage, largeAuthFrame(t, srv, testLaterFrameLimit+1))
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil || msg["type"] != "ok" {
			t.Fatalf("auth: msg=%v err=%v, want ok", msg, err)
		}
		return conn
	}

	t.Run("a frame after auth at the small limit is read", func(t *testing.T) {
		conn := authed(t)
		conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", testLaterFrameLimit)))
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil || msg["type"] != "read" {
			t.Fatalf("msg=%v err=%v, want the frame read", msg, err)
		}
	})

	t.Run("a frame after auth over the small limit ends the stream", func(t *testing.T) {
		conn := authed(t)
		conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", testLaterFrameLimit+1)))
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err == nil {
			t.Fatalf("got %v, want the stream closed for an oversized frame", msg)
		}
	})

	t.Run("an auth frame over its own limit is not read", func(t *testing.T) {
		conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://localhost"}})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		// Under the limit this token would earn "invalid token"; over it,
		// the frame is never parsed and the connection closes.
		frame, _ := json.Marshal(map[string]any{"type": "auth", "token": strings.Repeat("x", testAuthFrameLimit)})
		conn.WriteMessage(websocket.TextMessage, frame)
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err == nil {
			t.Fatalf("got %v, want the connection closed unread", msg)
		}
	})
}
