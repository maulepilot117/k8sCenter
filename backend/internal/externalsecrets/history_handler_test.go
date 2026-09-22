package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// --- Fixtures ----------------------------------------------------------------

// fakeHistoryReader records every QueryPage call and serves rows filtered by
// (clusterID, uid), so a test can prove what identity the handler asked for
// rather than trusting a canned page.
type fakeHistoryReader struct {
	mu    sync.Mutex
	rows  []store.ESOSyncHistoryEntry
	err   error
	calls []historyQuery
}

type historyQuery struct {
	clusterID string
	uid       string
	after     *store.ESOHistoryCursor
	limit     int
}

func (f *fakeHistoryReader) QueryPage(ctx context.Context, clusterID, uid string, after *store.ESOHistoryCursor, limit int) (store.ESOHistoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, historyQuery{clusterID: clusterID, uid: uid, after: after, limit: limit})
	if err := ctx.Err(); err != nil {
		return store.ESOHistoryPage{}, err
	}
	if f.err != nil {
		return store.ESOHistoryPage{}, f.err
	}
	var out []store.ESOSyncHistoryEntry
	for _, e := range f.rows {
		if e.ClusterID == clusterID && e.UID == uid {
			out = append(out, e)
		}
	}
	return store.ESOHistoryPage{Entries: out}, nil
}

func (f *fakeHistoryReader) lastCall(t *testing.T) historyQuery {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("history store was never queried")
	}
	return f.calls[len(f.calls)-1]
}

// sensitiveEntry is a row whose every redactable field carries a marker an
// L1 response must never contain.
func sensitiveEntry(clusterID, uid string, id int64) store.ESOSyncHistoryEntry {
	return store.ESOSyncHistoryEntry{
		ID:                    id,
		ClusterID:             clusterID,
		UID:                   uid,
		Namespace:             "apps",
		Name:                  "db-creds",
		AttemptAt:             time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Outcome:               "failure",
		Reason:                "SecretSyncedError",
		Message:               "key 'prod/db/PROVIDER_PATH_MARKER' not found",
		DiffKeysAdded:         []string{"ADDED_KEY_MARKER"},
		DiffKeysRemoved:       []string{"REMOVED_KEY_MARKER"},
		DiffKeysChanged:       []string{"CHANGED_KEY_A", "CHANGED_KEY_B"},
		SyncedResourceVersion: "RV_MARKER_4711",
	}
}

// historyHandler builds a Handler whose impersonating dynamic client serves
// esObjs and whose history store is reader (a nil reader leaves the field a
// true nil interface, the production "no DB" shape).
func historyHandler(esObjs []runtime.Object, reader *fakeHistoryReader, ac *resources.AccessChecker) *Handler {
	dynFake := newEsoFakeDynClient(esObjs...)
	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: ac,
		Logger:        slog.Default(),
		ClusterID:     "local",
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
	}
	if reader != nil {
		h.HistoryStore = reader
	}
	return h
}

// esOnly grants every ESO-group check and denies core Secrets: the AE4
// "outcome-only" reader.
func esOnly(verb, group, resource, namespace string) bool {
	return !(group == "" && resource == "secrets")
}

func getHistory(t *testing.T, h *Handler, ns, name, query string, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	return serveHistory(h, historyRequest(ns, name, query, u))
}

func historyRequest(ns, name, query string, u *auth.User) *http.Request {
	target := "/externalsecrets/externalsecrets/" + ns + "/" + name + "/history"
	if query != "" {
		target += "?" + query
	}
	r := withUser(httptest.NewRequest(http.MethodGet, target, nil), u)
	return urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
}

func serveHistory(h *Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.HandleGetExternalSecretHistory(w, r)
	return w
}

var historyUser = &auth.User{KubernetesUsername: "alice", KubernetesGroups: []string{"dev"}}

// historyBody decodes a 200 response into the wire shape plus the raw entry
// maps, so tests can assert on key ABSENCE rather than on zero values.
type historyBody struct {
	Data struct {
		UID        string `json:"uid"`
		ClusterID  string `json:"clusterId"`
		Projection struct {
			Level         string   `json:"level"`
			DroppedFields []string `json:"droppedFields"`
		} `json:"projection"`
		Entries []map[string]json.RawMessage `json:"entries"`
	} `json:"data"`
	Metadata struct {
		Total    int    `json:"total"`
		Continue string `json:"continue"`
	} `json:"metadata"`
}

func decodeHistory(t *testing.T, w *httptest.ResponseRecorder) historyBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200\nbody: %s", w.Code, w.Body.String())
	}
	var b historyBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	if b.Data.Entries == nil {
		t.Fatalf("entries is null; an empty history must be [] \nbody: %s", w.Body.String())
	}
	return b
}

func assertErrorReason(t *testing.T, w *httptest.ResponseRecorder, status int, reason string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d; want %d\nbody: %s", w.Code, status, w.Body.String())
	}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	if env.Data != nil {
		t.Errorf("error response carries data: %s", env.Data)
	}
	if reason != "" && env.Error.Reason != reason {
		t.Errorf("reason = %q; want %q", env.Error.Reason, reason)
	}
}

// l1Absent is the field list an outcome-only entry must not carry. It is
// re-derived here, not imported, so deleting a field from the production
// projection's drop list cannot also weaken this oracle.
var l1Absent = []string{
	"message", "messageTruncated",
	"diffKeysAdded", "diffKeysRemoved", "diffKeysChanged",
	"syncedResourceVersion",
}

// --- AE4: the projection -----------------------------------------------------

func TestHistory_ESOnlyReader_OmitsKeysAndMessage(t *testing.T) {
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{sensitiveEntry("local", "uid-1", 7)}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewPredicateAccessChecker(esOnly))

	w := getHistory(t, h, "apps", "db-creds", "", historyUser)
	b := decodeHistory(t, w)

	for _, marker := range []string{
		"PROVIDER_PATH_MARKER", "ADDED_KEY_MARKER", "REMOVED_KEY_MARKER",
		"CHANGED_KEY_A", "RV_MARKER_4711",
	} {
		if strings.Contains(w.Body.String(), marker) {
			t.Errorf("L1 body leaks %q\nbody: %s", marker, w.Body.String())
		}
	}
	if b.Data.Projection.Level != "outcome-only" {
		t.Errorf("projection level = %q; want outcome-only", b.Data.Projection.Level)
	}
	if len(b.Data.Projection.DroppedFields) == 0 {
		t.Error("droppedFields is empty at L1; the client cannot say what is hidden")
	}
	if len(b.Data.Entries) != 1 {
		t.Fatalf("entries = %d; want 1", len(b.Data.Entries))
	}
	e := b.Data.Entries[0]
	for _, k := range l1Absent {
		if _, present := e[k]; present {
			t.Errorf("L1 entry carries %q; it must be absent, not empty", k)
		}
	}
	var counts struct{ Added, Removed, Changed int }
	if err := json.Unmarshal(e["diffKeyCounts"], &counts); err != nil {
		t.Fatalf("diffKeyCounts: %v (raw %s)", err, e["diffKeyCounts"])
	}
	if counts.Added != 1 || counts.Removed != 1 || counts.Changed != 2 {
		t.Errorf("diffKeyCounts = %+v; want 1/1/2", counts)
	}
	if string(e["outcome"]) != `"failure"` {
		t.Errorf("outcome = %s; want \"failure\"", e["outcome"])
	}
	if string(e["reason"]) != `"SecretSyncedError"` {
		t.Errorf("reason = %s; allowlisted reason must survive L1", e["reason"])
	}
}

func TestHistory_ESPlusSecretReader_ReturnsFullProjection(t *testing.T) {
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{sensitiveEntry("local", "uid-1", 7)}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysAllowAccessChecker())

	b := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", historyUser))

	if b.Data.Projection.Level != "full" {
		t.Errorf("projection level = %q; want full", b.Data.Projection.Level)
	}
	if b.Data.Projection.DroppedFields == nil || len(b.Data.Projection.DroppedFields) != 0 {
		t.Errorf("droppedFields = %v; want [] at L2", b.Data.Projection.DroppedFields)
	}
	e := b.Data.Entries[0]
	for _, k := range l1Absent {
		if _, present := e[k]; !present {
			t.Errorf("L2 entry is missing %q", k)
		}
	}
	if !strings.Contains(string(e["message"]), "PROVIDER_PATH_MARKER") {
		t.Errorf("message = %s; want the controller text", e["message"])
	}
	if string(e["syncedResourceVersion"]) != `"RV_MARKER_4711"` {
		t.Errorf("syncedResourceVersion = %s", e["syncedResourceVersion"])
	}
	if b.Data.UID != "uid-1" || b.Data.ClusterID != "local" {
		t.Errorf("envelope uid/clusterId = %q/%q", b.Data.UID, b.Data.ClusterID)
	}
}

// An L2 reader with a genuinely empty diff must see [] — distinguishable from
// the absent key an L1 reader gets.
func TestHistory_FullProjection_EmptyDiffIsEmptyArrayNotAbsent(t *testing.T) {
	row := sensitiveEntry("local", "uid-1", 7)
	row.DiffKeysAdded, row.DiffKeysRemoved, row.DiffKeysChanged = nil, nil, nil
	row.Message = ""
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{row}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysAllowAccessChecker())

	e := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", historyUser)).Data.Entries[0]
	for _, k := range []string{"diffKeysAdded", "diffKeysRemoved", "diffKeysChanged"} {
		if string(e[k]) != "[]" {
			t.Errorf("%s = %s; want []", k, e[k])
		}
	}
	if string(e["message"]) != `""` {
		t.Errorf("message = %s; want \"\"", e["message"])
	}
}

func TestHistory_CrossUserIsolation(t *testing.T) {
	// Real AccessChecker over per-user fake SSAR clients: alice may read
	// Secrets, bob may not. Exercises the cache-keyed path, not the predicate
	// short-circuit.
	ssarFor := func(allowSecrets bool) kubernetes.Interface {
		cs := kubefake.NewClientset()
		cs.PrependReactor("create", "selfsubjectaccessreviews", func(a clienttesting.Action) (bool, runtime.Object, error) {
			review := a.(clienttesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
			attrs := review.Spec.ResourceAttributes
			review.Status.Allowed = !(attrs.Group == "" && attrs.Resource == "secrets") || allowSecrets
			return true, review, nil
		})
		return cs
	}
	clients := map[string]kubernetes.Interface{"alice": ssarFor(true), "bob": ssarFor(false)}
	ac := resources.NewAccessChecker(clientFactoryFunc(func(username string, _ []string) (kubernetes.Interface, error) {
		return clients[username], nil
	}), slog.Default())

	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{sensitiveEntry("local", "uid-1", 7)}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader, ac)

	alice := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", &auth.User{KubernetesUsername: "alice"}))
	bob := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", &auth.User{KubernetesUsername: "bob"}))

	if alice.Data.Projection.Level != "full" {
		t.Errorf("alice projection = %q; want full", alice.Data.Projection.Level)
	}
	if bob.Data.Projection.Level != "outcome-only" {
		t.Errorf("bob projection = %q; want outcome-only (alice's cached grant must not leak)", bob.Data.Projection.Level)
	}
	if _, present := bob.Data.Entries[0]["message"]; present {
		t.Error("bob received a controller message")
	}
}

// clientFactoryFunc adapts a function to AccessChecker's client-factory
// interface.
type clientFactoryFunc func(username string, groups []string) (kubernetes.Interface, error)

func (f clientFactoryFunc) ClientForUser(username string, groups []string) (kubernetes.Interface, error) {
	return f(username, groups)
}

func TestHistory_RevokedSecretAccess_DropsToL1(t *testing.T) {
	// The predicate path bypasses accessCacheTTL, so this proves the level is
	// decided per request from the current grant. With the real cache a
	// revocation takes up to 60s to land; see the handler doc comment.
	var secretsAllowed atomic.Bool
	secretsAllowed.Store(true)
	ac := resources.NewPredicateAccessChecker(func(verb, group, resource, namespace string) bool {
		if group == "" && resource == "secrets" {
			return secretsAllowed.Load()
		}
		return true
	})
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{sensitiveEntry("local", "uid-1", 7)}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader, ac)

	if lvl := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", historyUser)).Data.Projection.Level; lvl != "full" {
		t.Fatalf("before revoke: level = %q; want full", lvl)
	}
	secretsAllowed.Store(false)
	after := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", historyUser))
	if after.Data.Projection.Level != "outcome-only" {
		t.Errorf("after revoke: level = %q; want outcome-only", after.Data.Projection.Level)
	}
	if _, present := after.Data.Entries[0]["diffKeysAdded"]; present {
		t.Error("after revoke: key names still returned")
	}
}

// A Secret check that errors must fail closed to L1, never open to L2.
func TestHistory_SecretCheckError_FailsClosedToL1(t *testing.T) {
	// An erroring checker also fails the ES pre-check (403), so the Secret
	// decision is asserted on the helper directly.
	h := historyHandler(nil, &fakeHistoryReader{}, resources.NewErroringAccessChecker(errors.New("apiserver down")))
	if h.canAccessCore(context.Background(), historyUser, "get", "secrets", "apps") {
		t.Error("canAccessCore returned true on a failed check")
	}
}

// --- Gates -------------------------------------------------------------------

func TestHistory_ForbiddenES_Returns403(t *testing.T) {
	reader := &fakeHistoryReader{}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysDenyAccessChecker())

	assertErrorReason(t, getHistory(t, h, "apps", "db-creds", "", historyUser), http.StatusForbidden, "")
	if len(reader.calls) != 0 {
		t.Error("history store queried for a caller denied the ExternalSecret")
	}
}

func TestHistory_HistoryStoreNil_Returns503Unavailable(t *testing.T) {
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, nil,
		resources.NewAlwaysAllowAccessChecker())
	if h.HistoryStore != nil {
		t.Fatal("fixture: HistoryStore must be a nil interface")
	}
	assertErrorReason(t, getHistory(t, h, "apps", "db-creds", "", historyUser),
		http.StatusServiceUnavailable, "history_unavailable")
}

func TestHistory_StoreError_Returns503NotEmpty200(t *testing.T) {
	reader := &fakeHistoryReader{err: errors.New("connection refused")}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysAllowAccessChecker())

	w := getHistory(t, h, "apps", "db-creds", "", historyUser)
	assertErrorReason(t, w, http.StatusServiceUnavailable, "history_unavailable")
	if strings.Contains(w.Body.String(), "connection refused") {
		t.Error("store error text reached the client")
	}
}

func TestHistory_EmptyHistory_Returns200EmptyArray(t *testing.T) {
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, &fakeHistoryReader{},
		resources.NewAlwaysAllowAccessChecker())
	b := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", historyUser))
	if len(b.Data.Entries) != 0 || b.Metadata.Total != 0 || b.Metadata.Continue != "" {
		t.Errorf("empty history = %+v / %+v", b.Data.Entries, b.Metadata)
	}
}

func TestHistory_ForgedCursor_Returns400(t *testing.T) {
	for _, cursor := range []string{"not-base64!!", "MToyOjM", "eDox"} { // junk, "1:2:3", "x:1"
		reader := &fakeHistoryReader{}
		h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
			resources.NewAlwaysAllowAccessChecker())
		assertErrorReason(t, getHistory(t, h, "apps", "db-creds", "cursor="+cursor, historyUser),
			http.StatusBadRequest, "invalid_cursor")
		if len(reader.calls) != 0 {
			t.Errorf("cursor %q: store queried despite a malformed cursor", cursor)
		}
	}
}

func TestHistory_InvalidLimit_Returns400(t *testing.T) {
	for _, limit := range []string{"abc", "1.5", "99999999999999999999"} {
		h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, &fakeHistoryReader{},
			resources.NewAlwaysAllowAccessChecker())
		assertErrorReason(t, getHistory(t, h, "apps", "db-creds", "limit="+limit, historyUser),
			http.StatusBadRequest, "invalid_limit")
	}
}

func TestHistory_CursorAndLimitReachStore(t *testing.T) {
	reader := &fakeHistoryReader{}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysAllowAccessChecker())
	want := store.ESOHistoryCursor{AttemptAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), ID: 42}

	decodeHistory(t, getHistory(t, h, "apps", "db-creds",
		"limit=10&cursor="+store.EncodeESOHistoryCursor(want), historyUser))

	call := reader.lastCall(t)
	if call.limit != 10 {
		t.Errorf("limit = %d; want 10", call.limit)
	}
	if call.after == nil || !call.after.AttemptAt.Equal(want.AttemptAt) || call.after.ID != want.ID {
		t.Errorf("after = %+v; want %+v", call.after, want)
	}
}

func TestHistory_NextCursorInMetadataContinue(t *testing.T) {
	reader := &pagedReader{page: store.ESOHistoryPage{
		Entries:    []store.ESOSyncHistoryEntry{sensitiveEntry("local", "uid-1", 7)},
		NextCursor: "NEXT",
	}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, nil,
		resources.NewAlwaysAllowAccessChecker())
	h.HistoryStore = reader

	b := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", historyUser))
	if b.Metadata.Continue != "NEXT" || b.Metadata.Total != 1 {
		t.Errorf("metadata = %+v; want continue=NEXT total=1", b.Metadata)
	}
}

type pagedReader struct{ page store.ESOHistoryPage }

func (p *pagedReader) QueryPage(context.Context, string, string, *store.ESOHistoryCursor, int) (store.ESOHistoryPage, error) {
	return p.page, nil
}

func TestHistory_CursorCannotCrossObject(t *testing.T) {
	// A cursor minted on object A and replayed on object B's URL moves only
	// B's window: the store is still asked for B's live UID.
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{
		sensitiveEntry("local", "uid-a", 1),
		sensitiveEntry("local", "uid-b", 2),
	}}
	h := historyHandler([]runtime.Object{
		makeES("apps", "es-a", "uid-a"),
		makeES("apps", "es-b", "uid-b"),
	}, reader, resources.NewAlwaysAllowAccessChecker())
	cursorFromA := store.EncodeESOHistoryCursor(store.ESOHistoryCursor{AttemptAt: time.Now().UTC(), ID: 1})

	getHistory(t, h, "apps", "es-b", "cursor="+cursorFromA, historyUser)
	if call := reader.lastCall(t); call.uid != "uid-b" {
		t.Errorf("store queried for uid %q; want uid-b", call.uid)
	}
}

func TestHistory_RemoteClusterID_Returns501(t *testing.T) {
	reader := &fakeHistoryReader{}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysAllowAccessChecker())
	r := historyRequest("apps", "db-creds", "", historyUser)
	r = r.WithContext(middleware.WithClusterID(r.Context(), "prod"))

	assertErrorReason(t, serveHistory(h, r), http.StatusNotImplemented, "remote_history_unsupported")
	if len(reader.calls) != 0 {
		t.Error("history store queried for a remote cluster")
	}
}

// The request context names the local cluster "local" regardless of the
// configured cluster id, while history rows carry the configured id. A
// configured id other than "local" must neither 501 a local request nor query
// the store under the wrong id.
func TestHistory_ConfiguredClusterID_LocalRequestQueriesConfiguredID(t *testing.T) {
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{sensitiveEntry("homelab", "uid-1", 7)}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysAllowAccessChecker())
	h.ClusterID = "homelab"

	for _, ctxID := range []string{"", "local", "homelab"} {
		r := historyRequest("apps", "db-creds", "", historyUser)
		if ctxID != "" {
			r = r.WithContext(middleware.WithClusterID(r.Context(), ctxID))
		}
		b := decodeHistory(t, serveHistory(h, r))
		if len(b.Data.Entries) != 1 || b.Data.ClusterID != "homelab" {
			t.Errorf("ctx %q: entries=%d clusterId=%q; want 1/homelab", ctxID, len(b.Data.Entries), b.Data.ClusterID)
		}
		if call := reader.lastCall(t); call.clusterID != "homelab" {
			t.Errorf("ctx %q: store queried for cluster %q; want homelab", ctxID, call.clusterID)
		}
	}
}

func TestHistory_UIDResolvedLive_NotFromRequest(t *testing.T) {
	reader := &fakeHistoryReader{}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-live")}, reader,
		resources.NewAlwaysAllowAccessChecker())

	decodeHistory(t, getHistory(t, h, "apps", "db-creds", "uid=uid-attacker", historyUser))
	if call := reader.lastCall(t); call.uid != "uid-live" {
		t.Errorf("store queried for uid %q; want the live uid-live", call.uid)
	}
}

func TestHistory_RecreatedES_DoesNotInheritOldRows(t *testing.T) {
	// Rows exist under the deleted object's UID; the live object with the
	// same name has a new UID and must start with an empty history.
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{sensitiveEntry("local", "uid-old", 7)}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-new")}, reader,
		resources.NewAlwaysAllowAccessChecker())

	b := decodeHistory(t, getHistory(t, h, "apps", "db-creds", "", historyUser))
	if len(b.Data.Entries) != 0 {
		t.Errorf("recreated ES inherited %d rows", len(b.Data.Entries))
	}
	if b.Data.UID != "uid-new" {
		t.Errorf("uid = %q; want uid-new", b.Data.UID)
	}
}

func TestHistory_ESNotFound_Returns404(t *testing.T) {
	reader := &fakeHistoryReader{}
	h := historyHandler(nil, reader, resources.NewAlwaysAllowAccessChecker())
	assertErrorReason(t, getHistory(t, h, "apps", "missing", "", historyUser), http.StatusNotFound, "")
	if len(reader.calls) != 0 {
		t.Error("history store queried for an ExternalSecret that does not exist")
	}
}

func TestHistory_ESONotDetected_Returns503(t *testing.T) {
	h := historyHandler(nil, &fakeHistoryReader{}, resources.NewAlwaysAllowAccessChecker())
	h.Discoverer = undetectedDiscoverer()
	assertErrorReason(t, getHistory(t, h, "apps", "db-creds", "", historyUser),
		http.StatusServiceUnavailable, "eso_not_detected")
}

func TestHistory_Unauthenticated_Returns401(t *testing.T) {
	h := historyHandler(nil, &fakeHistoryReader{}, resources.NewAlwaysAllowAccessChecker())
	w := getHistory(t, h, "apps", "db-creds", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

func TestHistory_ContextCancelled_NoPartialWrite(t *testing.T) {
	reader := &fakeHistoryReader{rows: []store.ESOSyncHistoryEntry{sensitiveEntry("local", "uid-1", 7)}}
	h := historyHandler([]runtime.Object{makeES("apps", "db-creds", "uid-1")}, reader,
		resources.NewAlwaysAllowAccessChecker())
	r := historyRequest("apps", "db-creds", "", historyUser)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)

	w := serveHistory(h, r)
	// Whatever gate observes the cancellation, the client gets exactly one
	// well-formed error envelope and never a page.
	if w.Code == http.StatusOK {
		t.Fatalf("cancelled request returned 200: %s", w.Body.String())
	}
	assertErrorReason(t, w, w.Code, "")
	if strings.Contains(w.Body.String(), "PROVIDER_PATH_MARKER") {
		t.Error("cancelled request leaked row content")
	}
}

// --- Pure helpers ------------------------------------------------------------

func TestProjectReason_UnknownTokenBecomesUnknown(t *testing.T) {
	for _, in := range []string{"", "NotAReason", "secretsynced", "SecretSynced ", "\x1b]0;pwn\x07", "SecretSynced\n"} {
		if got := projectReason(in, projectionOutcomeOnly); got != "Unknown" {
			t.Errorf("projectReason(%q, L1) = %q; want Unknown", in, got)
		}
	}
	for r := range knownESOReasons {
		if got := projectReason(r, projectionOutcomeOnly); got != r {
			t.Errorf("projectReason(%q, L1) = %q; allowlisted token must pass", r, got)
		}
	}
	if got := projectReason("CustomProviderReason", projectionFull); got != "CustomProviderReason" {
		t.Errorf("L2 reason = %q; want passthrough", got)
	}
}

func TestSanitizeControllerText_StripsTerminalEscapes(t *testing.T) {
	got, truncated := sanitizeControllerText("ok\x1b[31mred\x1b]0;title\x07 \u009b2J end\r\n", 2048)
	if strings.ContainsAny(got, "\x1b\x07\r\u009b") {
		t.Errorf("control characters survived: %q", got)
	}
	if truncated {
		t.Error("short input reported truncated")
	}
	if !strings.HasPrefix(got, "ok") || !strings.Contains(got, "end") {
		t.Errorf("printable text lost: %q", got)
	}
}

func TestSanitizeControllerText_KeepsTabAndCollapsesNewlines(t *testing.T) {
	got, _ := sanitizeControllerText("a\tb\n\n\n\n\x00\nc", 2048)
	if got != "a\tb\n\nc" {
		t.Errorf("got %q; want %q", got, "a\tb\n\nc")
	}
}

func TestSanitizeControllerText_InvalidUTF8(t *testing.T) {
	got, _ := sanitizeControllerText("bad\xff\xfebytes", 2048)
	if !utf8.ValidString(got) || !strings.Contains(got, "�") {
		t.Errorf("got %q; want valid UTF-8 with U+FFFD", got)
	}
}

func TestSanitizeControllerText_TruncatesOnRuneBoundary(t *testing.T) {
	in := strings.Repeat("é", 100) // 2 bytes per rune
	for _, max := range []int{10, 11, 12, 13} {
		got, truncated := sanitizeControllerText(in, max)
		if !truncated {
			t.Errorf("max %d: truncated = false", max)
		}
		if len(got) > max {
			t.Errorf("max %d: len = %d exceeds the bound", max, len(got))
		}
		if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
			t.Errorf("max %d: got %q; want valid UTF-8 ending in an ellipsis", max, got)
		}
	}
	if got, truncated := sanitizeControllerText("exactly", 7); truncated || got != "exactly" {
		t.Errorf("at-limit input: got %q truncated=%v", got, truncated)
	}
}

func TestSanitizeControllerText_DoesNotHTMLEscape(t *testing.T) {
	got, _ := sanitizeControllerText("<script>alert(1)</script>", 2048)
	if got != "<script>alert(1)</script>" {
		t.Errorf("got %q; the renderer escapes, the sanitizer must not", got)
	}
}
