package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// fakeProm is a promQuerier whose Query method returns a scripted result.
// Each entry in scripts is consumed in order; reaching the end is a test
// error. scripts can carry a vector value, an explicit error, or both
// (error wins).
type fakeProm struct {
	scripts []promScript
	calls   int
}

type promScript struct {
	value model.Value
	err   error
}

func (f *fakeProm) Query(_ context.Context, _ string, _ time.Time) (model.Value, v1.Warnings, error) {
	if f.calls >= len(f.scripts) {
		return nil, nil, errors.New("fakeProm: no script left")
	}
	s := f.scripts[f.calls]
	f.calls++
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.value, nil, nil
}

func vec(samples ...float64) model.Vector {
	out := make(model.Vector, 0, len(samples))
	for _, s := range samples {
		out = append(out, &model.Sample{Value: model.SampleValue(s)})
	}
	return out
}

// metricsHandler builds a Handler ready for metrics-endpoint testing. The
// returned dynamic-client fake is preloaded with the supplied stores AND
// objects (the latter populates the service-account-scoped cache so the
// dependent-ES resolution inside HandleGetStoreMetrics finds matches).
// Pass `prom` as nil to simulate Prometheus offline.
func metricsHandler(objects []runtime.Object, prom promQuerier, accessChecker *resources.AccessChecker) *Handler {
	dynFake := newEsoFakeDynClient(objects...)
	return &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: accessChecker,
		Logger:        slog.Default(),
		dynOverride:   dynFake, // service-account cache reads
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
		promQuerierOverride: prom,
	}
}

// makeESForStore builds an ExternalSecret that references the named
// (Cluster)SecretStore. Used by metrics tests to populate the dependent-ES
// set the handler aggregates over.
func makeESForStore(ns, name, uid, storeName, storeKind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "ExternalSecret",
			"metadata":   map[string]any{"name": name, "namespace": ns, "uid": uid},
			"spec": map[string]any{
				"refreshInterval": "1h",
				"secretStoreRef":  map[string]any{"name": storeName, "kind": storeKind},
				"target":          map[string]any{"name": name + "-secret"},
			},
			"status": map[string]any{
				"conditions": []any{map[string]any{"type": "Ready", "status": "True", "reason": "SecretSynced"}},
			},
		},
	}
}

func decodeMetrics(t *testing.T, w *httptest.ResponseRecorder) metricsResponse {
	t.Helper()
	var env struct {
		Data metricsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	return env.Data
}

// --- Happy path -----------------------------------------------------------

func TestHandleGetStoreMetrics_HappyPath_PaidTierStore(t *testing.T) {
	awsStore := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "SecretStore",
			"metadata":   map[string]any{"name": "aws", "namespace": "apps", "uid": "uid-aws"},
			"spec": map[string]any{
				"provider": map[string]any{
					"aws": map[string]any{"service": "SecretsManager", "region": "us-east-1"},
				},
			},
			"status": map[string]any{
				"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			},
		},
	}

	es := makeESForStore("apps", "db-creds", "uid-es", "aws", "SecretStore")
	prom := &fakeProm{scripts: []promScript{
		{value: vec(2.5)},       // rate query — 2.5 req/s
		{value: vec(1_500_000)}, // 24h count — 1.5M requests
	}}

	h := metricsHandler([]runtime.Object{awsStore, es}, prom, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "aws"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	got := decodeMetrics(t, w)
	if got.RatePerMin == nil || *got.RatePerMin < 149 || *got.RatePerMin > 151 {
		t.Errorf("ratePerMin = %v; want ~150 (2.5 req/s * 60)", got.RatePerMin)
	}
	if got.Last24h == nil || *got.Last24h != 1_500_000 {
		t.Errorf("last24h = %v; want 1500000", got.Last24h)
	}
	if got.Cost == nil {
		t.Fatal("cost = nil; want populated for AWS Secrets Manager")
	}
	if got.Cost.BillingProvider != "aws-secrets-manager" {
		t.Errorf("billingProvider = %q", got.Cost.BillingProvider)
	}
	if got.Cost.Estimated24h <= 0 {
		t.Errorf("estimated24h = %v; want positive", got.Cost.Estimated24h)
	}
	if got.Error != "" {
		t.Errorf("error = %q; want empty", got.Error)
	}
	if prom.calls != 2 {
		t.Errorf("prom called %d times; want 2", prom.calls)
	}
}

func TestHandleGetStoreMetrics_HappyPath_SelfHostedSuppressesCost(t *testing.T) {
	store := makeStore("apps", "vault", "uid-vault")
	es := makeESForStore("apps", "db-creds", "uid-es", "vault", "SecretStore")
	prom := &fakeProm{scripts: []promScript{
		{value: vec(0.5)},
		{value: vec(43200)},
	}}
	h := metricsHandler([]runtime.Object{store, es}, prom, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	got := decodeMetrics(t, w)
	if got.Cost != nil {
		t.Errorf("cost = %+v; want nil for self-hosted vault", got.Cost)
	}
	if got.RatePerMin == nil {
		t.Errorf("ratePerMin should still be populated for self-hosted")
	}
}

// --- Cluster scope --------------------------------------------------------

func TestHandleGetClusterStoreMetrics_HappyPath(t *testing.T) {
	cs := makeClusterStore("global-vault", "uid-cs")
	es1 := makeESForStore("apps", "shared-creds", "uid-es1", "global-vault", "ClusterSecretStore")
	es2 := makeESForStore("backend", "api-creds", "uid-es2", "global-vault", "ClusterSecretStore")
	prom := &fakeProm{scripts: []promScript{{value: vec(1.0)}, {value: vec(86400)}}}
	h := metricsHandler([]runtime.Object{cs, es1, es2}, prom, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"name": "global-vault"})
	h.HandleGetClusterStoreMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	got := decodeMetrics(t, w)
	if got.RatePerMin == nil || *got.RatePerMin < 59 || *got.RatePerMin > 61 {
		t.Errorf("ratePerMin = %v; want ~60", got.RatePerMin)
	}
}

// --- Degradation paths ----------------------------------------------------

func TestHandleGetStoreMetrics_PrometheusOffline_ReturnsErrorEnvelope(t *testing.T) {
	store := makeStore("apps", "vault", "uid-vault")
	es := makeESForStore("apps", "db-creds", "uid-es", "vault", "SecretStore")
	h := metricsHandler([]runtime.Object{store, es}, nil, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (degraded path is HTTP 200)", w.Code)
	}
	got := decodeMetrics(t, w)
	if got.Error != "rate metrics offline" {
		t.Errorf("error = %q; want 'rate metrics offline'", got.Error)
	}
	if got.RatePerMin != nil || got.Last24h != nil {
		t.Errorf("expected nil rate fields on degradation, got %+v", got)
	}
}

func TestHandleGetStoreMetrics_PrometheusQueryError_ReturnsErrorEnvelope(t *testing.T) {
	store := makeStore("apps", "vault", "uid-vault")
	es := makeESForStore("apps", "db-creds", "uid-es", "vault", "SecretStore")
	prom := &fakeProm{scripts: []promScript{{err: errors.New("connection refused")}}}
	h := metricsHandler([]runtime.Object{store, es}, prom, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	got := decodeMetrics(t, w)
	if got.Error != "rate metrics offline" {
		t.Errorf("error = %q; want degradation envelope", got.Error)
	}
}

func TestHandleGetStoreMetrics_NoSamplesYet_SuppressesCost(t *testing.T) {
	awsStore := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "SecretStore",
			"metadata":   map[string]any{"name": "aws", "namespace": "apps", "uid": "uid-aws"},
			"spec":       map[string]any{"provider": map[string]any{"aws": map[string]any{"service": "SecretsManager"}}},
			"status":     map[string]any{},
		},
	}
	es := makeESForStore("apps", "db-creds", "uid-es", "aws", "SecretStore")
	// Both queries return empty vectors — Prometheus reachable, no series.
	prom := &fakeProm{scripts: []promScript{{value: model.Vector{}}, {value: model.Vector{}}}}
	h := metricsHandler([]runtime.Object{awsStore, es}, prom, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "aws"})
	h.HandleGetStoreMetrics(w, r)

	got := decodeMetrics(t, w)
	if got.RatePerMin != nil || got.Last24h != nil {
		t.Errorf("rate fields should be nil when Prometheus has no series; got %+v", got)
	}
	if got.Cost != nil {
		t.Errorf("cost should be suppressed when last24h is nil; got %+v", got.Cost)
	}
	if got.Error != "" {
		t.Errorf("error should be empty (Prometheus reachable); got %q", got.Error)
	}
}

// --- Validation paths -----------------------------------------------------

func TestHandleGetStoreMetrics_RBACDenied(t *testing.T) {
	store := makeStore("apps", "vault", "uid-vault")
	denied := resources.NewAlwaysDenyAccessChecker()
	h := metricsHandler([]runtime.Object{store}, &fakeProm{}, denied)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", w.Code)
	}
}

func TestHandleGetStoreMetrics_InvalidName_Returns400(t *testing.T) {
	h := metricsHandler(nil, &fakeProm{}, resources.NewAlwaysAllowAccessChecker())

	for _, bad := range []string{"UPPERCASE", "with space", "semi;colon", "../escape"} {
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
		r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": bad})
		h.HandleGetStoreMetrics(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("name=%q: status = %d; want 400", bad, w.Code)
		}
	}
}

func TestHandleGetStoreMetrics_ESONotDetected_Returns503(t *testing.T) {
	h := metricsHandler(nil, &fakeProm{}, resources.NewAlwaysAllowAccessChecker())
	h.Discoverer = undetectedDiscoverer()

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503", w.Code)
	}
}

func TestHandleGetStoreMetrics_StoreNotFound_Returns404(t *testing.T) {
	h := metricsHandler(nil, &fakeProm{}, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "nonexistent"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", w.Code)
	}
}

// --- Dependent-aggregation -----------------------------------------------

func TestHandleGetStoreMetrics_NoDependents_ShortCircuits(t *testing.T) {
	// Store exists but no ExternalSecret references it. Handler should
	// return a zero/nil response WITHOUT calling Prometheus.
	store := makeStore("apps", "vault", "uid-vault")
	prom := &fakeProm{} // no scripts — any call would fail the test
	h := metricsHandler([]runtime.Object{store}, prom, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleGetStoreMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if prom.calls != 0 {
		t.Errorf("prom called %d times; want 0 — should short-circuit before query", prom.calls)
	}
	got := decodeMetrics(t, w)
	if got.RatePerMin != nil || got.Last24h != nil || got.Cost != nil {
		t.Errorf("expected nil rate/cost for store with no dependents; got %+v", got)
	}
}

func TestDependentMatchers_NamespacedScope_FiltersByNamespace(t *testing.T) {
	store := makeStore("apps", "vault", "uid-vault")
	matchingES := makeESForStore("apps", "in-ns", "uid-1", "vault", "SecretStore")
	wrongNS := makeESForStore("other", "wrong-ns", "uid-2", "vault", "SecretStore")
	wrongKind := makeESForStore("apps", "wrong-kind", "uid-3", "vault", "ClusterSecretStore")
	wrongName := makeESForStore("apps", "wrong-name", "uid-4", "different-store", "SecretStore")

	h := metricsHandler(
		[]runtime.Object{store, matchingES, wrongNS, wrongKind, wrongName},
		&fakeProm{}, resources.NewAlwaysAllowAccessChecker(),
	)

	deps, err := h.dependentMatchers(context.Background(), SecretStore{
		Name:      "vault",
		Namespace: "apps",
		Scope:     "Namespaced",
	})
	if err != nil {
		t.Fatalf("dependentMatchers: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("got %d deps; want 1 — only matchingES should pass filters: %+v", len(deps), deps)
	}
	if deps[0].Name != "in-ns" || deps[0].Namespace != "apps" {
		t.Errorf("dep = %+v", deps[0])
	}
}

func TestDependentMatchers_ClusterScope_AggregatesAllNamespaces(t *testing.T) {
	cs := makeClusterStore("global", "uid-cs")
	es1 := makeESForStore("apps", "es1", "uid-1", "global", "ClusterSecretStore")
	es2 := makeESForStore("backend", "es2", "uid-2", "global", "ClusterSecretStore")
	wrong := makeESForStore("apps", "es3", "uid-3", "global", "SecretStore") // wrong kind

	h := metricsHandler(
		[]runtime.Object{cs, es1, es2, wrong},
		&fakeProm{}, resources.NewAlwaysAllowAccessChecker(),
	)

	deps, err := h.dependentMatchers(context.Background(), SecretStore{
		Name:  "global",
		Scope: "Cluster",
	})
	if err != nil {
		t.Fatalf("dependentMatchers: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("got %d deps; want 2: %+v", len(deps), deps)
	}
}

func TestBuildDepRegexes(t *testing.T) {
	// Single dep, namespaced.
	ns, names := buildDepRegexes([]dependentMatcher{{Namespace: "apps", Name: "es1"}})
	if ns != "apps" || names != "es1" {
		t.Errorf("ns=%q name=%q", ns, names)
	}

	// Multiple deps, same namespace — namespace deduped.
	ns, names = buildDepRegexes([]dependentMatcher{
		{Namespace: "apps", Name: "es1"},
		{Namespace: "apps", Name: "es2"},
	})
	if ns != "apps" || names != "es1|es2" {
		t.Errorf("ns=%q name=%q", ns, names)
	}

	// Empty namespace (cluster scope) — match any.
	ns, _ = buildDepRegexes([]dependentMatcher{{Namespace: "", Name: "es1"}})
	if ns != ".+" {
		t.Errorf("expected cross-namespace regex; got %q", ns)
	}

	// Names with regex meta chars are escaped.
	_, names = buildDepRegexes([]dependentMatcher{{Namespace: "apps", Name: "my.es-1"}})
	if !strings.Contains(names, `my\.es-1`) {
		t.Errorf("expected escaped name; got %q", names)
	}
}

// --- Helpers --------------------------------------------------------------

func TestIsMetricLabelSafe(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"valid-name", true},
		{"valid.name", true},
		{"valid123", true},
		{"with space", false},
		{"UPPER", false},
		{"semi;colon", false},
		{"path/escape", false},
		{"quote\"inject", false},
		{"newline\n", false},
	}
	// 254 chars exceeds k8s name max
	long := make([]byte, 254)
	for i := range long {
		long[i] = 'a'
	}
	tests = append(tests, struct {
		in   string
		want bool
	}{string(long), false})

	for _, tc := range tests {
		if got := isMetricLabelSafe(tc.in); got != tc.want {
			t.Errorf("isMetricLabelSafe(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestScalarFromVector(t *testing.T) {
	if scalarFromVector(model.Vector{}) != nil {
		t.Error("empty vector should return nil")
	}
	if got := scalarFromVector(vec(42)); got == nil || *got != 42 {
		t.Errorf("single sample should return 42; got %v", got)
	}
	// Non-vector types (scalar, matrix) return nil — caller treats as
	// "no data" rather than misinterpreting the sample.
	if got := scalarFromVector(&model.Scalar{Value: 1}); got != nil {
		t.Errorf("scalar should return nil; got %v", got)
	}
}

// --- writeStoreFetchError mapping ----------------------------------------

func TestWriteStoreFetchError_MapsCanonicalCodes(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{apierrors.NewForbidden(schema.GroupResource{Group: "external-secrets.io", Resource: "secretstores"}, "vault", errors.New("nope")), http.StatusForbidden},
		{apierrors.NewNotFound(schema.GroupResource{Group: "external-secrets.io", Resource: "secretstores"}, "vault"), http.StatusNotFound},
		{errors.New("opaque k8s error"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		w := httptest.NewRecorder()
		writeStoreFetchError(w, slog.Default(), tc.err, "Namespaced", "apps", "vault")
		if w.Code != tc.want {
			t.Errorf("err=%v: status = %d; want %d", tc.err, w.Code, tc.want)
		}
	}
}
