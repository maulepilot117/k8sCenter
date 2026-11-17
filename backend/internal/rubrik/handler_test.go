package rubrik

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

const (
	testTenant = "https://acme.my.rubrik.com"
	testSecret = "stored-secret-value"
)

// adminStore adds the handler's writes to the service's fakeStore, so the
// service resolves what the handler saved.
type adminStore struct {
	*fakeStore
	getErr     error // GetConnection's error, e.g. an undecryptable secret
	upsertErr  error
	setErr     error
	deleteErr  error
	upserts    int
	setMapping []store.RubrikMappingWrite
}

func (s *adminStore) GetConnection(ctx context.Context, id string) (store.RubrikConnection, store.RubrikCredentials, error) {
	s.mu.Lock()
	err := s.getErr
	s.mu.Unlock()
	if err != nil {
		return store.RubrikConnection{}, store.RubrikCredentials{}, err
	}
	return s.fakeStore.GetConnection(ctx, id)
}

func (s *adminStore) UpsertConnection(_ context.Context, _ string, w store.RubrikConnectionWrite) (store.RubrikConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts++
	if s.upsertErr != nil {
		return store.RubrikConnection{}, s.upsertErr
	}
	newOrigin := urlorigin.Of(w.TenantURL)
	exists := s.revision != 0
	sameOrigin := exists && urlorigin.Of(s.conn.TenantURL) == newOrigin
	if w.ClientSecret == nil {
		if !sameOrigin || !s.conn.HasSecret {
			return store.RubrikConnection{}, store.ErrRubrikSecretRequired
		}
		if s.conn.TenantURL != w.KeptFrom {
			return store.RubrikConnection{}, store.ErrRubrikConnectionChanged
		}
	} else {
		s.secret = *w.ClientSecret
	}
	if exists && !sameOrigin {
		clear(s.mappings)
	}
	s.revision++
	s.conn = store.RubrikConnection{
		ID: testConn, TenantURL: w.TenantURL, ClientID: w.ClientID, HasSecret: true,
		Revision: s.revision, UpdatedBy: w.UpdatedBy, UpdatedAt: time.Now(),
	}
	return s.conn, nil
}

func (s *adminStore) SetMapping(_ context.Context, w store.RubrikMappingWrite) (store.RubrikClusterMapping, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setMapping = append(s.setMapping, w)
	if s.setErr != nil {
		return store.RubrikClusterMapping{}, s.setErr
	}
	if s.revision == 0 {
		return store.RubrikClusterMapping{}, store.ErrRubrikConnectionNotFound
	}
	if s.revision != w.ConnectionRevision {
		return store.RubrikClusterMapping{}, store.ErrRubrikConnectionChanged
	}
	for id, m := range s.mappings {
		if id != w.ClusterID && strings.EqualFold(m.RSCClusterID, w.RSCClusterID) {
			return store.RubrikClusterMapping{}, store.ErrRubrikMappingConflict
		}
	}
	m := store.RubrikClusterMapping{
		ClusterID: w.ClusterID, ConnectionID: w.ConnectionID, RSCClusterID: w.RSCClusterID,
		RSCClusterName: w.RSCClusterName, ClusterGeneration: w.ClusterGeneration,
		UpdatedBy: w.UpdatedBy, UpdatedAt: time.Now(),
	}
	s.mappings[w.ClusterID] = m
	return m, nil
}

func (s *adminStore) ListMappings(_ context.Context, _ string) ([]store.RubrikClusterMapping, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.RubrikClusterMapping, 0, len(s.mappings))
	for _, m := range s.mappings {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClusterID < out[j].ClusterID })
	return out, nil
}

func (s *adminStore) DeleteMapping(_ context.Context, clusterID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.mappings[clusterID]; !ok {
		return store.ErrRubrikMappingNotFound
	}
	delete(s.mappings, clusterID)
	return nil
}

// fakeAudit records audit entries.
type fakeAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (f *fakeAudit) Log(_ context.Context, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeAudit) all() []audit.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.entries)
}

type flushCounter struct {
	mu sync.Mutex
	n  int
}

func (f *flushCounter) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
}

func (f *flushCounter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

type probeCall struct{ url, clientID, secret string }

// handlerFixture is a handler over the service fixture's fakes: local is
// mapped to the RSC-native cluster idCluster, remote-1 to the CDM cluster
// idSet2, and the stored connection is testTenant at revision 7.
type handlerFixture struct {
	h        *Handler
	svc      *Service
	rsc      *fakeReader
	st       *adminStore
	clusters fakeClusters
	audit    *fakeAudit
	flush    *flushCounter

	mu       sync.Mutex
	probes   []probeCall
	probeErr error
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	fx := &handlerFixture{rsc: newFakeReader(), audit: &fakeAudit{}, flush: &flushCounter{}}
	fx.rsc.clusters = []Cluster{
		{Family: FamilyNamespace, ID: idCluster, Name: "native"},
		{Family: FamilyProtectionSet, ID: idSet2, Name: "cdm"},
	}
	fx.rsc.objects[idCluster] = []Object{objNS(idNS, "team-a", idSLA, nil)}
	fx.rsc.objects[idSet2] = []Object{objSet(idSet, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil)}
	fx.st = &adminStore{fakeStore: &fakeStore{
		revision: 7,
		conn:     store.RubrikConnection{ID: testConn, TenantURL: testTenant, ClientID: "client-1", HasSecret: true},
		secret:   testSecret,
		mappings: map[string]store.RubrikClusterMapping{
			"local":    {ClusterID: "local", ConnectionID: testConn, RSCClusterID: idCluster, RSCClusterName: "native", ClusterGeneration: "local"},
			"remote-1": {ClusterID: "remote-1", ConnectionID: testConn, RSCClusterID: idSet2, RSCClusterName: "cdm", ClusterGeneration: registered.Format(time.RFC3339Nano)},
		},
	}}
	fx.clusters = fakeClusters{"remote-1": registered, "remote-2": registered.Add(time.Hour)}
	fx.svc = NewService(fx.rsc, fx.st, fx.clusters, testConn, nil, fx.flush.Flush)
	access := NewAccess(newFakeChecker("get protections team-a"), lister(t, map[string]time.Time{"team-a": created}), true)
	h, err := NewHandler(HandlerDeps{
		Store:        fx.st,
		Clusters:     fx.clusters,
		Service:      fx.svc,
		Access:       access,
		RSC:          fx.rsc,
		Client:       fx.flush,
		Audit:        fx.audit,
		ConnectionID: testConn,
		Probe: func(_ context.Context, u, id, secret string) error {
			fx.mu.Lock()
			defer fx.mu.Unlock()
			fx.probes = append(fx.probes, probeCall{u, id, secret})
			return fx.probeErr
		},
		ValidateURL: func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	fx.h = h
	return fx
}

func (fx *handlerFixture) probeCalls() []probeCall {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return slices.Clone(fx.probes)
}

// do sends a request through the production route table as user, with the
// selected cluster set the way the ClusterContext middleware sets it.
func (fx *handlerFixture) do(t *testing.T, user *auth.User, clusterID, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, fx.h, user, clusterID, method, path, body)
}

func serve(t *testing.T, h *Handler, user *auth.User, clusterID, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return serveWith(t, h, nil, user, clusterID, method, path, body)
}

// serveWith is serve with the write rate limiter Mount receives.
func serveWith(t *testing.T, h *Handler, writeLimit func(http.Handler) http.Handler, user *auth.User, clusterID, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if user != nil {
				ctx = auth.ContextWithUser(ctx, user)
			}
			ctx = middleware.WithClusterID(ctx, clusterID)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	Mount(r, h, writeLimit)

	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func responseData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return env.Data
}

func errorReason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return env.Error.Reason
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
}

func strptr(s string) *string { return &s }

// ---------------------------------------------------------------------------

// With the provider off, main.go passes no handler and the server mounts
// nothing: every /rubrik path is a 404 (R28).
func TestProviderOffHasNoRoutes(t *testing.T) {
	for _, path := range []string{"/rubrik/status", "/rubrik/mappings", "/rubrik/connection"} {
		rec := serve(t, nil, admin, "local", http.MethodGet, path, nil)
		wantStatus(t, rec, http.StatusNotFound)
	}
}

// The route table is exactly the Rubrik surface so far (settings, reads,
// the U7 writes and the U8 restore), and every admin endpoint refuses a non-admin before
// reaching the store or RSC.
func TestAdminEndpointsRefuseNonAdmins(t *testing.T) {
	fx := newHandlerFixture(t)

	got := map[string]bool{}
	r := chi.NewRouter()
	Mount(r, fx.h, nil)
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"GET /rubrik/status":                                 true,
		"GET /rubrik/namespaces":                             true,
		"GET /rubrik/namespaces/{namespace}":                 true,
		"GET /rubrik/sla-domains":                            true,
		"GET /rubrik/operations/{id}":                        true,
		"POST /rubrik/namespaces/{namespace}/snapshots":      true,
		"PUT /rubrik/namespaces/{namespace}/sla":             true,
		"GET /rubrik/namespaces/{namespace}/restore-targets": true,
		"POST /rubrik/restores":                              true,
		"GET /rubrik/rsc-clusters":                           true,
		"GET /rubrik/mappings":                               true,
		"PUT /rubrik/connection":                             true,
		"PUT /rubrik/mappings/{clusterID}":                   true,
		"DELETE /rubrik/mappings/{clusterID}":                true,
	}
	for k := range want {
		if !got[k] {
			t.Errorf("route %q not registered", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected route %q", k)
		}
	}

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/rubrik/rsc-clusters"},
		{http.MethodGet, "/rubrik/mappings"},
		{http.MethodPut, "/rubrik/connection"},
		{http.MethodPut, "/rubrik/mappings/local"},
		{http.MethodDelete, "/rubrik/mappings/local"},
	} {
		rec := fx.do(t, owner, "local", c.method, c.path, map[string]any{"tenantUrl": testTenant, "clientId": "x", "clientSecret": "y"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if fx.rsc.count("Clusters") != 0 || fx.st.upserts != 0 || len(fx.st.setMapping) != 0 || len(fx.probeCalls()) != 0 {
		t.Error("a refused request reached RSC or the store")
	}
	if n := len(fx.audit.all()); n != 0 {
		t.Errorf("refused requests wrote %d audit entries", n)
	}
	if rec := fx.do(t, nil, "local", http.MethodGet, "/rubrik/status", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("status without a user = %d, want 401", rec.Code)
	}
}

// The write rate limiter wraps every write, admin and Rubrik, and none of
// the reads, and an exhausted limiter refuses a write before it reaches the
// store or RSC.
func TestWriteLimitWrapsOnlyTheWrites(t *testing.T) {
	fx := newHandlerFixture(t)
	var mu sync.Mutex
	var limited []string
	exhausted := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			limited = append(limited, r.Method+" "+r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusTooManyRequests)
		})
	}
	connection := map[string]any{"tenantUrl": testTenant, "clientId": "client-1", "clientSecret": "s2"}
	mapping := map[string]any{"rscClusterId": idCluster, "connectionRevision": 7}
	for _, c := range []struct {
		method, path string
		body         any
		write        bool
	}{
		{http.MethodGet, "/rubrik/status", nil, false},
		{http.MethodGet, "/rubrik/namespaces", nil, false},
		{http.MethodGet, "/rubrik/namespaces/team-a", nil, false},
		{http.MethodGet, "/rubrik/sla-domains", nil, false},
		{http.MethodGet, "/rubrik/operations/" + testOpID, nil, false},
		{http.MethodPost, "/rubrik/namespaces/team-a/snapshots", map[string]any{"operationId": testOpID}, true},
		{http.MethodPut, "/rubrik/namespaces/team-a/sla", map[string]any{"operationId": testOpID, "slaId": idSLA}, true},
		{http.MethodGet, "/rubrik/namespaces/team-a/restore-targets?objectId=" + idNS, nil, false},
		{http.MethodPost, "/rubrik/restores", map[string]any{"operationId": testOpID}, true},
		{http.MethodGet, "/rubrik/rsc-clusters", nil, false},
		{http.MethodGet, "/rubrik/mappings", nil, false},
		{http.MethodPut, "/rubrik/connection", connection, true},
		{http.MethodPut, "/rubrik/mappings/local", mapping, true},
		{http.MethodDelete, "/rubrik/mappings/local", nil, true},
	} {
		rec := serveWith(t, fx.h, exhausted, admin, "local", c.method, c.path, c.body)
		if limitedNow := rec.Code == http.StatusTooManyRequests; limitedNow != c.write {
			t.Errorf("%s %s = %d; rate limited %v, want %v", c.method, c.path, rec.Code, limitedNow, c.write)
		}
	}
	want := []string{
		"POST /rubrik/namespaces/team-a/snapshots", "PUT /rubrik/namespaces/team-a/sla", "POST /rubrik/restores",
		"PUT /rubrik/connection", "PUT /rubrik/mappings/local", "DELETE /rubrik/mappings/local",
	}
	if !slices.Equal(limited, want) {
		t.Errorf("limiter saw %v, want %v", limited, want)
	}
	if fx.st.upserts != 0 || len(fx.st.setMapping) != 0 || len(fx.probeCalls()) != 0 || len(fx.audit.all()) != 0 {
		t.Error("a rate-limited write reached the store, RSC or the audit log")
	}
	if list, _ := fx.st.ListMappings(context.Background(), testConn); len(list) != 2 {
		t.Errorf("a rate-limited delete removed a mapping: %+v", list)
	}
}

func TestStatusForNonAdminOmitsConnection(t *testing.T) {
	fx := newHandlerFixture(t)

	rec := fx.do(t, owner, "local", http.MethodGet, "/rubrik/status", nil)
	wantStatus(t, rec, http.StatusOK)
	for _, leak := range []string{"connection", "tenantUrl", "clientId", "rscClusterId", "acme", idCluster} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("non-admin status contains %q: %s", leak, rec.Body.String())
		}
	}
	st := responseData[StatusView](t, rec)
	if !st.Enabled || !st.Preview || !st.WritesEnabled {
		t.Errorf("settings = %+v, want enabled, preview and writes", st)
	}
	if st.Cluster.State != StateMapped || st.Cluster.Mapping != nil {
		t.Errorf("cluster = %+v, want mapped without mapping detail", st.Cluster)
	}

	// Every "not set up" reason looks the same to a non-admin (R22).
	fx.st.mu.Lock()
	delete(fx.st.mappings, "local")
	fx.st.mu.Unlock()
	if got := responseData[StatusView](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/status", nil)); got.Cluster.State != StateNotSetUp {
		t.Errorf("unmapped cluster state = %q, want %q", got.Cluster.State, StateNotSetUp)
	}
	fx.st.setRevision(0)
	if got := responseData[StatusView](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/status", nil)); got.Cluster.State != StateNotSetUp {
		t.Errorf("unconfigured state = %q, want %q", got.Cluster.State, StateNotSetUp)
	}

	// Remote clusters are admin-only (R10).
	if got := responseData[StatusView](t, fx.do(t, owner, "remote-1", http.MethodGet, "/rubrik/status", nil)); got.Cluster.State != StateAdminOnly || got.Connection != nil {
		t.Errorf("remote status for non-admin = %+v, want admin_only without connection", got)
	}
}

func TestStatusForAdminIncludesConnection(t *testing.T) {
	fx := newHandlerFixture(t)
	success := registered.Add(time.Minute)
	fx.st.mu.Lock()
	fx.st.conn.LastSuccessAt = &success
	fx.st.mu.Unlock()

	rec := fx.do(t, admin, "local", http.MethodGet, "/rubrik/status", nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), testSecret) {
		t.Fatalf("status leaks the client secret: %s", rec.Body.String())
	}
	st := responseData[StatusView](t, rec)
	c := st.Connection
	if c == nil || !c.Configured || c.TenantURL != testTenant || c.ClientID != "client-1" || !c.HasSecret || c.Revision != 7 {
		t.Fatalf("connection = %+v", c)
	}
	if c.Health != HealthOK || c.LastSuccessAt == nil || !c.LastSuccessAt.Equal(success) {
		t.Errorf("health = %q at %v, want ok at %v", c.Health, c.LastSuccessAt, success)
	}
	if st.Cluster.State != StateMapped || st.Cluster.Mapping == nil || st.Cluster.Mapping.RSCClusterID != idCluster {
		t.Errorf("cluster = %+v, want mapped to %s", st.Cluster, idCluster)
	}

	fx.st.setRevision(0)
	st = responseData[StatusView](t, fx.do(t, admin, "local", http.MethodGet, "/rubrik/status", nil))
	if st.Cluster.State != StateNotConfigured || st.Connection == nil || st.Connection.Configured || st.Connection.Health != HealthNotConfigured {
		t.Errorf("unconfigured admin status = %+v / %+v", st.Cluster, st.Connection)
	}
}

func TestConnectionHealth(t *testing.T) {
	early, late := registered, registered.Add(time.Hour)
	for _, c := range []struct {
		name string
		conn store.RubrikConnection
		want string
	}{
		{"no secret", store.RubrikConnection{}, HealthSecretRequired},
		{"never contacted", store.RubrikConnection{HasSecret: true}, HealthUnknown},
		{"success", store.RubrikConnection{HasSecret: true, LastSuccessAt: &late}, HealthOK},
		{"success after failure", store.RubrikConnection{HasSecret: true, LastSuccessAt: &late, LastFailureAt: &early, LastFailureKind: HealthAuthFailed}, HealthOK},
		{"auth failure last", store.RubrikConnection{HasSecret: true, LastSuccessAt: &early, LastFailureAt: &late, LastFailureKind: HealthAuthFailed}, HealthAuthFailed},
		{"unreachable", store.RubrikConnection{HasSecret: true, LastFailureAt: &late, LastFailureKind: HealthUnreachable}, HealthUnreachable},
		{"other failure", store.RubrikConnection{HasSecret: true, LastFailureAt: &late, LastFailureKind: "rate_limited"}, HealthFailing},
	} {
		if got := connectionHealth(c.conn); got != c.want {
			t.Errorf("%s: health = %q, want %q", c.name, got, c.want)
		}
	}
}

// Creating a mapping stores the cluster's current registration generation;
// registering the cluster again makes status report the mapping invalid.
func TestMappingStoresGenerationAndReRegistrationInvalidatesIt(t *testing.T) {
	fx := newHandlerFixture(t)

	rec := fx.do(t, admin, "remote-2", http.MethodPut, "/rubrik/mappings/remote-2",
		map[string]any{"rscClusterId": strings.ToUpper(idCluster), "connectionRevision": 7})
	// idCluster is mapped to local already.
	wantStatus(t, rec, http.StatusConflict)
	if errorReason(t, rec) != "rsc_cluster_already_mapped" {
		t.Errorf("reason = %q", errorReason(t, rec))
	}

	fx.st.mu.Lock()
	delete(fx.st.mappings, "local")
	fx.st.mu.Unlock()
	rec = fx.do(t, admin, "remote-2", http.MethodPut, "/rubrik/mappings/remote-2",
		map[string]any{"rscClusterId": strings.ToUpper(idCluster), "connectionRevision": 7})
	wantStatus(t, rec, http.StatusOK)
	got := responseData[MappingView](t, rec)
	if got.RSCClusterID != idCluster || got.RSCClusterName != "native" || got.State != StateMapped {
		t.Errorf("mapping = %+v; want RSC's id and name, not the request's", got)
	}
	w := fx.st.setMapping[len(fx.st.setMapping)-1]
	if want := registered.Add(time.Hour).Format(time.RFC3339Nano); w.ClusterGeneration != want {
		t.Errorf("stored generation = %q, want %q", w.ClusterGeneration, want)
	}
	if w.UpdatedBy != admin.Username || w.ConnectionRevision != 7 {
		t.Errorf("write = %+v", w)
	}

	st := responseData[StatusView](t, fx.do(t, admin, "remote-2", http.MethodGet, "/rubrik/status", nil))
	if st.Cluster.State != StateMapped {
		t.Fatalf("state after mapping = %q, want mapped", st.Cluster.State)
	}

	// Delete and register the cluster again under the same id.
	fx.clusters["remote-2"] = registered.Add(2 * time.Hour)
	st = responseData[StatusView](t, fx.do(t, admin, "remote-2", http.MethodGet, "/rubrik/status", nil))
	if st.Cluster.State != StateMappingInvalid || st.Cluster.Mapping == nil || st.Cluster.Mapping.State != StateMappingInvalid {
		t.Errorf("status after re-registration = %+v, want mapping_invalid with its detail", st.Cluster)
	}
	list := responseData[[]MappingView](t, fx.do(t, admin, "local", http.MethodGet, "/rubrik/mappings", nil))
	states := map[string]string{}
	for _, m := range list {
		states[m.ClusterID] = m.State
	}
	if states["remote-2"] != StateMappingInvalid || states["remote-1"] != StateMapped {
		t.Errorf("mapping list states = %v", states)
	}

	// The local cluster's generation is the literal "local".
	rec = fx.do(t, admin, "local", http.MethodPut, "/rubrik/mappings/local",
		map[string]any{"rscClusterId": idSet2Free(t, fx), "connectionRevision": 7})
	wantStatus(t, rec, http.StatusOK)
	if w := fx.st.setMapping[len(fx.st.setMapping)-1]; w.ClusterGeneration != "local" {
		t.Errorf("local generation = %q, want local", w.ClusterGeneration)
	}
	for _, e := range fx.audit.all() {
		if e.Action == audit.ActionRubrikMappingUpdate && e.Result == audit.ResultSuccess && e.ResourceKind != auditKindMapping {
			t.Errorf("mapping audit kind = %q", e.ResourceKind)
		}
	}
}

// idSet2Free frees idSet2 (mapped to remote-1) and returns it.
func idSet2Free(t *testing.T, fx *handlerFixture) string {
	t.Helper()
	wantStatus(t, fx.do(t, admin, "local", http.MethodDelete, "/rubrik/mappings/remote-1", nil), http.StatusNoContent)
	return idSet2
}

func TestMappingRefusals(t *testing.T) {
	for _, c := range []struct {
		name    string
		cluster string
		body    any
		setup   func(fx *handlerFixture)
		status  int
		reason  string
	}{
		{"stale revision", "remote-2", map[string]any{"rscClusterId": idSet2, "connectionRevision": 6}, func(fx *handlerFixture) {
			fx.st.mu.Lock()
			delete(fx.st.mappings, "remote-1")
			fx.st.mu.Unlock()
		}, http.StatusConflict, "connection_changed"},
		{"unknown rsc cluster", "remote-2", map[string]any{"rscClusterId": "00000000-0000-4000-8000-00000000abcd", "connectionRevision": 7}, nil, http.StatusNotFound, "rsc_cluster_not_found"},
		{"ambiguous rsc cluster", "remote-2", map[string]any{"rscClusterId": idCluster, "connectionRevision": 7}, func(fx *handlerFixture) {
			fx.rsc.set(func(f *fakeReader) {
				f.clusters = append(f.clusters, Cluster{Family: FamilyProtectionSet, ID: idCluster, Name: "dup"})
			})
		}, http.StatusConflict, "rsc_cluster_ambiguous"},
		{"rsc unreachable", "remote-2", map[string]any{"rscClusterId": idSet2, "connectionRevision": 7}, func(fx *handlerFixture) {
			fx.rsc.set(func(f *fakeReader) { f.err = ErrUnreachable })
		}, http.StatusBadGateway, "unreachable"},
		{"not configured", "remote-2", map[string]any{"rscClusterId": idSet2, "connectionRevision": 7}, func(fx *handlerFixture) {
			fx.st.mu.Lock()
			delete(fx.st.mappings, "remote-1")
			fx.st.mu.Unlock()
			fx.st.setRevision(0)
		}, http.StatusConflict, "not_configured"},
		{"store down", "remote-2", map[string]any{"rscClusterId": idSet2, "connectionRevision": 7}, func(fx *handlerFixture) {
			fx.st.setErr = errors.New("pool closed")
		}, http.StatusServiceUnavailable, "store_unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newHandlerFixture(t)
			if c.setup != nil {
				c.setup(fx)
			}
			rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/mappings/"+c.cluster, c.body)
			wantStatus(t, rec, c.status)
			if got := errorReason(t, rec); got != c.reason {
				t.Errorf("reason = %q, want %q", got, c.reason)
			}
		})
	}

	fx := newHandlerFixture(t)
	for _, c := range []struct {
		path   string
		body   any
		status int
	}{
		{"/rubrik/mappings/unregistered", map[string]any{"rscClusterId": idSet2, "connectionRevision": 7}, http.StatusNotFound},
		{"/rubrik/mappings/remote-2", map[string]any{"rscClusterId": "not-a-uuid", "connectionRevision": 7}, http.StatusBadRequest},
		{"/rubrik/mappings/remote-2", map[string]any{"rscClusterId": idSet2}, http.StatusBadRequest},
		{"/rubrik/mappings/bad%20id", map[string]any{"rscClusterId": idSet2, "connectionRevision": 7}, http.StatusBadRequest},
		{"/rubrik/mappings/remote-2", "{not json", http.StatusBadRequest},
	} {
		if rec := fx.do(t, admin, "local", http.MethodPut, c.path, c.body); rec.Code != c.status {
			t.Errorf("PUT %s %v = %d, want %d; %s", c.path, c.body, rec.Code, c.status, rec.Body.String())
		}
	}
	if fx.rsc.count("Clusters") != 0 {
		t.Error("a malformed or unregistered mapping request reached RSC")
	}
}

func TestDeleteMapping(t *testing.T) {
	fx := newHandlerFixture(t)
	wantStatus(t, fx.do(t, admin, "local", http.MethodDelete, "/rubrik/mappings/remote-1", nil), http.StatusNoContent)
	if _, err := fx.st.GetMapping(context.Background(), "remote-1"); !errors.Is(err, store.ErrRubrikMappingNotFound) {
		t.Errorf("mapping still stored: %v", err)
	}
	rec := fx.do(t, admin, "local", http.MethodDelete, "/rubrik/mappings/remote-1", nil)
	wantStatus(t, rec, http.StatusNotFound)

	var deletes []audit.Entry
	for _, e := range fx.audit.all() {
		if e.Action == audit.ActionRubrikMappingDelete {
			deletes = append(deletes, e)
		}
	}
	if len(deletes) != 1 || deletes[0].Result != audit.ResultSuccess || deletes[0].ClusterID != "remote-1" || deletes[0].User != admin.Username {
		t.Errorf("delete audit = %+v, want one success for remote-1", deletes)
	}
}

func TestListRSCClustersMarksMappedOnes(t *testing.T) {
	fx := newHandlerFixture(t)
	rec := fx.do(t, admin, "local", http.MethodGet, "/rubrik/rsc-clusters", nil)
	wantStatus(t, rec, http.StatusOK)
	got := responseData[[]RSCClusterView](t, rec)
	mapped := map[string]string{}
	for _, c := range got {
		mapped[c.ID] = c.MappedTo
	}
	if mapped[idCluster] != "local" || mapped[idSet2] != "remote-1" || len(got) != 2 {
		t.Errorf("rsc clusters = %+v", got)
	}

	fx.rsc.set(func(f *fakeReader) { f.err = ErrAuthFailed })
	rec = fx.do(t, admin, "local", http.MethodGet, "/rubrik/rsc-clusters", nil)
	wantStatus(t, rec, http.StatusBadRequest)
	if errorReason(t, rec) != HealthAuthFailed {
		t.Errorf("reason = %q", errorReason(t, rec))
	}
}

// ---------------------------------------------------------------------------
// Connection

// A tenant URL on a private address is refused before any request leaves,
// and nothing is stored (R4). This uses the production address check.
func TestPutConnectionRefusesPrivateAddress(t *testing.T) {
	fx := newHandlerFixture(t)
	fx.h.validateURL = defaultValidateURL(t)

	for _, u := range []string{"https://10.0.0.5", "https://127.0.0.1", "https://169.254.169.254", "https://[::1]"} {
		rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
			map[string]any{"tenantUrl": u, "clientId": "client-1", "clientSecret": "new-secret"})
		wantStatus(t, rec, http.StatusBadRequest)
		if errorReason(t, rec) != "invalid_tenant_url" {
			t.Errorf("%s: reason = %q", u, errorReason(t, rec))
		}
	}
	if len(fx.probeCalls()) != 0 || fx.st.upserts != 0 {
		t.Errorf("a private address was probed (%d) or stored (%d)", len(fx.probeCalls()), fx.st.upserts)
	}
}

// A tenant name that cannot be resolved is reported as unreachable, the way
// the client classifies it, not as a private address; nothing is probed or
// stored either way.
func TestPutConnectionLookupFailureIsUnreachable(t *testing.T) {
	for name, lookupErr := range map[string]error{
		"nxdomain": &net.DNSError{Err: "no such host", Name: "acme.my.rubrik.com", IsNotFound: true},
		"timeout":  context.DeadlineExceeded,
		"canceled": context.Canceled,
	} {
		fx := newHandlerFixture(t)
		fx.h.validateURL = func(context.Context, string) error {
			return fmt.Errorf("DNS resolution failed for acme.my.rubrik.com: %w", lookupErr)
		}
		rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
			map[string]any{"tenantUrl": testTenant, "clientId": "client-1", "clientSecret": "new-secret"})
		wantStatus(t, rec, http.StatusBadGateway)
		if got := errorReason(t, rec); got != HealthUnreachable {
			t.Errorf("%s: reason = %q, want %q", name, got, HealthUnreachable)
		}
		if strings.Contains(rec.Body.String(), "private") {
			t.Errorf("%s: a lookup failure was reported as a private address: %s", name, rec.Body.String())
		}
		if len(fx.probeCalls()) != 0 || fx.st.upserts != 0 {
			t.Errorf("%s: an unresolved address was probed or stored", name)
		}
	}
}

func defaultValidateURL(t *testing.T) func(context.Context, string) error {
	t.Helper()
	h, err := NewHandler(HandlerDeps{
		Store: &adminStore{fakeStore: &fakeStore{}}, Clusters: fakeClusters{}, Service: &Service{},
		Access: &Access{}, RSC: newFakeReader(), Client: &flushCounter{}, Audit: &fakeAudit{}, ConnectionID: testConn,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h.validateURL
}

func TestPutConnectionValidatesTheRequest(t *testing.T) {
	fx := newHandlerFixture(t)
	for _, body := range []any{
		map[string]any{"clientId": "c", "clientSecret": "s"},
		map[string]any{"tenantUrl": testTenant, "clientSecret": "s"},
		map[string]any{"tenantUrl": "http://acme.my.rubrik.com", "clientId": "c", "clientSecret": "s"},
		map[string]any{"tenantUrl": "https://user:pw@acme.my.rubrik.com", "clientId": "c", "clientSecret": "s"},
		map[string]any{"tenantUrl": testTenant + "/api", "clientId": "c", "clientSecret": "s"},
		map[string]any{"tenantUrl": testTenant + "?x=1", "clientId": "c", "clientSecret": "s"},
		map[string]any{"tenantUrl": testTenant, "clientId": "c", "clientSecret": ""},
		map[string]any{"tenantUrl": testTenant, "clientId": strings.Repeat("c", maxClientIDBytes+1), "clientSecret": "s"},
		"[]",
	} {
		rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%v = %d, want 400", body, rec.Code)
		}
	}
	if len(fx.probeCalls()) != 0 || fx.st.upserts != 0 {
		t.Error("a malformed request was probed or stored")
	}
}

// A failed probe returns its classified error and leaves the stored row as
// it was; the attempt is audited without the URL or the secret.
func TestPutConnectionProbeFailureKeepsTheRow(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		reason string
	}{
		{ErrAuthFailed, http.StatusBadRequest, HealthAuthFailed},
		{ErrUnreachable, http.StatusBadGateway, HealthUnreachable},
		{ErrInvalidTenantURL, http.StatusBadRequest, "invalid_tenant_url"},
		{&RateLimitError{RetryAfter: time.Second}, http.StatusBadGateway, "rate_limited"},
	} {
		fx := newHandlerFixture(t)
		fx.probeErr = c.err
		rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
			map[string]any{"tenantUrl": "https://other.my.rubrik.com", "clientId": "client-2", "clientSecret": "brand-new-secret"})
		wantStatus(t, rec, c.status)
		if got := errorReason(t, rec); got != c.reason {
			t.Errorf("%v: reason = %q, want %q", c.err, got, c.reason)
		}
		if fx.st.upserts != 0 || fx.st.revision != 7 || fx.st.conn.TenantURL != testTenant || fx.st.secret != testSecret {
			t.Errorf("%v: stored connection changed after a failed probe", c.err)
		}
		if fx.flush.count() != 0 {
			t.Errorf("%v: token flushed after a failed probe", c.err)
		}
		entries := fx.audit.all()
		if len(entries) != 1 || entries[0].Result != audit.ResultFailure || entries[0].Action != audit.ActionRubrikConnectionUpdate {
			t.Fatalf("%v: audit = %+v, want one failure", c.err, entries)
		}
		assertNoConnectionDetail(t, entries[0], "other.my.rubrik.com", "brand-new-secret")
	}
}

// A save probes the new credentials, stores them, drops the cached token
// and is audited with action and result, never the URL or the secret.
func TestPutConnectionSuccess(t *testing.T) {
	fx := newHandlerFixture(t)
	rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
		map[string]any{"tenantUrl": " https://acme.my.rubrik.com/ ", "clientId": " client-9 ", "clientSecret": "rotated-secret"})
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "rotated-secret") {
		t.Fatalf("response echoes the secret: %s", rec.Body.String())
	}
	got := responseData[connectionResponse](t, rec)
	if got.Revision != 8 || got.ClientID != "client-9" || !got.HasSecret || got.MappingsCleared {
		t.Errorf("response = %+v", got)
	}
	if p := fx.probeCalls(); len(p) != 1 || p[0] != (probeCall{"https://acme.my.rubrik.com/", "client-9", "rotated-secret"}) {
		t.Errorf("probes = %+v", p)
	}
	if fx.st.secret != "rotated-secret" || fx.flush.count() != 1 {
		t.Errorf("secret %q stored, %d flushes; want the new secret and one flush", fx.st.secret, fx.flush.count())
	}
	entries := fx.audit.all()
	if len(entries) != 1 {
		t.Fatalf("audit = %+v, want one entry", entries)
	}
	e := entries[0]
	if e.Action != audit.ActionRubrikConnectionUpdate || e.Result != audit.ResultSuccess || e.User != admin.Username || e.ResourceKind != auditKindConnection {
		t.Errorf("audit entry = %+v", e)
	}
	assertNoConnectionDetail(t, e, "acme.my.rubrik.com", "rotated-secret")
	assertNoConnectionDetail(t, e, "acme.my.rubrik.com", testSecret)
}

func assertNoConnectionDetail(t *testing.T, e audit.Entry, host, secret string) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{host, secret} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("audit entry contains %q: %s", leak, raw)
		}
	}
}

// An omitted secret is kept only on the origin it was entered for: moving
// the URL elsewhere needs the secret again, and the stored secret is never
// sent to the new address.
func TestPutConnectionKeepsTheSecretOnlyOnItsOrigin(t *testing.T) {
	fx := newHandlerFixture(t)
	rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
		map[string]any{"tenantUrl": "https://attacker.example.com", "clientId": "client-1"})
	wantStatus(t, rec, http.StatusBadRequest)
	if errorReason(t, rec) != "secret_required" {
		t.Errorf("reason = %q", errorReason(t, rec))
	}
	if len(fx.probeCalls()) != 0 {
		t.Fatalf("the stored secret was sent to another origin: %+v", fx.probeCalls())
	}

	rec = fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
		map[string]any{"tenantUrl": testTenant, "clientId": "client-2", "clientSecret": nil})
	wantStatus(t, rec, http.StatusOK)
	if p := fx.probeCalls(); len(p) != 1 || p[0].secret != testSecret || p[0].clientID != "client-2" {
		t.Errorf("probes = %+v; want the stored secret with the new client id", p)
	}
	if fx.st.secret != testSecret {
		t.Error("the stored secret changed on a keep")
	}

	// A connection without a usable secret (one entered for another origin)
	// always needs one, and that stale secret is never probed.
	fx.st.mu.Lock()
	fx.st.conn.HasSecret = false
	fx.st.mu.Unlock()
	rec = fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
		map[string]any{"tenantUrl": testTenant, "clientId": "client-2"})
	wantStatus(t, rec, http.StatusBadRequest)
	if p := fx.probeCalls(); len(p) != 1 {
		t.Errorf("a secret without a usable origin was probed: %+v", p)
	}
}

// Changing the tenant origin with a new secret clears the mappings, and the
// response and audit entry say so.
func TestPutConnectionOriginChangeClearsMappings(t *testing.T) {
	fx := newHandlerFixture(t)
	rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
		map[string]any{"tenantUrl": "https://other.my.rubrik.com", "clientId": "client-1", "clientSecret": "other-secret"})
	wantStatus(t, rec, http.StatusOK)
	if got := responseData[connectionResponse](t, rec); !got.MappingsCleared {
		t.Error("mappingsCleared = false after an origin change")
	}
	if list, _ := fx.st.ListMappings(context.Background(), testConn); len(list) != 0 {
		t.Errorf("mappings kept after an origin change: %+v", list)
	}
	if e := fx.audit.all(); len(e) != 1 || !strings.Contains(e[0].Detail, "mappings cleared") {
		t.Errorf("audit = %+v", e)
	}
}

func TestPutConnectionStoreOutcomes(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		reason string
	}{
		{store.ErrRubrikConnectionChanged, http.StatusConflict, "connection_changed"},
		{store.ErrRubrikSecretRequired, http.StatusBadRequest, "secret_required"},
		{errors.New("pool closed"), http.StatusServiceUnavailable, ""},
	} {
		fx := newHandlerFixture(t)
		fx.st.upsertErr = c.err
		rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
			map[string]any{"tenantUrl": testTenant, "clientId": "client-1", "clientSecret": "s2"})
		wantStatus(t, rec, c.status)
		if c.reason != "" && errorReason(t, rec) != c.reason {
			t.Errorf("%v: reason = %q, want %q", c.err, errorReason(t, rec), c.reason)
		}
		if fx.flush.count() != 0 {
			t.Errorf("%v: token flushed although nothing was saved", c.err)
		}
	}
}

// A stored connection that cannot be read (its secret no longer decrypts
// after a key change) blocks only a save that would keep that secret. A new
// secret repairs it, and the response and audit say whether the save
// cleared the mappings, never with the URL or a secret.
func TestPutConnectionRepairsAnUnreadableConnection(t *testing.T) {
	decryptErr := errors.New("decrypting rubrik client secret: cipher: message authentication failed")

	t.Run("kept secret is refused", func(t *testing.T) {
		fx := newHandlerFixture(t)
		fx.st.getErr = decryptErr
		rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
			map[string]any{"tenantUrl": testTenant, "clientId": "client-1"})
		wantStatus(t, rec, http.StatusServiceUnavailable)
		if len(fx.probeCalls()) != 0 || fx.st.upserts != 0 || len(fx.audit.all()) != 0 {
			t.Error("a kept-secret save over an unreadable row was probed, stored or audited")
		}
	})

	for _, c := range []struct {
		name, tenant string
		cleared      bool
	}{
		{"same origin keeps the mappings", testTenant, false},
		{"new origin clears the mappings", "https://other.my.rubrik.com", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newHandlerFixture(t)
			fx.st.getErr = decryptErr
			rec := fx.do(t, admin, "local", http.MethodPut, "/rubrik/connection",
				map[string]any{"tenantUrl": c.tenant, "clientId": "client-1", "clientSecret": "repaired-secret"})
			wantStatus(t, rec, http.StatusOK)
			if got := responseData[connectionResponse](t, rec); got.MappingsCleared != c.cleared || got.Revision != 8 {
				t.Errorf("response = %+v, want mappingsCleared %v at revision 8", got, c.cleared)
			}
			if p := fx.probeCalls(); len(p) != 1 || p[0].secret != "repaired-secret" {
				t.Errorf("probes = %+v; want one with the new secret", p)
			}
			if fx.st.secret != "repaired-secret" || fx.flush.count() != 1 {
				t.Errorf("secret %q stored, %d flushes; want the new secret and one flush", fx.st.secret, fx.flush.count())
			}
			entries := fx.audit.all()
			if len(entries) != 1 || entries[0].Result != audit.ResultSuccess || !strings.Contains(entries[0].Detail, "previous connection unreadable") {
				t.Fatalf("audit = %+v, want one success noting the unreadable row", entries)
			}
			if got := strings.Contains(entries[0].Detail, "mappings cleared"); got != c.cleared {
				t.Errorf("audit detail %q: mappings cleared %v, want %v", entries[0].Detail, got, c.cleared)
			}
			assertNoConnectionDetail(t, entries[0], strings.TrimPrefix(c.tenant, "https://"), "repaired-secret")
		})
	}
}

// ---------------------------------------------------------------------------

func TestEvictClusterDropsOnlyThatCluster(t *testing.T) {
	fx := newHandlerFixture(t)
	ctx := context.Background()
	for _, id := range []string{"local", "remote-1"} {
		tgt, err := fx.svc.Resolve(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.svc.Objects(ctx, tgt); err != nil {
			t.Fatal(err)
		}
	}
	tgt, _ := fx.svc.Resolve(ctx, "local")
	if _, _, err := fx.svc.SLADomains(ctx, tgt); err != nil {
		t.Fatal(err)
	}

	fx.h.EvictCluster("remote-1")

	fx.svc.mu.Lock()
	keys := make([]string, 0, len(fx.svc.entries))
	for k := range fx.svc.entries {
		keys = append(keys, k)
	}
	fx.svc.mu.Unlock()
	sort.Strings(keys)
	want := []string{"objects|7|local|" + idCluster, "sla|7"}
	if !slices.Equal(keys, want) {
		t.Errorf("cache keys after evicting remote-1 = %v, want %v", keys, want)
	}

	fx.h.EvictCluster("") // the local cluster's empty id
	fx.svc.mu.Lock()
	_, kept := fx.svc.entries["objects|7|local|"+idCluster]
	fx.svc.mu.Unlock()
	if kept {
		t.Error("evicting the local cluster by its empty id kept its entry")
	}
}

func TestNewHandlerRequiresItsCollaborators(t *testing.T) {
	full := HandlerDeps{
		Store: &adminStore{fakeStore: &fakeStore{}}, Clusters: fakeClusters{}, Service: &Service{},
		Access: &Access{}, RSC: newFakeReader(), Client: &flushCounter{}, Audit: &fakeAudit{}, ConnectionID: testConn,
	}
	if _, err := NewHandler(full); err != nil {
		t.Fatalf("full deps: %v", err)
	}
	for name, mutate := range map[string]func(d *HandlerDeps){
		"store":    func(d *HandlerDeps) { d.Store = nil },
		"clusters": func(d *HandlerDeps) { d.Clusters = nil },
		"service":  func(d *HandlerDeps) { d.Service = nil },
		"access":   func(d *HandlerDeps) { d.Access = nil },
		"rsc":      func(d *HandlerDeps) { d.RSC = nil },
		"client":   func(d *HandlerDeps) { d.Client = nil },
		"audit":    func(d *HandlerDeps) { d.Audit = nil },
		"conn id":  func(d *HandlerDeps) { d.ConnectionID = "" },
	} {
		d := full
		mutate(&d)
		if _, err := NewHandler(d); err == nil {
			t.Errorf("NewHandler without %s succeeded", name)
		}
	}
}

// The writes setting reaches status, so the UI can hide write controls (R29).
func TestStatusReportsWritesSetting(t *testing.T) {
	fx := newHandlerFixture(t)
	fx.h.access = NewAccess(newFakeChecker(), lister(t, nil), false)
	if st := responseData[StatusView](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/status", nil)); st.WritesEnabled {
		t.Error("writesEnabled = true with writes off")
	}
}

// Status survives a store failure: it reports the cluster unavailable and
// never "not set up", which would read as unprotected (R13).
func TestStatusWhenTheStoreFails(t *testing.T) {
	fx := newHandlerFixture(t)
	fx.st.mu.Lock()
	fx.st.revErr = errors.New("pool closed")
	fx.st.mu.Unlock()
	st := responseData[StatusView](t, fx.do(t, owner, "local", http.MethodGet, "/rubrik/status", nil))
	if st.Cluster.State != StateUnavailable {
		t.Errorf("state = %q, want %q", st.Cluster.State, StateUnavailable)
	}
}
