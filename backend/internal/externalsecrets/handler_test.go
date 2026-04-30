package externalsecrets

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// --- Test fixtures ---------------------------------------------------------

func esoScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	for gvr, kind := range map[schema.GroupVersionResource]string{
		ExternalSecretGVR:        "ExternalSecret",
		ClusterExternalSecretGVR: "ClusterExternalSecret",
		SecretStoreGVR:           "SecretStore",
		ClusterSecretStoreGVR:    "ClusterSecretStore",
		PushSecretGVR:            "PushSecret",
	} {
		gv := schema.GroupVersion{Group: gvr.Group, Version: gvr.Version}
		scheme.AddKnownTypeWithName(gv.WithKind(kind), &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gv.WithKind(kind+"List"), &unstructured.UnstructuredList{})
	}
	return scheme
}

func newEsoFakeDynClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	gvrToListKind := map[schema.GroupVersionResource]string{
		ExternalSecretGVR:        "ExternalSecretList",
		ClusterExternalSecretGVR: "ClusterExternalSecretList",
		SecretStoreGVR:           "SecretStoreList",
		ClusterSecretStoreGVR:    "ClusterSecretStoreList",
		PushSecretGVR:            "PushSecretList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(esoScheme(), gvrToListKind, objects...)
}

// detectedDiscoverer returns a Discoverer pre-seeded with detected=true so
// list handlers don't short-circuit on the IsAvailable() empty-state path.
func detectedDiscoverer() *Discoverer {
	return &Discoverer{
		logger: slog.Default(),
		status: ESOStatus{
			Detected:    true,
			LastChecked: time.Now().UTC(),
		},
	}
}

// undetectedDiscoverer returns a Discoverer pre-seeded with detected=false so
// list handlers exercise the empty-state path.
func undetectedDiscoverer() *Discoverer {
	return &Discoverer{
		logger: slog.Default(),
		status: ESOStatus{
			Detected:    false,
			LastChecked: time.Now().UTC(),
		},
	}
}

// withUser injects an *auth.User into the request context using the same
// helper key the real auth middleware uses.
func withUser(r *http.Request, u *auth.User) *http.Request {
	if u == nil {
		return r
	}
	return r.WithContext(auth.ContextWithUser(r.Context(), u))
}

// makeES builds an unstructured ExternalSecret with the given identity and a
// minimal Ready=True status so it normalizes to StatusSynced.
func makeES(ns, name, uid string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "ExternalSecret",
			"metadata": map[string]any{
				"name": name, "namespace": ns, "uid": uid,
			},
			"spec": map[string]any{
				"refreshInterval": "1h",
				"secretStoreRef":  map[string]any{"name": "vault", "kind": "SecretStore"},
				"target":          map[string]any{"name": name + "-secret"},
			},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True", "reason": "SecretSynced"},
				},
			},
		},
	}
}

// makeStore builds a SecretStore with vault provider and Ready=True.
func makeStore(ns, name, uid string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "SecretStore",
			"metadata":   map[string]any{"name": name, "namespace": ns, "uid": uid},
			"spec":       map[string]any{"provider": map[string]any{"vault": map[string]any{}}},
			"status": map[string]any{
				"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			},
		},
	}
}

// makeClusterStore builds a ClusterSecretStore (cluster-scoped — no namespace).
func makeClusterStore(name, uid string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "ClusterSecretStore",
			"metadata":   map[string]any{"name": name, "uid": uid},
			"spec":       map[string]any{"provider": map[string]any{"vault": map[string]any{}}},
			"status":     map[string]any{},
		},
	}
}

func decodeArray[T any](t *testing.T, w *httptest.ResponseRecorder) []T {
	t.Helper()
	var env struct {
		Data []T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	return env.Data
}

func decodeStatus(t *testing.T, w *httptest.ResponseRecorder) ESOStatus {
	t.Helper()
	var env struct {
		Data ESOStatus `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	return env.Data
}

// --- Status endpoint -------------------------------------------------------

func TestHandleStatus_ESONotInstalled(t *testing.T) {
	h := &Handler{
		Discoverer:    undetectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	h.HandleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d; want 200", w.Code)
	}
	st := decodeStatus(t, w)
	if st.Detected {
		t.Errorf("Detected = true; want false")
	}
}

func TestHandleStatus_ESOInstalled(t *testing.T) {
	d := detectedDiscoverer()
	d.status.Namespace = "external-secrets"
	d.status.Version = "0.14.0"

	h := &Handler{
		Discoverer:    d,
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	h.HandleStatus(w, r)

	st := decodeStatus(t, w)
	if !st.Detected || st.Version != "0.14.0" {
		t.Errorf("status = %+v; want detected/0.14.0", st)
	}
}

// --- ESO not installed: list endpoints all return [] HTTP 200 --------------

func TestListEndpoints_ESONotInstalled(t *testing.T) {
	h := &Handler{
		Discoverer:    undetectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}

	user := &auth.User{KubernetesUsername: "u"}
	endpoints := []func(http.ResponseWriter, *http.Request){
		h.HandleListExternalSecrets,
		h.HandleListClusterExternalSecrets,
		h.HandleListStores,
		h.HandleListClusterStores,
		h.HandleListPushSecrets,
	}

	for i, ep := range endpoints {
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), user)
		ep(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("endpoint #%d: status = %d; want 200", i, w.Code)
		}
		// Decode as raw json so we don't have to know the type for each
		var env struct {
			Data []any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Errorf("endpoint #%d decode: %v", i, err)
		}
		if env.Data == nil {
			t.Errorf("endpoint #%d data is nil — frontend treats null as error", i)
		}
		if len(env.Data) != 0 {
			t.Errorf("endpoint #%d data len = %d; want 0", i, len(env.Data))
		}
	}
}

// --- Happy list with full RBAC -------------------------------------------

func TestHandleListExternalSecrets_AllVisible(t *testing.T) {
	es1 := makeES("apps", "es1", "uid-1")
	es2 := makeES("platform", "es2", "uid-2")
	store := makeStore("apps", "vault", "uid-store")

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   newEsoFakeDynClient(es1, es2, store),
	}

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil),
		&auth.User{KubernetesUsername: "u", KubernetesGroups: []string{"g"}})
	h.HandleListExternalSecrets(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	got := decodeArray[ExternalSecret](t, w)
	if len(got) != 2 {
		t.Errorf("got %d ExternalSecrets; want 2", len(got))
	}
}

// --- Namespace query filter ----------------------------------------------

func TestHandleListExternalSecrets_NamespaceFilter(t *testing.T) {
	es1 := makeES("apps", "es1", "uid-1")
	es2 := makeES("platform", "es2", "uid-2")

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   newEsoFakeDynClient(es1, es2),
	}

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/?namespace=apps", nil),
		&auth.User{KubernetesUsername: "u"})
	h.HandleListExternalSecrets(w, r)

	got := decodeArray[ExternalSecret](t, w)
	if len(got) != 1 || got[0].Namespace != "apps" {
		t.Errorf("filter result = %+v; want only apps-ns", got)
	}
}

// --- Permissive-read on cluster-scoped resources -------------------------

func TestHandleListClusterStores_PermissiveReadGrant(t *testing.T) {
	cs := makeClusterStore("global-vault", "uid-cs")

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   newEsoFakeDynClient(cs),
	}

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil),
		&auth.User{KubernetesUsername: "u", KubernetesGroups: []string{"g"}})
	h.HandleListClusterStores(w, r)

	got := decodeArray[SecretStore](t, w)
	if len(got) != 1 {
		t.Errorf("got %d ClusterSecretStores; want 1", len(got))
	}
}

func TestHandleListClusterStores_PermissiveReadDenied(t *testing.T) {
	cs := makeClusterStore("global-vault", "uid-cs")

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysDenyAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   newEsoFakeDynClient(cs),
	}

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil),
		&auth.User{KubernetesUsername: "u"})
	h.HandleListClusterStores(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (silent empty, not 403)", w.Code)
	}
	got := decodeArray[SecretStore](t, w)
	if len(got) != 0 {
		t.Errorf("got %d ClusterSecretStores; want 0 (denied user sees empty list)", len(got))
	}
}

// --- Namespaced-resource RBAC: predicate filter --------------------------

func TestHandleListExternalSecrets_NamespacedRBACFilter(t *testing.T) {
	es1 := makeES("apps", "es1", "uid-1")
	es2 := makeES("platform", "es2", "uid-2")

	// Predicate: only "apps" namespace allowed.
	checker := resources.NewPredicateAccessChecker(func(verb, group, resource, namespace string) bool {
		return namespace == "apps"
	})

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: checker,
		Logger:        slog.Default(),
		dynOverride:   newEsoFakeDynClient(es1, es2),
	}

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil),
		&auth.User{KubernetesUsername: "u", KubernetesGroups: []string{"g"}})
	h.HandleListExternalSecrets(w, r)

	got := decodeArray[ExternalSecret](t, w)
	if len(got) != 1 || got[0].Namespace != "apps" {
		t.Errorf("filter result = %+v; want only apps-ns ES", got)
	}
}

// --- Cache hit / TTL semantics -------------------------------------------

// countingDynClient wraps a fake dynamic client to count list invocations
// per resource. Used to verify singleflight collapsing.
type countingDynClient struct {
	dynamic.Interface
	listCalls atomic.Int64
}

func (c *countingDynClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &countingNRI{NamespaceableResourceInterface: c.Interface.Resource(gvr), parent: c}
}

type countingNRI struct {
	dynamic.NamespaceableResourceInterface
	parent *countingDynClient
}

func (c *countingNRI) Namespace(ns string) dynamic.ResourceInterface {
	return &countingRI{ResourceInterface: c.NamespaceableResourceInterface.Namespace(ns), parent: c.parent}
}

func (c *countingNRI) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	c.parent.listCalls.Add(1)
	return c.NamespaceableResourceInterface.List(ctx, opts)
}

type countingRI struct {
	dynamic.ResourceInterface
	parent *countingDynClient
}

func (c *countingRI) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	c.parent.listCalls.Add(1)
	return c.ResourceInterface.List(ctx, opts)
}

func TestCacheHit_SingleflightCollapse(t *testing.T) {
	es := makeES("apps", "es1", "uid-1")
	fakeClient := newEsoFakeDynClient(es)
	counter := &countingDynClient{Interface: fakeClient}

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   counter,
	}

	// Fire 10 concurrent list calls — singleflight should collapse to one fetchAll
	// (which itself dispatches 5 list calls, one per CRD).
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			w := httptest.NewRecorder()
			r := withUser(httptest.NewRequest(http.MethodGet, "/", nil),
				&auth.User{KubernetesUsername: "u"})
			h.HandleListExternalSecrets(w, r)
		})
	}
	wg.Wait()

	// 5 list calls per fetchAll. Singleflight collapses N concurrent
	// HandleList invocations into 1 fetchAll. Allow up to 2 fetchAll cycles
	// (10 list calls) to tolerate scheduler races where the first request
	// finishes before another arrives — the requirement is "not 50."
	calls := counter.listCalls.Load()
	if calls > 10 {
		t.Errorf("dynClient.List invoked %d times across 10 concurrent requests; expected <=10 (5 per fetchAll, 1-2 fetchAlls)", calls)
	}
}

func TestCacheTTL_ReFetchAfterExpiry(t *testing.T) {
	es := makeES("apps", "es1", "uid-1")
	fakeClient := newEsoFakeDynClient(es)
	counter := &countingDynClient{Interface: fakeClient}

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   counter,
	}

	// First fetch
	if _, err := h.getCached(context.Background()); err != nil {
		t.Fatalf("first getCached: %v", err)
	}
	first := counter.listCalls.Load()

	// Force expiry by mutating fetchedAt
	h.cacheMu.Lock()
	h.cache.fetchedAt = time.Now().Add(-2 * cacheTTL)
	h.cacheMu.Unlock()

	if _, err := h.getCached(context.Background()); err != nil {
		t.Fatalf("second getCached: %v", err)
	}
	second := counter.listCalls.Load()

	if second <= first {
		t.Errorf("expected re-fetch after TTL expiry: first=%d second=%d", first, second)
	}
}

func TestInvalidateCache(t *testing.T) {
	es := makeES("apps", "es1", "uid-1")
	fakeClient := newEsoFakeDynClient(es)
	counter := &countingDynClient{Interface: fakeClient}

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   counter,
	}

	if _, err := h.getCached(context.Background()); err != nil {
		t.Fatalf("first getCached: %v", err)
	}
	first := counter.listCalls.Load()

	h.InvalidateCache()
	if h.cache != nil {
		t.Errorf("InvalidateCache didn't clear cache")
	}

	if _, err := h.getCached(context.Background()); err != nil {
		t.Fatalf("second getCached: %v", err)
	}
	second := counter.listCalls.Load()

	if second <= first {
		t.Errorf("expected re-fetch after InvalidateCache: first=%d second=%d", first, second)
	}
}

// --- Empty fake-dynamic-client scenario: 5 CRDs all return empty lists ---

// --- Drift status helper -------------------------------------------------

func TestComputeDriftStatus(t *testing.T) {
	cases := []struct {
		name     string
		syncedRV string
		liveRV   string
		want     DriftStatus
	}{
		{"empty-synced-RV-unknown", "", "12345", DriftUnknown},
		{"matching-RVs-in-sync", "12345", "12345", DriftInSync},
		{"differing-RVs-drifted", "12345", "12346", DriftDrifted},
		{"empty-live-RV-drifted", "12345", "", DriftDrifted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeDriftStatus(tc.syncedRV, tc.liveRV)
			if got != tc.want {
				t.Errorf("computeDriftStatus(%q, %q) = %q; want %q",
					tc.syncedRV, tc.liveRV, got, tc.want)
			}
		})
	}
}

// --- Detail endpoint with drift resolution -------------------------------

// urlWithChiParams attaches chi route params to a request context. The
// Get<...> handlers read namespace/name via chi.URLParam; in production a
// route definition supplies those, in tests we must inject them directly.
func urlWithChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// detailHandler builds a Handler with all overrides wired for detail tests.
// The fakeKubeSecret param, when non-nil, is preloaded into the typed-client
// fake so the drift check can find it.
func detailHandler(esObjs []runtime.Object, fakeKubeSecret *corev1.Secret, accessChecker *resources.AccessChecker) *Handler {
	dynFake := newEsoFakeDynClient(esObjs...)
	var typedFake kubernetes.Interface = kubefake.NewClientset()
	if fakeKubeSecret != nil {
		typedFake = kubefake.NewClientset(fakeKubeSecret)
	}
	return &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: accessChecker,
		Logger:        slog.Default(),
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
		clientForUserOverride: func(string, []string) (kubernetes.Interface, error) {
			return typedFake, nil
		},
	}
}

func TestHandleGetExternalSecret_DriftInSync(t *testing.T) {
	ns, name := "apps", "db-creds"
	targetSecret := name + "-secret" // matches makeES convention
	es := makeES(ns, name, "uid-1")
	// Set syncedResourceVersion on the ES status
	status, _ := es.Object["status"].(map[string]any)
	status["syncedResourceVersion"] = "100"

	syncedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: targetSecret, Namespace: ns, ResourceVersion: "100"},
	}

	h := detailHandler([]runtime.Object{es}, syncedSecret, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleGetExternalSecret(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data ExternalSecret `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.DriftStatus != DriftInSync {
		t.Errorf("DriftStatus = %q; want %q", env.Data.DriftStatus, DriftInSync)
	}
	if env.Data.Status != StatusSynced {
		t.Errorf("Status = %q; want %q (drift in-sync should not overlay)", env.Data.Status, StatusSynced)
	}
}

func TestHandleGetExternalSecret_DriftDetected(t *testing.T) {
	ns, name := "apps", "db-creds"
	targetSecret := name + "-secret"
	es := makeES(ns, name, "uid-1")
	status, _ := es.Object["status"].(map[string]any)
	status["syncedResourceVersion"] = "100"

	// Live Secret has a different RV — drift detected
	syncedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: targetSecret, Namespace: ns, ResourceVersion: "101"},
	}

	h := detailHandler([]runtime.Object{es}, syncedSecret, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleGetExternalSecret(w, r)

	var env struct {
		Data ExternalSecret `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data.DriftStatus != DriftDrifted {
		t.Errorf("DriftStatus = %q; want %q", env.Data.DriftStatus, DriftDrifted)
	}
	if env.Data.Status != StatusDrifted {
		t.Errorf("Status = %q; want %q (drift overlay on Synced)", env.Data.Status, StatusDrifted)
	}
}

func TestHandleGetExternalSecret_DriftUnknownNoSyncedRV(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	// status.syncedResourceVersion is intentionally absent — provider
	// doesn't populate it, so drift can't be determined.

	h := detailHandler([]runtime.Object{es}, nil, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleGetExternalSecret(w, r)

	var env struct {
		Data ExternalSecret `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data.DriftStatus != DriftUnknown {
		t.Errorf("DriftStatus = %q; want %q", env.Data.DriftStatus, DriftUnknown)
	}
	if env.Data.Status != StatusSynced {
		t.Errorf("Status = %q; want %q (no overlay when drift unknown)", env.Data.Status, StatusSynced)
	}
}

func TestHandleGetExternalSecret_SyncedSecretDeleted(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	status, _ := es.Object["status"].(map[string]any)
	status["syncedResourceVersion"] = "100"

	// No synced Secret in the typed-client — Get returns NotFound.
	h := detailHandler([]runtime.Object{es}, nil, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleGetExternalSecret(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (ES exists even though Secret is missing)", w.Code)
	}
	var env struct {
		Data ExternalSecret `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data.DriftStatus != DriftUnknown {
		t.Errorf("DriftStatus = %q; want %q", env.Data.DriftStatus, DriftUnknown)
	}
}

func TestHandleGetExternalSecret_RBACDenied(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")

	h := detailHandler([]runtime.Object{es}, nil, resources.NewAlwaysDenyAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleGetExternalSecret(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403 (denied user)", w.Code)
	}
}

func TestHandleGetExternalSecret_NotFound(t *testing.T) {
	h := detailHandler(nil, nil, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "missing"})
	h.HandleGetExternalSecret(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", w.Code)
	}
}

func TestHandleGetStore_HappyPath(t *testing.T) {
	ns, name := "apps", "vault"
	store := makeStore(ns, name, "uid-store")

	h := detailHandler([]runtime.Object{store}, nil, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleGetStore(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data SecretStore `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data.Provider != "vault" || env.Data.Scope != "Namespaced" {
		t.Errorf("store = %+v", env.Data)
	}
}

func TestHandleGetClusterStore_HappyPath(t *testing.T) {
	cs := makeClusterStore("global-vault", "uid-cs")

	h := detailHandler([]runtime.Object{cs}, nil, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"name": "global-vault"})
	h.HandleGetClusterStore(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data SecretStore `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data.Scope != "Cluster" || env.Data.Namespace != "" {
		t.Errorf("clusterstore = %+v", env.Data)
	}
}

func TestFetchAll_EmptyClusterReturnsEmptyLists(t *testing.T) {
	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynOverride:   newEsoFakeDynClient(),
	}

	data, err := h.getCached(context.Background())
	if err != nil {
		t.Fatalf("getCached: %v", err)
	}
	if data.externalSecrets == nil ||
		data.clusterExternalSecrets == nil ||
		data.stores == nil ||
		data.clusterStores == nil ||
		data.pushSecrets == nil {
		t.Errorf("nil slice in cache: %+v", data)
	}
	for name, n := range map[string]int{
		"externalSecrets":        len(data.externalSecrets),
		"clusterExternalSecrets": len(data.clusterExternalSecrets),
		"stores":                 len(data.stores),
		"clusterStores":          len(data.clusterStores),
		"pushSecrets":            len(data.pushSecrets),
	} {
		if n != 0 {
			t.Errorf("%s len = %d; want 0", name, n)
		}
	}
}
