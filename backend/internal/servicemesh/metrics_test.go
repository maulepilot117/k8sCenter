package servicemesh

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/monitoring"
)

// --- template render tests -------------------------------------------------

// TestIstioTemplates_RenderLabels confirms the Istio templates resolve
// to queries with the expected labels and no unsubstituted $ tokens.
func TestIstioTemplates_RenderLabels(t *testing.T) {
	vars := map[string]string{"svc": "cart", "ns": "shop"}
	rps, err := istioTemplates.rps.Render(vars)
	if err != nil {
		t.Fatalf("render rps: %v", err)
	}
	if strings.Contains(rps, "$") {
		t.Errorf("rps has unsubstituted variable: %q", rps)
	}
	if !strings.Contains(rps, `destination_service_name="cart"`) {
		t.Errorf("rps missing destination_service_name: %q", rps)
	}
	if !strings.Contains(rps, `destination_service_namespace="shop"`) {
		t.Errorf("rps missing destination_service_namespace: %q", rps)
	}

	errNum, err := istioTemplates.errorNum.Render(vars)
	if err != nil {
		t.Fatalf("render errorNum: %v", err)
	}
	if !strings.Contains(errNum, `response_code=~"5.."`) {
		t.Errorf("errorNum should filter 5xx responses: %q", errNum)
	}

	p95, err := istioTemplates.latencyP("0.95").Render(vars)
	if err != nil {
		t.Fatalf("render p95: %v", err)
	}
	if !strings.Contains(p95, "histogram_quantile(0.95") {
		t.Errorf("p95 should use histogram_quantile(0.95): %q", p95)
	}
	if !strings.Contains(p95, "istio_request_duration_milliseconds_bucket") {
		t.Errorf("p95 should use the bucket histogram: %q", p95)
	}
}

// TestLinkerdTemplates_RenderLabels confirms the Linkerd templates use
// direction=inbound and the authority-shaped selector.
func TestLinkerdTemplates_RenderLabels(t *testing.T) {
	vars := map[string]string{"svc": "cart", "ns": "shop"}
	rps, err := linkerdTemplates.rps.Render(vars)
	if err != nil {
		t.Fatalf("render rps: %v", err)
	}
	if !strings.Contains(rps, `direction="inbound"`) {
		t.Errorf("rps should filter inbound traffic: %q", rps)
	}
	if !strings.Contains(rps, `authority="cart.shop.svc.cluster.local"`) {
		t.Errorf("rps should build authority from svc+ns: %q", rps)
	}
	errNum, err := linkerdTemplates.errorNum.Render(vars)
	if err != nil {
		t.Fatalf("render errorNum: %v", err)
	}
	if !strings.Contains(errNum, `classification="failure"`) {
		t.Errorf("errorNum should filter classification=failure: %q", errNum)
	}
}

// TestTemplateRejectsInjection: the monitoring.QueryTemplate validator
// refuses values containing quotes, spaces, or PromQL operators. This
// is the plan's "label-substitution rejects a namespace with \" in it"
// scenario — the injection attempt surfaces as an error upstream.
func TestTemplateRejectsInjection(t *testing.T) {
	vars := map[string]string{"svc": "cart", "ns": `shop"}) or vector(1) #`}
	if _, err := istioTemplates.rps.Render(vars); err == nil {
		t.Error("render accepted an invalid namespace with embedded quotes")
	}
}

// --- nanToZero -------------------------------------------------------------

func TestNanToZero(t *testing.T) {
	if got := nanToZero(0); got != 0 {
		t.Errorf("nanToZero(0) = %v", got)
	}
	if got := nanToZero(1.5); got != 1.5 {
		t.Errorf("nanToZero(1.5) = %v", got)
	}
	if got := nanToZero(math.NaN()); got != 0 {
		t.Errorf("nanToZero(NaN) = %v, want 0", got)
	}
	if got := nanToZero(math.Inf(1)); got != 0 {
		t.Errorf("nanToZero(+Inf) = %v, want 0", got)
	}
	if got := nanToZero(math.Inf(-1)); got != 0 {
		t.Errorf("nanToZero(-Inf) = %v, want 0", got)
	}
}

// --- goldenSignalsForService (nil PromQL client) ---------------------------

// TestGoldenSignalsForService_NilClientReportsUnavailable covers the
// plan's edge case: Prometheus offline → Available=false, Reason
// populated, zero values. Never returns an error to the caller.
func TestGoldenSignalsForService_NilClientReportsUnavailable(t *testing.T) {
	got, err := goldenSignalsForService(context.Background(), nil, MeshIstio, "shop", "cart")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Available {
		t.Error("Available should be false when client is nil")
	}
	if got.Reason != "metrics_unavailable" {
		t.Errorf("Reason = %q, want metrics_unavailable", got.Reason)
	}
}

// TestGoldenSignalsForService_UnsupportedMesh rejects unknown meshes
// before issuing any Prometheus work.
func TestGoldenSignalsForService_UnsupportedMesh(t *testing.T) {
	_, err := goldenSignalsForService(context.Background(), nil, MeshType("kuma"), "shop", "cart")
	if err == nil {
		t.Fatal("expected error for unsupported mesh")
	}
	if !strings.Contains(err.Error(), "unsupported mesh") {
		t.Errorf("error = %v, want unsupported mesh", err)
	}
}

// TestGoldenSignalsForService_RenderFailureSurfacesError: invalid
// namespace (embedded quotes) surfaces as a render-layer error so the
// handler can emit 400. We build a Prometheus client against an unreachable
// address — Render fires before Query would ever touch the network.
func TestGoldenSignalsForService_RenderFailureSurfacesError(t *testing.T) {
	pc, err := monitoring.NewPrometheusClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("prom client: %v", err)
	}
	_, err = goldenSignalsForService(context.Background(), pc, MeshIstio, `bad"ns`, "cart")
	if err == nil {
		t.Fatal("expected render error for namespace with quotes")
	}
	if !strings.Contains(err.Error(), "render") {
		t.Errorf("error = %v, want render wrapping", err)
	}
}

// --- resolveMeshParam ------------------------------------------------------

func TestResolveMeshParam(t *testing.T) {
	istioOnly := MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true}}
	linkerdOnly := MeshStatus{Detected: MeshLinkerd, Linkerd: &MeshInfo{Installed: true}}
	both := MeshStatus{Detected: MeshBoth, Istio: &MeshInfo{Installed: true}, Linkerd: &MeshInfo{Installed: true}}
	none := MeshStatus{Detected: MeshNone}

	cases := []struct {
		name    string
		param   string
		status  MeshStatus
		want    MeshType
		wantErr bool
	}{
		{"empty + istio only → istio", "", istioOnly, MeshIstio, false},
		{"empty + linkerd only → linkerd", "", linkerdOnly, MeshLinkerd, false},
		{"empty + none → error", "", none, MeshNone, true},
		{"empty + both → error (ambiguous)", "", both, MeshNone, true},
		{"explicit istio + istio installed", "istio", istioOnly, MeshIstio, false},
		{"explicit istio + not installed → error", "istio", linkerdOnly, MeshNone, true},
		{"explicit linkerd + linkerd installed", "linkerd", linkerdOnly, MeshLinkerd, false},
		{"explicit linkerd + not installed → error", "linkerd", istioOnly, MeshNone, true},
		{"bogus param → error", "osm", istioOnly, MeshNone, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveMeshParam(c.param, c.status)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mesh=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// --- handler tests ---------------------------------------------------------

// TestHandler_GoldenSignals_BadRequestMissingParams covers the plan's
// validation scenario — missing service / namespace → 400.
func TestHandler_GoldenSignals_BadRequestMissingParams(t *testing.T) {
	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true}}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}
	w := doGoldenSignals(t, h, url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// TestHandler_GoldenSignals_NoMeshDetected returns 400 with a guidance
// message so the frontend can surface a helpful empty state.
func TestHandler_GoldenSignals_NoMeshDetected(t *testing.T) {
	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshNone}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}
	w := doGoldenSignals(t, h, url.Values{"namespace": {"shop"}, "service": {"cart"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// TestHandler_GoldenSignals_AmbiguousDualMesh: both meshes installed
// and no explicit ?mesh= → 400 with ambiguity message.
func TestHandler_GoldenSignals_AmbiguousDualMesh(t *testing.T) {
	h := &Handler{
		Discoverer: seededDiscoverer(MeshStatus{
			Detected: MeshBoth,
			Istio:    &MeshInfo{Installed: true},
			Linkerd:  &MeshInfo{Installed: true},
		}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}
	w := doGoldenSignals(t, h, url.Values{"namespace": {"shop"}, "service": {"cart"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "both meshes") {
		t.Errorf("body should mention ambiguity: %q", w.Body.String())
	}
}

// TestHandler_GoldenSignals_Denied: user cannot list pods in namespace
// → 403. This diverges from the mTLS handler's silent empty-state
// because single-service metrics have no meaningful partial shape.
func TestHandler_GoldenSignals_Denied(t *testing.T) {
	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true}}),
		AccessChecker: resources.NewAlwaysDenyAccessChecker(),
		Logger:        slog.Default(),
	}
	w := doGoldenSignals(t, h, url.Values{"namespace": {"shop"}, "service": {"cart"}})
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// TestHandler_GoldenSignals_PrometheusOffline is the plan's graceful-
// degradation path: no Prometheus client → 200 with
// Signals.Available=false, Reason="metrics_unavailable".
func TestHandler_GoldenSignals_PrometheusOffline(t *testing.T) {
	h := &Handler{
		Discoverer:    seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true}}),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}
	w := doGoldenSignals(t, h, url.Values{"namespace": {"shop"}, "service": {"cart"}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var env struct {
		Data GoldenSignalsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Signals.Available {
		t.Error("Available should be false when Prometheus is offline")
	}
	if env.Data.Signals.Reason != "metrics_unavailable" {
		t.Errorf("Reason = %q, want metrics_unavailable", env.Data.Signals.Reason)
	}
}

// --- fan-out coverage -----------------------------------------------------

// TestGoldenSignalsForService_HappyPath exercises the six-query fan-out
// end-to-end with a fake Prometheus HTTP server. It asserts that every
// result lands in the right bucket (RPS, errorRate, P50/P95/P99), which
// is the regression coverage the Phase B review flagged as the largest
// testing gap: a mis-keying of "rps" vs "errorNum" would compile and
// pass every existing test.
func TestGoldenSignalsForService_HappyPath(t *testing.T) {
	srv := newFakePromServer(t, []promStub{
		// Order matters: more-specific fragments first so errorNum
		// (which contains istio_requests_total) doesn't collide with
		// the plain RPS template.
		{`response_code=~"5.."`, 2.5},     // errorNum
		{"istio_requests_total", 100},     // rps + errorDen
		{"histogram_quantile(0.50", 11.0}, // p50
		{"histogram_quantile(0.95", 55.0}, // p95
		{"histogram_quantile(0.99", 99.0}, // p99
	})
	defer srv.Close()
	pc, err := monitoring.NewPrometheusClient(srv.URL)
	if err != nil {
		t.Fatalf("prom client: %v", err)
	}

	got, gerr := goldenSignalsForService(context.Background(), pc, MeshIstio, "shop", "cart")
	if gerr != nil {
		t.Fatalf("unexpected error: %v", gerr)
	}
	if !got.Available {
		t.Fatalf("Available = false, want true")
	}
	if got.RPS != 100 {
		t.Errorf("RPS = %v, want 100", got.RPS)
	}
	if wantRate := 2.5 / 100.0; got.ErrorRate != wantRate {
		t.Errorf("ErrorRate = %v, want %v", got.ErrorRate, wantRate)
	}
	if got.P50Ms != 11 || got.P95Ms != 55 || got.P99Ms != 99 {
		t.Errorf("latencies = (%v, %v, %v), want (11, 55, 99)", got.P50Ms, got.P95Ms, got.P99Ms)
	}
}

// TestGoldenSignalsForService_PartialFailure drops one of the six
// queries and asserts the rest still populate the response. The Phase B
// review flagged this as a UX regression: p99 histogram_quantile is
// reliably the slowest query under load, and discarding RPS +
// error-rate because p99 errored out would blank exactly the signals
// operators need most during an incident.
func TestGoldenSignalsForService_PartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.FormValue("query")
		if strings.Contains(query, "histogram_quantile(0.99") {
			// Simulate a backend failure for p99 only.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error","errorType":"timeout","error":"query timed out"}`))
			return
		}
		var value float64
		switch {
		case strings.Contains(query, `response_code=~"5.."`):
			value = 1
		case strings.Contains(query, "istio_requests_total"):
			value = 50
		case strings.Contains(query, "histogram_quantile(0.50"):
			value = 7
		case strings.Contains(query, "histogram_quantile(0.95"):
			value = 23
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"%g"]}]}}`, value)
	}))
	defer srv.Close()
	pc, err := monitoring.NewPrometheusClient(srv.URL)
	if err != nil {
		t.Fatalf("prom client: %v", err)
	}

	got, gerr := goldenSignalsForService(context.Background(), pc, MeshIstio, "shop", "cart")
	if gerr != nil {
		t.Fatalf("unexpected error: %v", gerr)
	}
	if !got.Available {
		t.Fatalf("Available = false, want true (partial results should still count)")
	}
	if got.RPS != 50 {
		t.Errorf("RPS = %v, want 50", got.RPS)
	}
	if got.P50Ms != 7 || got.P95Ms != 23 {
		t.Errorf("P50/P95 = (%v, %v), want (7, 23)", got.P50Ms, got.P95Ms)
	}
	if got.P99Ms != 0 {
		t.Errorf("P99 = %v, want 0 (query failed so no value)", got.P99Ms)
	}
}

// TestGoldenSignalsForService_AllQueriesFailReportsUnavailable asserts
// the whole-fan-out-failure path still flips Available=false. Without
// this, a blanket Prometheus outage would incorrectly look like a
// meshed service reporting zero traffic.
func TestGoldenSignalsForService_AllQueriesFailReportsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	pc, err := monitoring.NewPrometheusClient(srv.URL)
	if err != nil {
		t.Fatalf("prom client: %v", err)
	}

	got, gerr := goldenSignalsForService(context.Background(), pc, MeshIstio, "shop", "cart")
	if gerr != nil {
		t.Fatalf("unexpected error: %v", gerr)
	}
	if got.Available {
		t.Error("Available = true, want false when every query fails")
	}
	if got.Reason != "metrics_unavailable" {
		t.Errorf("Reason = %q, want metrics_unavailable", got.Reason)
	}
}

// TestHandler_GoldenSignals_RenderFailureSurfacesAs400 covers the
// handler's mapping of a monitoring.QueryTemplate render error onto an
// HTTP 400 with a user-safe message. Internal render-key text ("render
// rps:" etc.) must not leak into the body.
func TestHandler_GoldenSignals_RenderFailureSurfacesAs400(t *testing.T) {
	// promClientOverride bypasses the nil-client early-exit, ensuring
	// the render path is the one that fails.
	srv := newFakePromServer(t, []promStub{{"istio_requests_total", 1}})
	defer srv.Close()
	pc, err := monitoring.NewPrometheusClient(srv.URL)
	if err != nil {
		t.Fatalf("prom client: %v", err)
	}

	h := &Handler{
		Discoverer:         seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true}}),
		AccessChecker:      resources.NewAlwaysAllowAccessChecker(),
		Logger:             slog.Default(),
		promClientOverride: pc,
	}
	// Namespace with embedded double-quote — the monitoring package's
	// k8s-name guard rejects this.
	w := doGoldenSignals(t, h, url.Values{"namespace": {`bad"ns`}, "service": {"cart"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "render") {
		t.Errorf("body leaks internal render-key prefix: %q", body)
	}
}

// --- helpers ---------------------------------------------------------------

// promStub pairs a query-fragment with the scalar value the fake
// Prometheus server should return when the PromQL query contains that
// fragment. Callers pass an ordered slice so match precedence is
// deterministic — a Go map would randomize iteration and silently
// cross-wire queries whose templates share a common substring (e.g.
// errorNum embeds istio_requests_total).
type promStub struct {
	frag  string
	value float64
}

// newFakePromServer returns an httptest.Server that impersonates a
// Prometheus v1 /api/v1/query endpoint. Stubs are checked in slice
// order; the first fragment contained in the query wins. Callers must
// list the most-specific fragments first. Queries that match no stub
// produce an empty vector (valid, zero).
func newFakePromServer(t *testing.T, stubs []promStub) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		query := r.FormValue("query")
		w.Header().Set("Content-Type", "application/json")
		for _, s := range stubs {
			if strings.Contains(query, s.frag) {
				_, _ = fmt.Fprintf(w,
					`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"%g"]}]}}`,
					s.value)
				return
			}
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
}

func doGoldenSignals(t *testing.T, h *Handler, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/mesh/golden-signals?"+q.Encode(), nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{KubernetesUsername: "u"}))
	w := httptest.NewRecorder()
	h.HandleGoldenSignals(w, req)
	return w
}

