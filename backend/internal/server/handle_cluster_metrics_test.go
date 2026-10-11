package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	testRemoteID   = "0123456789abcdef0123456789abcdef"
	testPublicProm = "https://8.8.8.8:9090"
	testToken      = "s3cr3t-bearer-token-value"
)

// fakeMetricsStore is an in-memory clusterMetricsStore with the same token
// semantics as the real one: nil keeps, "" clears, anything else replaces.
type fakeMetricsStore struct {
	mu       sync.Mutex
	bindings map[string]store.MetricsBinding
	tokens   map[string]string
	// lastUpsertToken records the token argument of the last Upsert, and
	// upsertCalled whether one happened at all.
	lastUpsertToken *string
	upsertCalled    bool
	getErr          error
}

func newFakeMetricsStore() *fakeMetricsStore {
	return &fakeMetricsStore{bindings: map[string]store.MetricsBinding{}, tokens: map[string]string{}}
}

func (f *fakeMetricsStore) Get(_ context.Context, id string) (store.MetricsBinding, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return store.MetricsBinding{}, "", f.getErr
	}
	b, ok := f.bindings[id]
	if !ok {
		return store.MetricsBinding{}, "", store.ErrMetricsBindingNotFound
	}
	return b, f.tokens[id], nil
}

func (f *fakeMetricsStore) Upsert(_ context.Context, id, promURL, amURL string, token *string, keptFrom string) (store.MetricsBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalled = true
	f.lastUpsertToken = token
	// The real store keeps the token only while the stored URL is still
	// keptFrom ("" = no binding); the check and the write are atomic there.
	if token == nil && f.bindings[id].PrometheusURL != keptFrom {
		return store.MetricsBinding{}, store.ErrMetricsBindingChanged
	}
	if token != nil {
		f.tokens[id] = *token
	}
	b := store.MetricsBinding{
		ClusterID: id, PrometheusURL: promURL, AlertmanagerURL: amURL,
		HasToken: f.tokens[id] != "", UpdatedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	}
	f.bindings[id] = b
	return b, nil
}

func (f *fakeMetricsStore) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bindings[id]; !ok {
		return store.ErrMetricsBindingNotFound
	}
	delete(f.bindings, id)
	delete(f.tokens, id)
	return nil
}

// fakeClusterRegistry answers Get from a fixed set of records.
type fakeClusterRegistry struct {
	records map[string]*store.ClusterRecord
	err     error
}

func (g *fakeClusterRegistry) Get(_ context.Context, id string) (*store.ClusterRecord, error) {
	if g.err != nil {
		return nil, g.err
	}
	rec, ok := g.records[id]
	if !ok {
		// The shape ClusterStore.Get returns for a missing row.
		return nil, fmt.Errorf("getting cluster %s: %w", id, pgx.ErrNoRows)
	}
	return rec, nil
}

type recordingEvicter struct {
	mu      sync.Mutex
	evicted []string
}

func (e *recordingEvicter) Evict(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evicted = append(e.evicted, id)
}

func (e *recordingEvicter) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.evicted...)
}

type probeCall struct{ url, token string }

type metricsFixture struct {
	srv     *Server
	router  chi.Router
	store   *fakeMetricsStore
	evicter *recordingEvicter
	audit   *captureAudit
	probes  *[]probeCall
	probeFn *func(ctx context.Context, url, token string) error
}

func newMetricsFixture(t *testing.T) *metricsFixture {
	t.Helper()
	fs := newFakeMetricsStore()
	ev := &recordingEvicter{}
	ca := &captureAudit{}
	var probes []probeCall
	probeFn := func(_ context.Context, url, token string) error {
		probes = append(probes, probeCall{url, token})
		return nil
	}
	s := &Server{
		Config:              &config.Config{ClusterID: "local"},
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuditLogger:         ca,
		ClusterMetricsStore: fs,
		MetricsResolver:     ev,
		metricsClusters: &fakeClusterRegistry{records: map[string]*store.ClusterRecord{
			testRemoteID: {ID: testRemoteID, Name: "remote-a"},
			"local-row":  {ID: "local-row", Name: "local", IsLocal: true},
		}},
	}
	f := &metricsFixture{srv: s, store: fs, evicter: ev, audit: ca, probes: &probes, probeFn: &probeFn}
	s.probePrometheus = func(ctx context.Context, url, token string) error { return (*f.probeFn)(ctx, url, token) }
	r := chi.NewRouter()
	s.registerClusterRoutes(r)
	f.router = r
	return f
}

var metricsAdmin = &auth.User{ID: "admin-1", Username: "admin", Provider: "local", Roles: []string{"admin"}}

func (f *metricsFixture) do(t *testing.T, user *auth.User, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/clusters/"+id+"/metrics", rd)
	if user != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

type metricsErrBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

func decodeMetricsErr(t *testing.T, rec *httptest.ResponseRecorder) metricsErrBody {
	t.Helper()
	var b metricsErrBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}
	return b
}

func decodeMetricsBinding(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var b struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decoding data body %q: %v", rec.Body.String(), err)
	}
	return b.Data
}

func TestClusterMetrics_PutWithTokenStoresProbesEvictsAudits(t *testing.T) {
	f := newMetricsFixture(t)

	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
		`{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatalf("response echoes the token: %s", rec.Body.String())
	}
	data := decodeMetricsBinding(t, rec)
	want := map[string]any{
		"clusterId": testRemoteID, "prometheusUrl": testPublicProm, "hasToken": true,
		"alertmanagerUrl": "", "updatedAt": "2026-10-10T12:00:00Z",
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data[%q] = %v; want %v", k, data[k], v)
		}
	}
	if len(data) != len(want) {
		t.Errorf("data has keys %v; want exactly %v", data, want)
	}

	if got := *f.probes; len(got) != 1 || got[0] != (probeCall{testPublicProm, testToken}) {
		t.Errorf("probe calls = %+v; want one with the request URL and token", got)
	}
	if f.store.tokens[testRemoteID] != testToken {
		t.Errorf("stored token = %q; want the request token", f.store.tokens[testRemoteID])
	}
	if ev := f.evicter.list(); len(ev) != 1 || ev[0] != testRemoteID {
		t.Errorf("evicted = %v; want [%s]", ev, testRemoteID)
	}

	if len(f.audit.entries) != 1 {
		t.Fatalf("audit entries = %d; want 1", len(f.audit.entries))
	}
	e := f.audit.entries[0]
	if e.Action != audit.ActionUpdate || e.ResourceKind != "cluster-metrics" || e.ResourceName != testRemoteID ||
		e.User != "admin" || e.Result != audit.ResultSuccess {
		t.Errorf("audit entry = %+v", e)
	}
	if strings.Contains(e.Detail, testToken) {
		t.Errorf("audit detail carries the token: %q", e.Detail)
	}
}

// An omitted token keeps the stored one only while the Prometheus stays on
// the same scheme and host: a path edit probes with, and keeps, the token.
func TestClusterMetrics_PutWithoutTokenSameHostKeepsStoredToken(t *testing.T) {
	f := newMetricsFixture(t)
	if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
		`{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d", rec.Code)
	}

	newURL := testPublicProm + "/prometheus"
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+newURL+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	if f.store.lastUpsertToken != nil {
		t.Errorf("Upsert token = %q; want nil (keep the stored token)", *f.store.lastUpsertToken)
	}
	if f.store.tokens[testRemoteID] != testToken {
		t.Errorf("stored token = %q; want it kept", f.store.tokens[testRemoteID])
	}
	if data := decodeMetricsBinding(t, rec); data["hasToken"] != true || data["prometheusUrl"] != newURL {
		t.Errorf("data = %v; want hasToken true and the new URL", data)
	}
	// The probe ran against the new URL with the token the binding keeps.
	probes := *f.probes
	if last := probes[len(probes)-1]; last != (probeCall{newURL, testToken}) {
		t.Errorf("probe = %+v; want the new URL with the stored token", last)
	}
}

// Moving the binding to another host without re-entering the token is
// refused before any probe: the stored token must never be sent to a host
// it was not entered for (credential retargeting, CWE-522).
func TestClusterMetrics_PutWithoutTokenNewHostRefused(t *testing.T) {
	cases := map[string]string{
		"other host": "https://1.1.1.1:9090",
		"other port": "https://8.8.8.8:9091",
	}
	for name, newURL := range cases {
		t.Run(name, func(t *testing.T) {
			f := newMetricsFixture(t)
			if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
				`{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`); rec.Code != http.StatusOK {
				t.Fatalf("seed PUT status = %d", rec.Code)
			}
			seedProbes := len(*f.probes)
			f.store.upsertCalled = false
			f.store.lastUpsertToken = nil

			rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+newURL+`"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s; want 400", rec.Code, rec.Body.String())
			}
			if got := decodeMetricsErr(t, rec).Error.Message; got != msgMetricsTokenReentry {
				t.Errorf("message = %q; want %q", got, msgMetricsTokenReentry)
			}
			if n := len(*f.probes); n != seedProbes {
				t.Errorf("probe calls = %d after the refused PUT; want %d (no probe)", n, seedProbes)
			}
			if f.store.upsertCalled {
				t.Error("store was written on a refused PUT")
			}
			if b := f.store.bindings[testRemoteID]; b.PrometheusURL != testPublicProm || f.store.tokens[testRemoteID] != testToken {
				t.Errorf("stored binding = %+v token %q; want it untouched", b, f.store.tokens[testRemoteID])
			}
			if strings.Contains(rec.Body.String(), testToken) {
				t.Errorf("response echoes the token: %s", rec.Body.String())
			}
		})
	}
}

// A new host with a token supplied proceeds and probes with the new token.
func TestClusterMetrics_PutNewHostWithTokenProceeds(t *testing.T) {
	f := newMetricsFixture(t)
	if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
		`{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d", rec.Code)
	}

	newURL := "https://1.1.1.1:9090"
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+newURL+`","token":"other-token"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	probes := *f.probes
	if last := probes[len(probes)-1]; last != (probeCall{newURL, "other-token"}) {
		t.Errorf("probe = %+v; want the new URL with the supplied token", last)
	}
	if f.store.tokens[testRemoteID] != "other-token" {
		t.Errorf("stored token = %q; want the supplied one", f.store.tokens[testRemoteID])
	}
}

// Two overlapping saves: A omits the token (keep) and passes the origin
// check against the old URL; while A probes, B moves the binding to another
// host with that host's token. A must not then write the old URL with B's
// token, which would send B's token to a host it was not entered for. A is
// refused with 409 and B's binding stands.
func TestClusterMetrics_PutKeepTokenRacingMoveRefused(t *testing.T) {
	cases := map[string]string{
		"binding moved with a token":  `{"prometheusUrl":"https://1.1.1.1:9090","token":"b-token"}`,
		"binding created meanwhile":   "",
		"binding moved without token": `{"prometheusUrl":"https://1.1.1.1:9090","token":""}`,
	}
	for name, bodyB := range cases {
		t.Run(name, func(t *testing.T) {
			f := newMetricsFixture(t)
			if bodyB != "" {
				if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
					`{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`); rec.Code != http.StatusOK {
					t.Fatalf("seed PUT status = %d", rec.Code)
				}
			} else {
				bodyB = `{"prometheusUrl":"https://1.1.1.1:9090","token":"b-token"}`
			}
			wantB := map[string]any{}
			if err := json.Unmarshal([]byte(bodyB), &wantB); err != nil {
				t.Fatal(err)
			}

			// B runs inside A's probe, after A's origin check.
			raced := false
			*f.probeFn = func(_ context.Context, url, token string) error {
				if !raced {
					raced = true
					if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, bodyB); rec.Code != http.StatusOK {
						t.Fatalf("racing PUT status = %d, body %s", rec.Code, rec.Body.String())
					}
				}
				return nil
			}
			f.evicter.evicted = nil

			rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`"}`)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, body %s; want 409", rec.Code, rec.Body.String())
			}
			if got := decodeMetricsErr(t, rec).Error.Message; got != msgMetricsBindingChanged {
				t.Errorf("message = %q; want %q", got, msgMetricsBindingChanged)
			}
			if b := f.store.bindings[testRemoteID]; b.PrometheusURL != wantB["prometheusUrl"] {
				t.Errorf("stored URL = %q; want B's %v", b.PrometheusURL, wantB["prometheusUrl"])
			}
			if got := f.store.tokens[testRemoteID]; got != wantB["token"] {
				t.Errorf("stored token = %q; want B's %v", got, wantB["token"])
			}
			// Only B's write evicted; the refused save did not.
			if ev := f.evicter.list(); len(ev) != 1 {
				t.Errorf("evictions = %v; want exactly one (B's)", ev)
			}
			if strings.Contains(rec.Body.String(), testToken) || strings.Contains(rec.Body.String(), "b-token") {
				t.Errorf("response echoes a token: %s", rec.Body.String())
			}
		})
	}
}

// A keep-token save with no concurrent writer is unaffected by the guard.
func TestClusterMetrics_PutKeepTokenSameHostPassesKeptFrom(t *testing.T) {
	f := newMetricsFixture(t)
	if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
		`{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d", rec.Code)
	}
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`/"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	if f.store.tokens[testRemoteID] != testToken {
		t.Errorf("stored token = %q; want it kept", f.store.tokens[testRemoteID])
	}
}

// With no stored token there is nothing to retarget, so a host change with
// the token omitted proceeds and probes without one.
func TestClusterMetrics_PutWithoutTokenNewHostNoStoredTokenProceeds(t *testing.T) {
	f := newMetricsFixture(t)
	if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d", rec.Code)
	}
	newURL := "https://1.1.1.1:9090"
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+newURL+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	probes := *f.probes
	if last := probes[len(probes)-1]; last != (probeCall{newURL, ""}) {
		t.Errorf("probe = %+v; want the new URL with no token", last)
	}
}

func TestSameMetricsOrigin(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://prom.example.com:9090", "https://prom.example.com:9090/api", true},
		{"https://Prom.Example.com:9090", "HTTPS://prom.example.COM:9090", true},
		{"https://prom.example.com:9090", "https://prom.example.com:9091", false},
		{"https://prom.example.com", "https://prom.example.com:443", false},
		{"https://prom.example.com", "https://evil.example.com", false},
		{"https://prom.example.com", "http://prom.example.com", false},
		{"https://prom.example.com", "::not a url", false},
		{"", "https://prom.example.com", false},
	}
	for _, c := range cases {
		if got := sameMetricsOrigin(c.a, c.b); got != c.want {
			t.Errorf("sameMetricsOrigin(%q, %q) = %v; want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestClusterMetrics_PutExplicitEmptyTokenClears(t *testing.T) {
	f := newMetricsFixture(t)
	f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`)

	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`","token":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	if f.store.lastUpsertToken == nil || *f.store.lastUpsertToken != "" {
		t.Errorf("Upsert token = %v; want a pointer to \"\"", f.store.lastUpsertToken)
	}
	if data := decodeMetricsBinding(t, rec); data["hasToken"] != false {
		t.Errorf("hasToken = %v; want false", data["hasToken"])
	}
	probes := *f.probes
	if last := probes[len(probes)-1]; last.token != "" {
		t.Errorf("probe token = %q; want none after clearing", last.token)
	}
}

func TestClusterMetrics_PutRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, body, wantMsg string
	}{
		{"missing url", `{}`, "prometheusUrl is required"},
		{"http scheme", `{"prometheusUrl":"http://8.8.8.8:9090"}`, "prometheusUrl must be a valid HTTPS URL"},
		{"malformed", `{"prometheusUrl":"https://%zz"}`, "prometheusUrl must be a valid HTTPS URL"},
		{"no host", `{"prometheusUrl":"https://"}`, "prometheusUrl must be a valid HTTPS URL"},
		{"rfc1918", `{"prometheusUrl":"https://10.0.0.5:9090"}`, "prometheusUrl resolves to a private or reserved address"},
		{"loopback", `{"prometheusUrl":"https://127.0.0.1:9090"}`, "prometheusUrl resolves to a private or reserved address"},
		{"metadata", `{"prometheusUrl":"https://169.254.169.254"}`, "prometheusUrl resolves to a private or reserved address"},
		{"userinfo", `{"prometheusUrl":"https://u:p@8.8.8.8:9090"}`, "prometheusUrl must not contain credentials; send the token separately"},
		{"private alertmanager", `{"prometheusUrl":"` + testPublicProm + `","alertmanagerUrl":"https://192.168.1.10:9093"}`, "alertmanagerUrl resolves to a private or reserved address"},
		{"http alertmanager", `{"prometheusUrl":"` + testPublicProm + `","alertmanagerUrl":"http://8.8.4.4:9093"}`, "alertmanagerUrl must be a valid HTTPS URL"},
		{"oversized token", `{"prometheusUrl":"` + testPublicProm + `","token":"` + strings.Repeat("a", maxMetricsTokenBytes+1) + `"}`, "token too large (max 64KB)"},
		{"not json", `not json`, "invalid request body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newMetricsFixture(t)
			rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s; want 400", rec.Code, rec.Body.String())
			}
			if got := decodeMetricsErr(t, rec).Error.Message; got != tc.wantMsg {
				t.Errorf("message = %q; want %q", got, tc.wantMsg)
			}
			if len(*f.probes) != 0 || f.store.upsertCalled || len(f.evicter.list()) != 0 {
				t.Errorf("a rejected request probed, stored or evicted (probes %v, upsert %v)", *f.probes, f.store.upsertCalled)
			}
		})
	}
}

func TestClusterMetrics_OversizedBodyRejected(t *testing.T) {
	f := newMetricsFixture(t)
	body := `{"prometheusUrl":"` + testPublicProm + `","token":"` + strings.Repeat("a", 1<<20) + `"}`
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, body)
	if rec.Code != http.StatusBadRequest || f.store.upsertCalled {
		t.Fatalf("status = %d, upsert %v; want 400 and nothing stored", rec.Code, f.store.upsertCalled)
	}
}

func TestClusterMetrics_LocalClusterRejected(t *testing.T) {
	for _, id := range []string{"local", "local-row"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			t.Run(id+" "+method, func(t *testing.T) {
				f := newMetricsFixture(t)
				body := ""
				if method == http.MethodPut {
					body = `{"prometheusUrl":"` + testPublicProm + `"}`
				}
				rec := f.do(t, metricsAdmin, method, id, body)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d; want 400", rec.Code)
				}
				if got := decodeMetricsErr(t, rec).Error.Message; got != "the local cluster uses in-cluster discovery" {
					t.Errorf("message = %q", got)
				}
			})
		}
	}
}

func TestClusterMetrics_UnknownClusterIs404ClusterUnknown(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			f := newMetricsFixture(t)
			body := ""
			if method == http.MethodPut {
				body = `{"prometheusUrl":"` + testPublicProm + `"}`
			}
			rec := f.do(t, metricsAdmin, method, "ffffffffffffffffffffffffffffffff", body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d; want 404", rec.Code)
			}
			if got := decodeMetricsErr(t, rec).Error.Reason; got != "cluster_unknown" {
				t.Errorf("reason = %q; want cluster_unknown", got)
			}
		})
	}
}

func TestClusterMetrics_RegistryFailureIs503WithoutRawError(t *testing.T) {
	f := newMetricsFixture(t)
	f.srv.metricsClusters = &fakeClusterRegistry{err: errors.New("dial tcp 10.1.2.3:5432: connection refused")}
	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.1.2.3") {
		t.Errorf("body leaks the raw error: %s", rec.Body.String())
	}
}

func TestClusterMetrics_ProbeFailureIs502FixedMessage(t *testing.T) {
	f := newMetricsFixture(t)
	*f.probeFn = func(context.Context, string, string) error {
		return errors.New("dial tcp 8.8.8.8:9090: i/o timeout (internal detail)")
	}
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", rec.Code)
	}
	b := decodeMetricsErr(t, rec)
	if b.Error.Message != "could not reach Prometheus at the given URL" || b.Error.Reason != "unreachable" {
		t.Errorf("error = %+v", b.Error)
	}
	if strings.Contains(rec.Body.String(), "i/o timeout") || strings.Contains(rec.Body.String(), "internal detail") ||
		strings.Contains(rec.Body.String(), testToken) {
		t.Errorf("body leaks raw detail: %s", rec.Body.String())
	}
	if f.store.upsertCalled || len(f.evicter.list()) != 0 || len(f.audit.entries) != 0 {
		t.Errorf("a failed probe stored, evicted or audited")
	}
}

func TestClusterMetrics_GetWithoutBindingIs404MetricsNotConfigured(t *testing.T) {
	f := newMetricsFixture(t)
	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	b := decodeMetricsErr(t, rec)
	if b.Error.Reason != "metrics_not_configured" || b.Error.Message != "metrics are not configured for the selected cluster" {
		t.Errorf("error = %+v", b.Error)
	}
}

func TestClusterMetrics_GetReturnsShapeWithoutToken(t *testing.T) {
	f := newMetricsFixture(t)
	f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
		`{"prometheusUrl":"`+testPublicProm+`","token":"`+testToken+`","alertmanagerUrl":"https://8.8.4.4:9093"}`)

	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testToken) || strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("GET exposes the token: %s", rec.Body.String())
	}
	data := decodeMetricsBinding(t, rec)
	if data["clusterId"] != testRemoteID || data["prometheusUrl"] != testPublicProm || data["hasToken"] != true ||
		data["alertmanagerUrl"] != "https://8.8.4.4:9093" || data["updatedAt"] != "2026-10-10T12:00:00Z" {
		t.Errorf("data = %v", data)
	}
}

func TestClusterMetrics_DeleteThen404(t *testing.T) {
	f := newMetricsFixture(t)
	f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`"}`)
	f.audit.entries = nil
	f.evicter.evicted = nil

	rec := f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d body %q; want 204 with no body", rec.Code, rec.Body.String())
	}
	if ev := f.evicter.list(); len(ev) != 1 || ev[0] != testRemoteID {
		t.Errorf("evicted = %v; want [%s]", ev, testRemoteID)
	}
	if len(f.audit.entries) != 1 || f.audit.entries[0].Action != audit.ActionDelete ||
		f.audit.entries[0].ResourceKind != "cluster-metrics" {
		t.Errorf("audit = %+v; want one delete of cluster-metrics", f.audit.entries)
	}

	rec = f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, "")
	if rec.Code != http.StatusNotFound || decodeMetricsErr(t, rec).Error.Reason != "metrics_not_configured" {
		t.Errorf("second DELETE = %d %s; want 404 metrics_not_configured", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET after delete = %d; want 404", rec.Code)
	}
}

func TestClusterMetrics_NoDatabaseIs503(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			f := newMetricsFixture(t)
			f.srv.ClusterMetricsStore = nil
			f.srv.metricsClusters = nil // and no ClusterStore: the production no-DB shape
			body := ""
			if method == http.MethodPut {
				body = `{"prometheusUrl":"` + testPublicProm + `"}`
			}
			rec := f.do(t, metricsAdmin, method, testRemoteID, body)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d; want 503", rec.Code)
			}
			if got := decodeMetricsErr(t, rec).Error.Message; got != "metrics bindings require a database" {
				t.Errorf("message = %q", got)
			}
		})
	}
}

func TestClusterMetrics_NonAdminForbidden(t *testing.T) {
	viewer := &auth.User{ID: "u-1", Username: "viewer", Provider: "local", Roles: []string{"user"}}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			f := newMetricsFixture(t)
			rec := f.do(t, viewer, method, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`"}`)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d; want 403", rec.Code)
			}
			if f.store.upsertCalled || len(*f.probes) != 0 {
				t.Errorf("non-admin request reached the handler")
			}
		})
	}
}

func TestClusterMetrics_NilResolverIsSafe(t *testing.T) {
	f := newMetricsFixture(t)
	f.srv.MetricsResolver = nil
	if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"prometheusUrl":"`+testPublicProm+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d; want 200", rec.Code)
	}
	if rec := f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d; want 204", rec.Code)
	}
}

// The resolver must see a missing binding as monitoring.ErrNoMetricsBinding
// (which the monitoring routes answer as 404 metrics_not_configured) and a
// database failure unchanged, so it is classified as one.
func TestMetricsBindingReader(t *testing.T) {
	fs := newFakeMetricsStore()
	tok := testToken
	if _, err := fs.Upsert(context.Background(), testRemoteID, testPublicProm, "https://8.8.4.4:9093", &tok, ""); err != nil {
		t.Fatal(err)
	}
	r := metricsBindingReader{store: fs}

	b, err := r.GetMetricsBinding(context.Background(), testRemoteID)
	if err != nil {
		t.Fatalf("GetMetricsBinding: %v", err)
	}
	want := monitoring.MetricsBinding{ClusterID: testRemoteID, PrometheusURL: testPublicProm, AlertmanagerURL: "https://8.8.4.4:9093", Token: testToken}
	if b != want {
		t.Errorf("binding = %+v; want %+v", b, want)
	}

	if _, err := r.GetMetricsBinding(context.Background(), "other"); !errors.Is(err, monitoring.ErrNoMetricsBinding) {
		t.Errorf("missing binding err = %v; want ErrNoMetricsBinding", err)
	}

	dbErr := errors.New("db down")
	fs.getErr = dbErr
	if _, err := r.GetMetricsBinding(context.Background(), testRemoteID); !errors.Is(err, dbErr) || errors.Is(err, monitoring.ErrNoMetricsBinding) {
		t.Errorf("db failure err = %v; want the store error unchanged", err)
	}
}

// Deregistering a cluster evicts its cached Prometheus client. The delete
// itself needs a real ClusterStore, so this pins the hook the handler calls.
func TestEvictMetricsClient(t *testing.T) {
	ev := &recordingEvicter{}
	s := &Server{MetricsResolver: ev}
	s.evictMetricsClient(testRemoteID)
	if got := ev.list(); len(got) != 1 || got[0] != testRemoteID {
		t.Errorf("evicted = %v", got)
	}
	(&Server{}).evictMetricsClient(testRemoteID) // nil resolver: no panic
}
