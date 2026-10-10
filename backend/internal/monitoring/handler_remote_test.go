package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// remoteFixture is a monitoring handler whose local Discoverer points at one
// fake Prometheus and whose resolver binds "remote-1" to another, with a
// bearer token. Every remote test asserts the local one was never hit.
type remoteFixture struct {
	h      *Handler
	local  *fakePrometheus
	remote *fakePrometheus
	binds  *fakeBindings
}

func newRemoteFixture(t *testing.T) *remoteFixture {
	t.Helper()
	local := newFakePrometheus(t, "1")
	remote := newFakePrometheus(t, "42")
	d := localDiscoverer(t, local)
	d.status = &MonitoringStatus{
		Prometheus:  ComponentStatus{Available: true, URL: "http://prometheus.monitoring:9090"},
		Grafana:     ComponentStatus{Available: true, URL: "http://grafana.monitoring"},
		Dashboards:  DashboardStatus{Provisioned: true, Count: 7},
		HasOperator: true,
	}
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL, Token: "remote-token"})
	return &remoteFixture{
		h:      &Handler{Discoverer: d, Resolver: newTestResolver(d, b), Logger: testLogger()},
		local:  local,
		remote: remote,
		binds:  b,
	}
}

func (f *remoteFixture) assertLocalUntouched(t *testing.T) {
	t.Helper()
	if n := f.local.hits.Load(); n != 0 {
		t.Fatalf("the local Prometheus served %d request(s) under a remote selection", n)
	}
}

func remoteRequest(target, clusterID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	return req.WithContext(middleware.WithClusterID(req.Context(), clusterID))
}

// slugRequest routes target through a chi wildcard so chi.URLParam(r, "*")
// carries the slug, as it does in production.
func serveSlug(h *Handler, req *http.Request) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get("/api/v1/monitoring/queries/*", h.HandleSlugQuery)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

type errorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
		Detail  string `json:"detail"`
	} `json:"error"`
}

func decodeErr(t *testing.T, rr *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return e
}

func assertNotConfigured(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rr.Code, rr.Body.String())
	}
	e := decodeErr(t, rr)
	if e.Error.Reason != "metrics_not_configured" || e.Error.Message != "metrics are not configured for the selected cluster" {
		t.Fatalf("want metrics_not_configured with the fixed message, got %+v", e.Error)
	}
}

func TestHandleQuery_RemoteUsesBinding(t *testing.T) {
	f := newRemoteFixture(t)
	rr := httptest.NewRecorder()
	f.h.HandleQuery(rr, remoteRequest("/api/v1/monitoring/query?query=up", "remote-1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"42"`) {
		t.Fatalf("want the remote Prometheus's value, got %s", rr.Body.String())
	}
	if got := f.remote.auth(); got != "Bearer remote-token" {
		t.Fatalf("Authorization = %q, want the binding's bearer token", got)
	}
	f.assertLocalUntouched(t)
}

func TestHandleQuery_RemoteWithoutBindingIsNotConfigured(t *testing.T) {
	f := newRemoteFixture(t)
	rr := httptest.NewRecorder()
	f.h.HandleQuery(rr, remoteRequest("/api/v1/monitoring/query?query=up", "remote-2"))
	assertNotConfigured(t, rr)
	f.assertLocalUntouched(t)
}

func TestHandleQuery_NilResolverNeverServesRemoteFromLocal(t *testing.T) {
	f := newRemoteFixture(t)
	f.h.Resolver = nil
	rr := httptest.NewRecorder()
	f.h.HandleQuery(rr, remoteRequest("/api/v1/monitoring/query?query=up", "remote-1"))
	assertNotConfigured(t, rr)
	f.assertLocalUntouched(t)
}

func TestHandleQuery_NilResolverServesLocal(t *testing.T) {
	f := newRemoteFixture(t)
	f.h.Resolver = nil
	rr := httptest.NewRecorder()
	f.h.HandleQuery(rr, remoteRequest("/api/v1/monitoring/query?query=up", "local"))
	if rr.Code != http.StatusOK || f.local.hits.Load() != 1 {
		t.Fatalf("want 200 from the local Prometheus, got %d (hits=%d)", rr.Code, f.local.hits.Load())
	}
}

func TestHandleQuery_RemoteQueryFailureIsFixed502(t *testing.T) {
	f := newRemoteFixture(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"status":"error","errorType":"execution","error":"INTERNAL-DETAIL at 10.9.8.7"}`)
	}))
	t.Cleanup(failing.Close)
	f.binds.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: failing.URL})

	rr := httptest.NewRecorder()
	f.h.HandleQuery(rr, remoteRequest("/api/v1/monitoring/query?query=up", "remote-1"))

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d (%s)", rr.Code, rr.Body.String())
	}
	e := decodeErr(t, rr)
	if e.Error.Message != "the metrics query failed on the selected cluster" {
		t.Fatalf("want the fixed message, got %q", e.Error.Message)
	}
	body := rr.Body.String()
	for _, leak := range []string{"INTERNAL-DETAIL", "10.9.8.7", strings.TrimPrefix(failing.URL, "http://")} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q: %s", leak, body)
		}
	}
	f.assertLocalUntouched(t)
}

func TestHandleQuery_UnknownClusterIsTargetError(t *testing.T) {
	f := newRemoteFixture(t)
	f.binds.err = fmt.Errorf("reading cluster: %w", pgx.ErrNoRows)
	rr := httptest.NewRecorder()
	f.h.HandleQuery(rr, remoteRequest("/api/v1/monitoring/query?query=up", "ghost"))
	if rr.Code != http.StatusNotFound || decodeErr(t, rr).Error.Reason != "cluster_unknown" {
		t.Fatalf("want 404 cluster_unknown, got %d (%s)", rr.Code, rr.Body.String())
	}
	f.assertLocalUntouched(t)
}

func TestHandleQueryRange_RemoteUsesBinding(t *testing.T) {
	f := newRemoteFixture(t)
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-time.Hour)
	target := fmt.Sprintf("/api/v1/monitoring/query_range?query=up&start=%s&end=%s&step=60s",
		start.Format(time.RFC3339), end.Format(time.RFC3339))
	rr := httptest.NewRecorder()
	f.h.HandleQueryRange(rr, remoteRequest(target, "remote-1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if f.remote.auth() != "Bearer remote-token" {
		t.Fatalf("Authorization = %q", f.remote.auth())
	}
	f.assertLocalUntouched(t)
}

func TestHandleQueryRange_RemoteWithoutBindingIsNotConfigured(t *testing.T) {
	f := newRemoteFixture(t)
	rr := httptest.NewRecorder()
	f.h.HandleQueryRange(rr, remoteRequest("/api/v1/monitoring/query_range?query=up", "remote-2"))
	assertNotConfigured(t, rr)
}

func TestHandleTemplateQuery_RemoteUsesBinding(t *testing.T) {
	f := newRemoteFixture(t)
	var name string
	var params []string
	for n, tmpl := range QueryTemplates {
		name = n
		for _, v := range tmpl.Variables {
			params = append(params, v+"=default")
		}
		break
	}
	target := "/api/v1/monitoring/templates/query?name=" + name
	if len(params) > 0 {
		target += "&" + strings.Join(params, "&")
	}
	rr := httptest.NewRecorder()
	f.h.HandleTemplateQuery(rr, remoteRequest(target, "remote-1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if f.remote.auth() != "Bearer remote-token" {
		t.Fatalf("Authorization = %q", f.remote.auth())
	}
	f.assertLocalUntouched(t)
}

func TestHandleTemplateQuery_RemoteWithoutBindingIsNotConfigured(t *testing.T) {
	f := newRemoteFixture(t)
	rr := httptest.NewRecorder()
	f.h.HandleTemplateQuery(rr, remoteRequest("/api/v1/monitoring/templates/query?name=x", "remote-2"))
	assertNotConfigured(t, rr)
}

func TestHandleSlugQuery_RemoteUsesBinding(t *testing.T) {
	f := newRemoteFixture(t)
	rr := serveSlug(f.h, remoteRequest("/api/v1/monitoring/queries/cluster/top-consumers-cpu", "remote-1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if f.remote.auth() != "Bearer remote-token" {
		t.Fatalf("Authorization = %q", f.remote.auth())
	}
	f.assertLocalUntouched(t)
}

func TestHandleSlugQuery_RemoteWithoutBindingIsNotConfigured(t *testing.T) {
	f := newRemoteFixture(t)
	rr := serveSlug(f.h, remoteRequest("/api/v1/monitoring/queries/cluster/top-consumers-cpu", "remote-2"))
	assertNotConfigured(t, rr)
	f.assertLocalUntouched(t)
}

func TestHandleStatus_RemoteAnswersFromBindingOnly(t *testing.T) {
	f := newRemoteFixture(t)
	rr := httptest.NewRecorder()
	f.h.HandleStatus(rr, remoteRequest("/api/v1/monitoring/status", "remote-1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data MonitoringStatus `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := resp.Data
	if !got.Prometheus.Available || got.Grafana.Available || got.Dashboards.Provisioned || got.HasOperator {
		t.Fatalf("want prometheus available only, got %+v", got)
	}
	if strings.Contains(rr.Body.String(), "monitoring:9090") || strings.Contains(rr.Body.String(), "grafana.monitoring") {
		t.Fatalf("the remote status leaks the local status: %s", rr.Body.String())
	}
	f.assertLocalUntouched(t)
}

func TestHandleStatus_RemoteWithoutBindingIsUnavailable(t *testing.T) {
	f := newRemoteFixture(t)
	rr := httptest.NewRecorder()
	f.h.HandleStatus(rr, remoteRequest("/api/v1/monitoring/status", "remote-2"))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data MonitoringStatus `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Prometheus.Available || resp.Data.Grafana.Available || resp.Data.HasOperator {
		t.Fatalf("want everything unavailable, got %+v", resp.Data)
	}
}

func TestHandleStatus_LocalUnchanged(t *testing.T) {
	f := newRemoteFixture(t)
	for _, id := range []string{"", "local"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/status", nil)
		if id != "" {
			req = req.WithContext(middleware.WithClusterID(req.Context(), id))
		}
		f.h.HandleStatus(rr, req)
		var resp struct {
			Data MonitoringStatus `json:"data"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !resp.Data.Grafana.Available || !resp.Data.HasOperator || resp.Data.Dashboards.Count != 7 {
			t.Fatalf("cluster %q: want the local Discoverer status, got %+v", id, resp.Data)
		}
	}
}

func TestHandleRediscover_RemoteRefused(t *testing.T) {
	f := newRemoteFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitoring/rediscover", nil)
	req = req.WithContext(middleware.WithClusterID(context.Background(), "remote-1"))
	rr := httptest.NewRecorder()
	f.h.HandleRediscover(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}
