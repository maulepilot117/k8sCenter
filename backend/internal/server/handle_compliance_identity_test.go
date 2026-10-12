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

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/store"
)

// fakeComplianceIdentityStore mirrors the real store's revision rule: a new
// revision only when the row is new or the username or groups differ.
type fakeComplianceIdentityStore struct {
	mu          sync.Mutex
	rows        map[string]store.ComplianceIdentity
	nextRev     int64
	upsertCalls int
	getErr      error
}

func newFakeComplianceIdentityStore() *fakeComplianceIdentityStore {
	return &fakeComplianceIdentityStore{rows: map[string]store.ComplianceIdentity{}}
}

func (f *fakeComplianceIdentityStore) Get(_ context.Context, id string) (store.ComplianceIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return store.ComplianceIdentity{}, f.getErr
	}
	row, ok := f.rows[id]
	if !ok {
		return store.ComplianceIdentity{}, store.ErrComplianceIdentityNotFound
	}
	return row, nil
}

func (f *fakeComplianceIdentityStore) Upsert(_ context.Context, id, username string, groups []string, updatedBy string) (store.ComplianceIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	row, ok := f.rows[id]
	if !ok || row.Username != username || strings.Join(row.Groups, "\x00") != strings.Join(groups, "\x00") {
		f.nextRev++
		row.Revision = f.nextRev
	}
	row.ClusterID, row.Username, row.Groups, row.UpdatedBy = id, username, groups, updatedBy
	row.UpdatedAt = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	f.rows[id] = row
	return row, nil
}

func (f *fakeComplianceIdentityStore) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return store.ErrComplianceIdentityNotFound
	}
	delete(f.rows, id)
	return nil
}

type fakeImpersonationChecker struct {
	err   error
	calls []string
}

func (c *fakeImpersonationChecker) CanImpersonate(_ context.Context, clusterID, username string, groups []string) error {
	c.calls = append(c.calls, fmt.Sprintf("%s|%s|%s", clusterID, username, strings.Join(groups, ",")))
	return c.err
}

type fakeSnapshotter struct {
	mu    sync.Mutex
	calls []string
}

func (s *fakeSnapshotter) SnapshotNow(_ context.Context, clusterID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, clusterID)
}

type complianceIdentityFixture struct {
	srv      *Server
	router   chi.Router
	store    *fakeComplianceIdentityStore
	checker  *fakeImpersonationChecker
	snapshot *fakeSnapshotter
	audit    *captureAudit
}

func newComplianceIdentityFixture(t *testing.T) *complianceIdentityFixture {
	t.Helper()
	fs := newFakeComplianceIdentityStore()
	ck := &fakeImpersonationChecker{}
	sn := &fakeSnapshotter{}
	ca := &captureAudit{}
	s := &Server{
		Config:                  &config.Config{ClusterID: "local"},
		Logger:                  slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuditLogger:             ca,
		ComplianceIdentityStore: fs,
		impersonationChecker:    ck,
		complianceSnapshotter:   sn,
		metricsClusters: &fakeClusterRegistry{records: map[string]*store.ClusterRecord{
			testRemoteID: {ID: testRemoteID, Name: "remote-a"},
			"local-row":  {ID: "local-row", Name: "local", IsLocal: true},
		}},
	}
	r := chi.NewRouter()
	s.registerClusterRoutes(r)
	return &complianceIdentityFixture{srv: s, router: r, store: fs, checker: ck, snapshot: sn, audit: ca}
}

func (f *complianceIdentityFixture) do(t *testing.T, user *auth.User, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/clusters/"+id+"/compliance-identity", rd)
	if user != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func decodeIdentityData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var b struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}
	return b.Data
}

func TestComplianceIdentity_LocalClusterRefused(t *testing.T) {
	for _, id := range []string{"local", "local-row"} {
		f := newComplianceIdentityFixture(t)
		rec := f.do(t, metricsAdmin, http.MethodPut, id, `{"username":"bot"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d; want 400", id, rec.Code)
		}
		if f.store.upsertCalls != 0 || len(f.checker.calls) != 0 {
			t.Errorf("%s: local cluster reached the store or the check", id)
		}
	}
}

func TestComplianceIdentity_UnknownCluster404(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	rec := f.do(t, metricsAdmin, http.MethodGet, "ffffffffffffffffffffffffffffffff", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if got := decodeMetricsErr(t, rec).Error.Reason; got != "cluster_unknown" {
		t.Errorf("reason = %q; want cluster_unknown", got)
	}
}

func TestComplianceIdentity_RegistryFailureHidesRawError(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	f.srv.metricsClusters = &fakeClusterRegistry{err: errors.New("dial tcp 10.1.2.3:5432: connection refused")}
	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.1.2.3") {
		t.Errorf("response leaks the raw error: %s", rec.Body.String())
	}
}

func TestComplianceIdentity_ValidationRefusals(t *testing.T) {
	long := strings.Repeat("x", maxComplianceUsernameLen+1)
	groups := make([]string, maxComplianceGroups+1)
	for i := range groups {
		groups[i] = fmt.Sprintf("g%d", i)
	}
	tooMany, _ := json.Marshal(map[string]any{"username": "bot", "groups": groups})
	cases := map[string]string{
		"blank username":   `{"username":"   "}`,
		"missing username": `{"groups":["a"]}`,
		"long username":    `{"username":"` + long + `"}`,
		"blank group":      `{"username":"bot","groups":["a"," "]}`,
		"long group":       `{"username":"bot","groups":["` + long + `"]}`,
		"too many groups":  string(tooMany),
		"bad json":         `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newComplianceIdentityFixture(t)
			rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s; want 400", rec.Code, rec.Body.String())
			}
			if f.store.upsertCalls != 0 || len(f.checker.calls) != 0 || len(f.snapshot.calls) != 0 {
				t.Errorf("a refused request reached the check, store or snapshot")
			}
		})
	}
}

func TestComplianceIdentity_DeniedImpersonationIs422AndStoresNothing(t *testing.T) {
	cases := []struct {
		name, username, body string
		err                  error
		wantMsg              string
	}{
		{"user", "bot", `{"username":"bot"}`,
			fmt.Errorf("%w: impersonate users %q", k8s.ErrImpersonationDenied, "bot"), msgComplianceDeniedUser},
		{"service account", "system:serviceaccount:ops:scanner", `{"username":"system:serviceaccount:ops:scanner"}`,
			fmt.Errorf("%w: impersonate serviceaccounts %q", k8s.ErrImpersonationDenied, "scanner"), msgComplianceDeniedSA},
		{"group", "bot", `{"username":"bot","groups":["auditors"]}`,
			fmt.Errorf("%w: impersonate groups %q", k8s.ErrImpersonationDenied, "auditors"), msgComplianceDeniedGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newComplianceIdentityFixture(t)
			f.checker.err = tc.err
			rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d; want 422", rec.Code)
			}
			e := decodeMetricsErr(t, rec).Error
			if e.Reason != "impersonation_denied" || e.Message != tc.wantMsg {
				t.Errorf("reason %q message %q; want impersonation_denied / %q", e.Reason, e.Message, tc.wantMsg)
			}
			if strings.Contains(rec.Body.String(), "serviceaccounts \"") || strings.Contains(rec.Body.String(), "may not impersonate") {
				t.Errorf("response echoes raw review text: %s", rec.Body.String())
			}
			if f.store.upsertCalls != 0 || len(f.snapshot.calls) != 0 || len(f.audit.entries) != 0 {
				t.Errorf("denied save stored, snapshotted or audited")
			}
		})
	}
}

func TestComplianceIdentity_CheckFailureMapsToTargetError(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	f.checker.err = k8s.TargetError{Err: errors.New("dial tcp 10.9.9.9:6443: i/o timeout")}
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"username":"bot"}`)
	if rec.Code == http.StatusOK || rec.Code == http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; want a target error", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.9.9.9") {
		t.Errorf("response leaks the raw error: %s", rec.Body.String())
	}
	if f.store.upsertCalls != 0 {
		t.Errorf("stored despite an unfinished check")
	}
}

// AE2: a valid PUT stores the identity, starts one snapshot and audits.
func TestComplianceIdentity_PutStoresSnapshotsAudits(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID,
		`{"username":"  bot ","groups":["viewers","auditors","viewers"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	data := decodeIdentityData(t, rec)
	if data["username"] != "bot" || data["revision"] != float64(1) || data["updatedAt"] != "2026-10-12T09:00:00Z" {
		t.Errorf("data = %v", data)
	}
	if g, _ := json.Marshal(data["groups"]); string(g) != `["auditors","viewers"]` {
		t.Errorf("groups = %s; want sorted, de-duplicated", g)
	}
	if len(data) != 4 {
		t.Errorf("data has keys %v; want exactly username, groups, revision, updatedAt", data)
	}
	if got := f.checker.calls; len(got) != 1 || got[0] != testRemoteID+"|bot|auditors,viewers" {
		t.Errorf("check calls = %v", got)
	}
	if row := f.store.rows[testRemoteID]; row.UpdatedBy != "admin" {
		t.Errorf("updatedBy = %q; want admin", row.UpdatedBy)
	}
	if got := f.snapshot.calls; len(got) != 1 || got[0] != testRemoteID {
		t.Errorf("snapshot calls = %v; want one for the cluster", got)
	}
	if len(f.audit.entries) != 1 {
		t.Fatalf("audit entries = %+v; want one", f.audit.entries)
	}
	e := f.audit.entries[0]
	if e.ResourceKind != "cluster-compliance-identity" || e.ResourceName != testRemoteID || e.User != "admin" {
		t.Errorf("audit entry = %+v", e)
	}
	if !strings.Contains(e.Detail, "bot") || !strings.Contains(e.Detail, "2 groups") {
		t.Errorf("audit detail = %q; want username and group count", e.Detail)
	}
}

func TestComplianceIdentity_SecondPutWithNewGroupsBumpsRevision(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	first := decodeIdentityData(t, f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"username":"bot","groups":["a"]}`))
	same := decodeIdentityData(t, f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"username":"bot","groups":["a"]}`))
	changed := decodeIdentityData(t, f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"username":"bot","groups":["b","a"]}`))
	if same["revision"] != first["revision"] {
		t.Errorf("unchanged PUT moved the revision: %v -> %v", first["revision"], same["revision"])
	}
	if changed["revision"].(float64) <= first["revision"].(float64) {
		t.Errorf("new groups did not bump the revision: %v -> %v", first["revision"], changed["revision"])
	}
}

func TestComplianceIdentity_NilRecorderIsSafe(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	f.srv.complianceSnapshotter = nil
	if rec := f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"username":"bot"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
}

func TestComplianceIdentity_GetWithoutRowIs404NotConfigured(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if got := decodeMetricsErr(t, rec).Error.Reason; got != "compliance_history_not_configured" {
		t.Errorf("reason = %q", got)
	}
}

func TestComplianceIdentity_GetReturnsStoredIdentity(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"username":"bot"}`)
	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	data := decodeIdentityData(t, rec)
	if data["username"] != "bot" || data["revision"] != float64(1) {
		t.Errorf("data = %v", data)
	}
	if g, _ := json.Marshal(data["groups"]); string(g) != `[]` {
		t.Errorf("groups = %s; want []", g)
	}
}

func TestComplianceIdentity_DeleteRemovesAndAudits(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	f.do(t, metricsAdmin, http.MethodPut, testRemoteID, `{"username":"bot"}`)
	f.audit.entries = nil

	rec := f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204", rec.Code)
	}
	if len(f.audit.entries) != 1 || f.audit.entries[0].ResourceKind != "cluster-compliance-identity" ||
		f.audit.entries[0].ResourceName != testRemoteID {
		t.Errorf("audit entries = %+v", f.audit.entries)
	}
	if rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET after delete = %d; want 404", rec.Code)
	}
	rec = f.do(t, metricsAdmin, http.MethodDelete, testRemoteID, "")
	if rec.Code != http.StatusNotFound || decodeMetricsErr(t, rec).Error.Reason != "compliance_history_not_configured" {
		t.Errorf("second DELETE = %d %s; want 404 compliance_history_not_configured", rec.Code, rec.Body.String())
	}
}

func TestComplianceIdentity_NonAdminForbidden(t *testing.T) {
	viewer := &auth.User{ID: "u-1", Username: "viewer", Provider: "local", Roles: []string{"user"}}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			f := newComplianceIdentityFixture(t)
			rec := f.do(t, viewer, method, testRemoteID, `{"username":"bot"}`)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d; want 403", rec.Code)
			}
			if f.store.upsertCalls != 0 || len(f.checker.calls) != 0 {
				t.Errorf("non-admin request reached the handler")
			}
		})
	}
}

func TestComplianceIdentity_NoDatabaseIs503(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			f := newComplianceIdentityFixture(t)
			f.srv.ComplianceIdentityStore = nil
			f.srv.metricsClusters = nil
			rec := f.do(t, metricsAdmin, method, testRemoteID, `{"username":"bot"}`)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d; want 503", rec.Code)
			}
			if got := decodeMetricsErr(t, rec).Error.Message; got != msgComplianceNoDatabase {
				t.Errorf("message = %q", got)
			}
		})
	}
}

func TestComplianceIdentity_StoreFailureHidesRawError(t *testing.T) {
	f := newComplianceIdentityFixture(t)
	f.store.getErr = errors.New("pq: password authentication failed for user kubecenter")
	rec := f.do(t, metricsAdmin, http.MethodGet, testRemoteID, "")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("got %d %s; want 503 without the raw error", rec.Code, rec.Body.String())
	}
}
