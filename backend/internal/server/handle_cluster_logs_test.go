package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kfake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/loki"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

// --- Loki binding admin API (remote logs and shells U13) ---

const (
	testLokiURL      = "https://8.8.8.8:3100"
	testLokiSameHost = "https://8.8.8.8:3100/gateway"
	testLokiElse     = "https://1.1.1.1:3100"
	testLokiToken    = "loki-secret-token"
)

// fakeLokiStore keeps bindings the way ClusterLogsStore does: a token is
// pinned to the origin it was entered for, and a kept token survives only
// while the URL the caller read is still stored and its origin does not
// move.
type fakeLokiStore struct {
	mu           sync.Mutex
	bindings     map[string]store.LokiBinding
	tokens       map[string]string
	origins      map[string]string
	upsertCalled bool
	lastWrite    store.LokiBindingWrite
	beforeUpsert func() // runs inside Upsert before it checks KeptFrom
	err          error
}

func newFakeLokiStore() *fakeLokiStore {
	return &fakeLokiStore{bindings: map[string]store.LokiBinding{}, tokens: map[string]string{}, origins: map[string]string{}}
}

func (f *fakeLokiStore) state(id string) (store.LokiBinding, string) {
	b := f.bindings[id]
	tok, origin := f.tokens[id], f.origins[id]
	b.HasToken = tok != "" && origin == urlorigin.Of(b.URL)
	b.TokenStale = tok != "" && !b.HasToken
	if !b.HasToken {
		tok = ""
	}
	return b, tok
}

func (f *fakeLokiStore) Get(_ context.Context, id string) (store.LokiBinding, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return store.LokiBinding{}, "", f.err
	}
	if _, ok := f.bindings[id]; !ok {
		return store.LokiBinding{}, "", store.ErrLokiBindingNotFound
	}
	b, tok := f.state(id)
	return b, tok, nil
}

func (f *fakeLokiStore) Upsert(_ context.Context, id string, w store.LokiBindingWrite) (store.LokiBinding, error) {
	if f.beforeUpsert != nil {
		f.beforeUpsert()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalled, f.lastWrite = true, w
	if f.err != nil {
		return store.LokiBinding{}, f.err
	}
	prev := f.bindings[id]
	switch {
	case w.Token != nil:
		f.tokens[id], f.origins[id] = *w.Token, urlorigin.Of(w.URL)
	case prev.URL != w.KeptFrom:
		return store.LokiBinding{}, store.ErrLokiBindingChanged
	case f.origins[id] != urlorigin.Of(w.URL) || f.origins[id] != urlorigin.Of(w.KeptFrom):
		delete(f.tokens, id)
		delete(f.origins, id)
	}
	if w.Token != nil && *w.Token == "" {
		delete(f.tokens, id)
		delete(f.origins, id)
	}
	f.bindings[id] = store.LokiBinding{ClusterID: id, URL: w.URL, TenantID: w.TenantID, UpdatedAt: time.Now()}
	b, _ := f.state(id)
	return b, nil
}

func (f *fakeLokiStore) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if _, ok := f.bindings[id]; !ok {
		return store.ErrLokiBindingNotFound
	}
	delete(f.bindings, id)
	delete(f.tokens, id)
	delete(f.origins, id)
	return nil
}

// seed stores a binding as if an earlier save had written it.
func (f *fakeLokiStore) seed(id, url, token, tokenOrigin string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[id] = store.LokiBinding{ClusterID: id, URL: url}
	if token != "" {
		f.tokens[id], f.origins[id] = token, tokenOrigin
	}
}

type logsFixture struct {
	srv     *Server
	router  chi.Router
	store   *fakeLokiStore
	evicter *recordingEvicter
	audit   *captureAudit
}

func newLogsFixture(t *testing.T) *logsFixture {
	t.Helper()
	f := &logsFixture{store: newFakeLokiStore(), evicter: &recordingEvicter{}, audit: &captureAudit{}}
	f.srv = &Server{
		Config:           &config.Config{ClusterID: "local"},
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuditLogger:      f.audit,
		ClusterLogsStore: f.store,
		LokiResolver:     f.evicter,
		metricsClusters: &fakeClusterRegistry{records: map[string]*store.ClusterRecord{
			testRemoteID: {ID: testRemoteID, Name: "remote-a"},
			"local-row":  {ID: "local-row", Name: "local", IsLocal: true},
		}},
	}
	f.router = chi.NewRouter()
	f.srv.registerClusterRoutes(f.router)
	return f
}

func (f *logsFixture) do(t *testing.T, user *auth.User, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/clusters/"+id+"/logs", rd)
	if user != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *logsFixture) auditEntries() []audit.Entry {
	f.audit.mu.Lock()
	defer f.audit.mu.Unlock()
	return append([]audit.Entry(nil), f.audit.entries...)
}

// assertNothingWritten fails when a refused request stored, evicted or
// audited anything.
func (f *logsFixture) assertNothingWritten(t *testing.T) {
	t.Helper()
	if f.store.upsertCalled || len(f.evicter.list()) != 0 || len(f.auditEntries()) != 0 {
		t.Errorf("a refused request wrote: upsert %v, evicted %v, audit %+v", f.store.upsertCalled, f.evicter.list(), f.auditEntries())
	}
}

// Covers R6: a PUT with an https public URL, a token and a tenant stores
// them, evicts the resolver, audits as cluster-logs without the URL or the
// token, and answers without the token.
func TestClusterLogs_PutStoresEvictsAudits(t *testing.T) {
	f := newLogsFixture(t)
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
		`{"url":" `+testLokiURL+` ","token":"`+testLokiToken+`","tenantId":"team-a|team-b"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	got := decodeMetricsBinding(t, rec)
	if got["url"] != testLokiURL || got["tenantId"] != "team-a|team-b" || got["hasToken"] != true || got["tokenStale"] != false {
		t.Fatalf("response = %v, want the trimmed URL, the tenant and hasToken", got)
	}
	if strings.Contains(rec.Body.String(), testLokiToken) {
		t.Fatal("the response carried the token")
	}
	if w := f.store.lastWrite; w.URL != testLokiURL || w.Token == nil || *w.Token != testLokiToken || w.TenantID != "team-a|team-b" {
		t.Fatalf("write = %+v, want the URL, token and tenant", w)
	}
	if ev := f.evicter.list(); len(ev) != 1 || ev[0] != testRemoteID {
		t.Fatalf("evicted %v, want [%s]", ev, testRemoteID)
	}
	entries := f.auditEntries()
	if len(entries) != 1 || entries[0].ResourceKind != "cluster-logs" || entries[0].Action != audit.ActionUpdate || entries[0].ResourceName != testRemoteID {
		t.Fatalf("audit = %+v, want one cluster-logs update for the cluster", entries)
	}
	if d := entries[0].Detail; strings.Contains(d, testLokiToken) || strings.Contains(d, "8.8.8.8") {
		t.Fatalf("audit detail %q carries the token or the URL", d)
	}
}

// A private, http, userinfo, query-string or fragment URL, a bad tenant and
// an oversized token are each refused with a fixed message, and nothing is
// written.
func TestClusterLogs_PutRejectsBadInput(t *testing.T) {
	cases := []struct{ name, body, msg string }{
		{"missing url", `{}`, "url is required"},
		{"http", `{"url":"http://8.8.8.8:3100"}`, "url must be a valid HTTPS URL"},
		{"no host", `{"url":"https://"}`, "url must be a valid HTTPS URL"},
		{"private", `{"url":"https://10.0.0.5:3100"}`, "url resolves to a private or reserved address"},
		{"loopback", `{"url":"https://127.0.0.1:3100"}`, "url resolves to a private or reserved address"},
		{"metadata", `{"url":"https://169.254.169.254"}`, "url resolves to a private or reserved address"},
		{"userinfo", `{"url":"https://u:p@8.8.8.8:3100"}`, "url must not contain credentials; send the token separately"},
		{"query string", `{"url":"` + testLokiURL + `/?orgId=1"}`, "url must not contain a query string or fragment"},
		{"fragment", `{"url":"` + testLokiURL + `/#f"}`, "url must not contain a query string or fragment"},
		{"tenant with a newline", `{"url":"` + testLokiURL + `","tenantId":"a\r\nX-Evil: 1"}`, msgLokiTenantInvalid},
		{"tenant with a space", `{"url":"` + testLokiURL + `","tenantId":"team a"}`, msgLokiTenantInvalid},
		{"tenant too long", `{"url":"` + testLokiURL + `","tenantId":"` + strings.Repeat("a", maxLokiTenantBytes+1) + `"}`, msgLokiTenantInvalid},
		{"oversized token", `{"url":"` + testLokiURL + `","token":"` + strings.Repeat("a", maxMetricsTokenBytes+1) + `"}`, "token too large (max 64KB)"},
		{"not json", `not json`, "invalid request body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLogsFixture(t)
			rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s; want 400", rec.Code, rec.Body.String())
			}
			if got := decodeMetricsErr(t, rec).Error.Message; got != tc.msg {
				t.Errorf("message = %q; want %q", got, tc.msg)
			}
			f.assertNothingWritten(t)
		})
	}
}

func TestValidLokiTenant(t *testing.T) {
	for _, ok := range []string{"", "fake", "team-a", "a.b_c*d'(e)!", "t1|t2|t3", strings.Repeat("x", maxLokiTenantBytes)} {
		if !validLokiTenant(ok) {
			t.Errorf("validLokiTenant(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"a b", "a\nb", "a/b", "a:b", "ä", strings.Repeat("x", maxLokiTenantBytes+1)} {
		if validLokiTenant(bad) {
			t.Errorf("validLokiTenant(%q) = true, want false", bad)
		}
	}
}

// Pointer semantics: an omitted token keeps the stored one on the same
// host, "" clears it, and a value replaces it.
func TestClusterLogs_PutTokenSemantics(t *testing.T) {
	t.Run("omitted keeps it on the same host", func(t *testing.T) {
		f := newLogsFixture(t)
		f.store.seed(testRemoteID, testLokiURL, testLokiToken, urlorigin.Of(testLokiURL))
		rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiSameHost+`"}`)
		if rec.Code != http.StatusOK || decodeMetricsBinding(t, rec)["hasToken"] != true {
			t.Fatalf("got %d %s, want 200 with the token kept", rec.Code, rec.Body.String())
		}
		if w := f.store.lastWrite; w.Token != nil || w.KeptFrom != testLokiURL {
			t.Fatalf("write = %+v, want no token and KeptFrom the stored URL", w)
		}
	})
	t.Run("empty clears it", func(t *testing.T) {
		f := newLogsFixture(t)
		f.store.seed(testRemoteID, testLokiURL, testLokiToken, urlorigin.Of(testLokiURL))
		rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiURL+`","token":""}`)
		if rec.Code != http.StatusOK || decodeMetricsBinding(t, rec)["hasToken"] != false {
			t.Fatalf("got %d %s, want 200 with no token", rec.Code, rec.Body.String())
		}
		if w := f.store.lastWrite; w.Token == nil || *w.Token != "" {
			t.Fatalf("write = %+v, want an explicit empty token", w)
		}
	})
	t.Run("a value replaces it", func(t *testing.T) {
		f := newLogsFixture(t)
		f.store.seed(testRemoteID, testLokiURL, "old-token", urlorigin.Of(testLokiURL))
		rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiURL+`","token":"new-token"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d %s, want 200", rec.Code, rec.Body.String())
		}
		if _, tok, _ := f.store.Get(context.Background(), testRemoteID); tok != "new-token" {
			t.Fatalf("stored token = %q, want the new one", tok)
		}
	})
	t.Run("a new host with a token proceeds", func(t *testing.T) {
		f := newLogsFixture(t)
		f.store.seed(testRemoteID, testLokiURL, testLokiToken, urlorigin.Of(testLokiURL))
		rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiElse+`","token":"other-token"}`)
		if rec.Code != http.StatusOK || decodeMetricsBinding(t, rec)["hasToken"] != true {
			t.Fatalf("got %d %s, want 200 with the new token", rec.Code, rec.Body.String())
		}
	})
	t.Run("a new host without a stored token proceeds", func(t *testing.T) {
		f := newLogsFixture(t)
		f.store.seed(testRemoteID, testLokiURL, "", "")
		if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiElse+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("got %d %s, want 200", rec.Code, rec.Body.String())
		}
	})
}

// A stored token is never carried to another host: moving the URL without
// the token is refused, and so is keeping a token already stale.
func TestClusterLogs_PutRefusesToMoveToken(t *testing.T) {
	cases := []struct{ name, tokenOrigin, url string }{
		{"new host", urlorigin.Of(testLokiURL), testLokiElse},
		{"stale token", urlorigin.Of(testLokiElse), testLokiURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLogsFixture(t)
			f.store.seed(testRemoteID, testLokiURL, testLokiToken, tc.tokenOrigin)
			rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+tc.url+`"}`)
			if rec.Code != http.StatusBadRequest || decodeMetricsErr(t, rec).Error.Message != msgLokiTokenReentry {
				t.Fatalf("got %d %s, want 400 %q", rec.Code, rec.Body.String(), msgLokiTokenReentry)
			}
			f.assertNothingWritten(t)
		})
	}
}

// A save that keeps the token while another save moves the binding is
// refused with 409, not paired with the other save's token.
func TestClusterLogs_PutKeepTokenRacingMoveRefused(t *testing.T) {
	f := newLogsFixture(t)
	f.store.seed(testRemoteID, testLokiURL, testLokiToken, urlorigin.Of(testLokiURL))
	f.store.beforeUpsert = func() {
		f.store.beforeUpsert = nil
		f.store.seed(testRemoteID, testLokiElse, "other-token", urlorigin.Of(testLokiElse))
	}
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiSameHost+`"}`)
	if rec.Code != http.StatusConflict || decodeMetricsErr(t, rec).Error.Message != msgLokiBindingChanged {
		t.Fatalf("got %d %s, want 409 %q", rec.Code, rec.Body.String(), msgLokiBindingChanged)
	}
	if len(f.evicter.list()) != 0 || len(f.auditEntries()) != 0 {
		t.Fatal("a refused save evicted or audited")
	}
}

// GET answers the binding without the token, and 404 loki_not_configured
// when there is none; DELETE removes it, evicts and audits, then 404s.
func TestClusterLogs_GetAndDelete(t *testing.T) {
	f := newLogsFixture(t)
	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusNotFound || decodeMetricsErr(t, rec).Error.Reason != reasonLokiNotConfigured {
		t.Fatalf("GET without a binding = %d %s, want 404 %s", rec.Code, rec.Body.String(), reasonLokiNotConfigured)
	}

	f.store.seed(testRemoteID, testLokiURL, testLokiToken, urlorigin.Of(testLokiURL))
	rec = f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	got := decodeMetricsBinding(t, rec)
	if rec.Code != http.StatusOK || got["url"] != testLokiURL || got["hasToken"] != true || strings.Contains(rec.Body.String(), testLokiToken) {
		t.Fatalf("GET = %d %s, want the binding with hasToken and no token", rec.Code, rec.Body.String())
	}

	if rec = f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", rec.Code, rec.Body.String())
	}
	if ev := f.evicter.list(); len(ev) != 1 || ev[0] != testRemoteID {
		t.Fatalf("evicted %v, want [%s]", ev, testRemoteID)
	}
	if e := f.auditEntries(); len(e) != 1 || e[0].ResourceKind != "cluster-logs" || e[0].Action != audit.ActionDelete {
		t.Fatalf("audit = %+v, want one cluster-logs delete", e)
	}
	rec = f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, "")
	if rec.Code != http.StatusNotFound || decodeMetricsErr(t, rec).Error.Reason != reasonLokiNotConfigured {
		t.Fatalf("second DELETE = %d %s, want 404 %s", rec.Code, rec.Body.String(), reasonLokiNotConfigured)
	}
}

// The local cluster discovers its own Loki and has no binding: every route
// refuses it, by the local id and by a registered row marked local.
func TestClusterLogs_LocalClusterRejected(t *testing.T) {
	for _, id := range []string{"local", "local-row"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			t.Run(id+" "+method, func(t *testing.T) {
				f := newLogsFixture(t)
				rec := f.do(t, metricsAdmin, method, id, `{"url":"`+testLokiURL+`"}`)
				if rec.Code != http.StatusBadRequest || decodeMetricsErr(t, rec).Error.Message != msgLokiLocalCluster {
					t.Fatalf("got %d %s, want 400 %q", rec.Code, rec.Body.String(), msgLokiLocalCluster)
				}
				f.assertNothingWritten(t)
			})
		}
	}
}

// A non-admin gets 403 on every route and reaches nothing.
func TestClusterLogs_NonAdminForbidden(t *testing.T) {
	viewer := &auth.User{ID: "u-1", Username: "viewer", Provider: "local", Roles: []string{"user"}}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			f := newLogsFixture(t)
			if rec := f.do(t, viewer, method, testRemoteID, `{"url":"`+testLokiURL+`"}`); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d; want 403", rec.Code)
			}
			f.assertNothingWritten(t)
		})
	}
}

// An unregistered cluster is 404 cluster_unknown; without a database the
// routes answer 503 db_unavailable; a store failure is a fixed 503 that
// leaks no raw error.
func TestClusterLogs_PreambleAndStoreFailures(t *testing.T) {
	t.Run("unknown cluster", func(t *testing.T) {
		f := newLogsFixture(t)
		rec := f.do(t, metricsAdmin, http.MethodGet, "nope", "")
		if rec.Code != http.StatusNotFound || decodeMetricsErr(t, rec).Error.Reason != string(k8s.ReasonClusterUnknown) {
			t.Fatalf("got %d %s, want 404 cluster_unknown", rec.Code, rec.Body.String())
		}
	})
	t.Run("no database", func(t *testing.T) {
		f := newLogsFixture(t)
		f.srv.ClusterLogsStore = nil
		rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiURL+`"}`)
		if rec.Code != http.StatusServiceUnavailable || decodeMetricsErr(t, rec).Error.Message != msgLokiNoDatabase {
			t.Fatalf("got %d %s, want 503 %q", rec.Code, rec.Body.String(), msgLokiNoDatabase)
		}
	})
	t.Run("store failure", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			f := newLogsFixture(t)
			f.store.err = errors.New("dial tcp 10.0.0.9:5432: connection refused")
			rec := f.do(t, metricsAdmin, method, testRemoteID, `{"url":"`+testLokiURL+`","token":"x"}`)
			if rec.Code != http.StatusServiceUnavailable || decodeMetricsErr(t, rec).Error.Message != msgLokiStoreFailed {
				t.Fatalf("%s = %d %s, want 503 %q", method, rec.Code, rec.Body.String(), msgLokiStoreFailed)
			}
			if strings.Contains(rec.Body.String(), "10.0.0.9") {
				t.Fatalf("%s leaked the raw error: %s", method, rec.Body.String())
			}
		}
	})
	t.Run("nil resolver", func(t *testing.T) {
		f := newLogsFixture(t)
		f.srv.LokiResolver = nil
		if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiURL+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("got %d %s, want 200 with no resolver wired", rec.Code, rec.Body.String())
		}
	})
}

// Covers AE3 end to end: once a binding is saved, log search for the
// cluster uses its URL and not discovery, and deleting it evicts what
// discovery had cached, so the next search finds the cluster's Loki as it
// now is.
func TestClusterLogs_ChangeReachesTheResolver(t *testing.T) {
	f := newLogsFixture(t)
	cs := kfake.NewClientset(remoteLokiObjects()...)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	resolver := loki.NewResolverWith(nil, f.store, tailClients{cs: cs}, &tailForwarder{}, logger)
	f.srv.LokiResolver = resolver
	ctx := context.Background()

	loc, err := resolver.Locate(ctx, testRemoteID, "admin", nil)
	if err != nil || loc.Source != loki.SourcePortForward || loc.Namespace != "monitoring" {
		t.Fatalf("before any binding: %+v, %v; want discovery of monitoring/loki-gateway", loc, err)
	}

	if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"url":"`+testLokiURL+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	loc, err = resolver.Locate(ctx, testRemoteID, "admin", nil)
	if err != nil || loc.Source != loki.SourceBinding || loc.URL != testLokiURL {
		t.Fatalf("after PUT: %+v, %v; want the binding URL", loc, err)
	}

	// The cluster's Loki goes away while the binding is set; the delete must
	// drop the discovery cached before it.
	if err := cs.CoreV1().Services("monitoring").Delete(ctx, "loki-gateway", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body.String())
	}
	_, err = resolver.Locate(ctx, testRemoteID, "admin", nil)
	var ue *loki.UnavailableError
	if !errors.As(err, &ue) || ue.Reason != loki.ReasonLokiNotFound {
		t.Fatalf("after DELETE: %v; want loki_not_found from fresh discovery, not the cached Service", err)
	}
}
