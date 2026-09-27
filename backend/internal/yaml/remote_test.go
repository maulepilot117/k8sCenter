package yaml

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	clienttesting "k8s.io/client-go/testing"
)

// --- Fixtures -----------------------------------------------------------
//
// LOCAL is real: a *k8s.ClientFactory whose base clientset points at an
// httptest server speaking the legacy discovery protocol (the idiom
// internal/server's capability tests use), wrapped in a real
// *k8s.ClusterRouter. Every discovery request the local server receives is
// counted, and the local fake dynamic client records its own actions, so a
// test can prove the local cluster was never consulted by asserting on call
// counts rather than on an error string.
//
// REMOTE is injected through the clusterTargeter seam: a remote target
// cannot be pointed at a test server from outside package k8s (see the
// comment on clusterTargeter). Local cluster IDs are delegated to the real
// router, so a handler that reached for the local factory on a remote request
// would show up in the local counters.

const (
	remoteClusterID  = "remote-a"
	remoteGeneration = "2026-01-02T03:04:05Z"
)

// widgetGVR is a CRD that exists ONLY on the remote cluster.
var widgetGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

var configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

var testListKinds = map[schema.GroupVersionResource]string{
	widgetGVR:    "WidgetList",
	configMapGVR: "ConfigMapList",
}

type localCluster struct {
	discoveryHits *atomic.Int64
	dyn           *dynfake.FakeDynamicClient
	factory       *k8s.ClientFactory
	router        *k8s.ClusterRouter
}

// newLocalCluster serves core/v1 configmaps (and nothing else) from a
// counting discovery server.
func newLocalCluster(t *testing.T, objs ...runtime.Object) *localCluster {
	t.Helper()
	hits := &atomic.Int64{}

	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, metav1.APIVersions{Versions: []string{"v1"}})
	})
	mux.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, metav1.APIResourceList{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{{
				Name: "configmaps", SingularName: "configmap", Namespaced: true, Kind: "ConfigMap",
				Verbs: metav1.Verbs{"get", "list", "create", "update", "patch", "delete"},
			}},
		})
	})
	mux.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, metav1.APIGroupList{})
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}
	dyn := newDynamicClient(objs...)
	factory := k8s.NewTestClientFactoryWithDynamic(cs, dyn)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &localCluster{
		discoveryHits: hits,
		dyn:           dyn,
		factory:       factory,
		router:        k8s.NewClusterRouter(factory, nil, "", logger),
	}
}

// newDynamicClient returns a fake dynamic client that answers server-side
// apply patches the way the API server answers a dry-run: by echoing the
// applied object. The fake's object tracker cannot merge an apply patch into
// an Unstructured, and validate/diff are dry-run applies, so without this
// every document would fail inside the fake rather than in the code under
// test. Every action is still recorded for the call-count assertions.
func newDynamicClient(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), testListKinds, objs...)
	dyn.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		patch, ok := action.(clienttesting.PatchAction)
		if !ok || patch.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patch.GetPatch()); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
	return dyn
}

// assertUntouched fails when anything reached the local cluster.
func (l *localCluster) assertUntouched(t *testing.T) {
	t.Helper()
	if n := l.discoveryHits.Load(); n != 0 {
		t.Errorf("local discovery server received %d requests; want 0 — a remote request must never consult the local schema", n)
	}
	if n := len(l.dyn.Actions()); n != 0 {
		t.Errorf("local dynamic client recorded %d actions %v; want 0 — a remote request must never execute locally", n, l.dyn.Actions())
	}
}

// newRemoteSchema builds a remote TargetSchema whose discovery advertises
// only example.com/v1 widgets.
func newRemoteSchema(t *testing.T) *k8s.TargetSchema {
	t.Helper()
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{{
		GroupVersion: "example.com/v1",
		APIResources: []metav1.APIResource{{
			Name: "widgets", SingularName: "widget", Namespaced: true, Kind: "Widget",
			Verbs: metav1.Verbs{"get", "list", "create", "update", "patch", "delete"},
		}},
	}}}}
	groups, err := restmapper.GetAPIGroupResources(disc)
	if err != nil {
		t.Fatalf("GetAPIGroupResources: %v", err)
	}
	return &k8s.TargetSchema{
		ClusterID:  remoteClusterID,
		Generation: remoteGeneration,
		IsLocal:    false,
		Discovery:  disc,
		Mapper:     restmapper.NewDiscoveryRESTMapper(groups),
		Invalidate: func() {},
	}
}

// fakeTargeter delegates local IDs to the real router and answers the remote
// ID with the injected pair + schema (or err).
type fakeTargeter struct {
	local     *k8s.ClusterRouter
	remoteDyn *dynfake.FakeDynamicClient
	schema    *k8s.TargetSchema
	err       error
	calls     atomic.Int64
}

func (f *fakeTargeter) RouterFor(ctx context.Context, clusterID, username string, groups []string) (*k8s.ClientPair, error) {
	f.calls.Add(1)
	if k8s.IsLocalClusterID(clusterID) {
		return f.local.RouterFor(ctx, clusterID, username, groups)
	}
	if f.err != nil {
		return nil, f.err
	}
	return &k8s.ClientPair{ClusterID: clusterID, IsLocal: false, Dynamic: f.remoteDyn}, nil
}

func (f *fakeTargeter) TargetFor(ctx context.Context, clusterID, username string, groups []string) (*k8s.ClientPair, *k8s.TargetSchema, error) {
	f.calls.Add(1)
	if k8s.IsLocalClusterID(clusterID) {
		return f.local.TargetFor(ctx, clusterID, username, groups)
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	return &k8s.ClientPair{ClusterID: clusterID, IsLocal: false, Dynamic: f.remoteDyn}, f.schema, nil
}

type fixture struct {
	local     *localCluster
	remoteDyn *dynfake.FakeDynamicClient
	targeter  *fakeTargeter
	handler   *Handler
}

func newFixture(t *testing.T, localObjs, remoteObjs []runtime.Object) *fixture {
	t.Helper()
	local := newLocalCluster(t, localObjs...)
	remoteDyn := newDynamicClient(remoteObjs...)
	targeter := &fakeTargeter{local: local.router, remoteDyn: remoteDyn, schema: newRemoteSchema(t)}
	return &fixture{
		local:     local,
		remoteDyn: remoteDyn,
		targeter:  targeter,
		handler:   newTestHandler(local.factory, targeter),
	}
}

func newTestHandler(factory *k8s.ClientFactory, targeter clusterTargeter) *Handler {
	return &Handler{
		K8sClient:     factory,
		ClusterRouter: targeter,
		AuditLogger:   audit.NewSlogLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newRequest(method, target, clusterID, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	ctx := auth.ContextWithUser(r.Context(), &auth.User{
		Username:           "alice",
		KubernetesUsername: "alice",
		KubernetesGroups:   []string{"system:authenticated"},
	})
	r = r.WithContext(middleware.WithClusterID(ctx, clusterID))
	return r
}

func serve(h http.HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// exportRequest routes through chi so URL params resolve exactly as in
// production.
func exportRequest(h *Handler, clusterID, kind, ns, name string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Get("/yaml/export/{kind}/{namespace}/{name}", h.HandleExport)
	r := newRequest(http.MethodGet, "/yaml/export/"+kind+"/"+ns+"/"+name, clusterID, "")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decoding response %q: %v", w.Body.String(), err)
	}
	return env.Data
}

const widgetYAML = `apiVersion: example.com/v1
kind: Widget
metadata:
  name: gizmo
  namespace: team-a
spec:
  size: 3
`

const configMapYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: team-a
data:
  key: value
`

func widget(name, ns string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("example.com/v1")
	u.SetKind("Widget")
	u.SetName(name)
	u.SetNamespace(ns)
	_ = unstructured.SetNestedField(u.Object, int64(3), "spec", "size")
	return u
}

func configMap(name, ns string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("ConfigMap")
	u.SetName(name)
	u.SetNamespace(ns)
	return u
}

type validateBody struct {
	Documents []struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Valid  bool   `json:"valid"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"documents"`
	Valid            bool   `json:"valid"`
	TargetCluster    string `json:"targetCluster"`
	TargetGeneration string `json:"targetGeneration"`
}

type diffBody struct {
	Documents []struct {
		Kind     string `json:"kind"`
		IsNew    bool   `json:"isNew"`
		Proposed string `json:"proposed"`
		Error    string `json:"error"`
	} `json:"documents"`
	TargetCluster    string `json:"targetCluster"`
	TargetGeneration string `json:"targetGeneration"`
}

// --- Tests --------------------------------------------------------------

func TestHandleValidate_RemoteUsesTargetMapper(t *testing.T) {
	fx := newFixture(t, nil, nil)

	w := serve(fx.handler.HandleValidate, newRequest(http.MethodPost, "/yaml/validate", remoteClusterID, widgetYAML))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[validateBody](t, w)
	if !body.Valid || len(body.Documents) != 1 || !body.Documents[0].Valid {
		t.Fatalf("validate = %+v; want the remote-only Widget to validate against the remote schema", body)
	}
	fx.local.assertUntouched(t)
	if len(fx.remoteDyn.Actions()) == 0 {
		t.Error("remote dynamic client recorded no actions; the dry-run must execute on the remote target")
	}
}

func TestHandleDiff_RemoteUsesTargetMapper(t *testing.T) {
	fx := newFixture(t, nil, nil)

	w := serve(fx.handler.HandleDiff, newRequest(http.MethodPost, "/yaml/diff", remoteClusterID, widgetYAML))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[diffBody](t, w)
	if len(body.Documents) != 1 || body.Documents[0].Error != "" {
		t.Fatalf("diff = %+v; want the remote-only Widget to resolve against the remote schema", body)
	}
	if !body.Documents[0].IsNew || !strings.Contains(body.Documents[0].Proposed, "gizmo") {
		t.Errorf("diff document = %+v; want a new Widget whose proposed YAML names gizmo", body.Documents[0])
	}
	fx.local.assertUntouched(t)
	if len(fx.remoteDyn.Actions()) == 0 {
		t.Error("remote dynamic client recorded no actions; the diff must execute on the remote target")
	}
}

func TestHandleExport_RemoteUsesTargetDiscovery(t *testing.T) {
	fx := newFixture(t, nil, []runtime.Object{widget("gizmo", "team-a")})

	w := exportRequest(fx.handler, remoteClusterID, "widgets", "team-a", "gizmo")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	got := decodeData[string](t, w)
	if !strings.Contains(got, "kind: Widget") || !strings.Contains(got, "name: gizmo") {
		t.Errorf("export = %q; want the remote Widget gizmo", got)
	}
	fx.local.assertUntouched(t)
}

func TestHandleValidate_ResponseCarriesTargetPin(t *testing.T) {
	fx := newFixture(t, nil, nil)

	cases := []struct {
		name, clusterID, body, wantCluster, wantGeneration string
	}{
		{"remote", remoteClusterID, widgetYAML, remoteClusterID, remoteGeneration},
		{"local", "", configMapYAML, k8s.LocalClusterID, "local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for verb, h := range map[string]http.HandlerFunc{
				"validate": fx.handler.HandleValidate,
				"diff":     fx.handler.HandleDiff,
			} {
				w := serve(h, newRequest(http.MethodPost, "/yaml/"+verb, tc.clusterID, tc.body))
				if w.Code != http.StatusOK {
					t.Fatalf("%s status = %d; want 200, body=%s", verb, w.Code, w.Body.String())
				}
				pin := decodeData[validateBody](t, w)
				if pin.TargetCluster != tc.wantCluster || pin.TargetGeneration != tc.wantGeneration {
					t.Errorf("%s pin = (%q, %q); want (%q, %q)", verb,
						pin.TargetCluster, pin.TargetGeneration, tc.wantCluster, tc.wantGeneration)
				}
			}
		})
	}
}

const secretYAML = `apiVersion: v1
kind: Secret
metadata:
  name: creds
  namespace: team-a
stringData:
  password: hunter2
`

func TestHandleDiff_SecretRefusedOnRemote(t *testing.T) {
	fx := newFixture(t, nil, nil)

	w := serve(fx.handler.HandleDiff, newRequest(http.MethodPost, "/yaml/diff", remoteClusterID, secretYAML))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; want 422, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Secrets cannot be diffed") {
		t.Errorf("body = %s; want the existing Secret-diff refusal message", w.Body.String())
	}
	if n := fx.targeter.calls.Load(); n != 0 {
		t.Errorf("targeter called %d times; the Secret refusal must run before any routing", n)
	}
	if n := len(fx.remoteDyn.Actions()); n != 0 {
		t.Errorf("remote dynamic client recorded %d actions; want 0", n)
	}
	fx.local.assertUntouched(t)
}

func TestHandleExport_SecretRefusedOnRemote(t *testing.T) {
	fx := newFixture(t, nil, nil)

	for _, kind := range []string{"secrets", "Secret"} {
		w := exportRequest(fx.handler, remoteClusterID, kind, "team-a", "creds")
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s status = %d; want 422, body=%s", kind, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "Secrets cannot be exported") {
			t.Errorf("%s body = %s; want the existing Secret-export refusal message", kind, w.Body.String())
		}
	}
	if n := fx.targeter.calls.Load(); n != 0 {
		t.Errorf("targeter called %d times; the Secret refusal must run before any routing", n)
	}
	fx.local.assertUntouched(t)
}

// TestHandleValidate_RemoteDiscoveryFailureIsNotLocalFallback drives the REAL
// ClusterRouter with no cluster store, so remote resolution fails closed
// inside production code (requireClusterStore). The handler must surface a
// 5xx and must not reach for its own local factory instead.
func TestHandleValidate_RemoteDiscoveryFailureIsNotLocalFallback(t *testing.T) {
	cases := []struct {
		name     string
		targeter func(l *localCluster) clusterTargeter
	}{
		{"real router without cluster store", func(l *localCluster) clusterTargeter { return l.router }},
		{"remote schema resolution error", func(l *localCluster) clusterTargeter {
			return &fakeTargeter{local: l.router, err: errors.New("dial tcp 203.0.113.9:6443: i/o timeout")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := newLocalCluster(t, configMap("settings", "team-a"))
			h := newTestHandler(local.factory, tc.targeter(local))

			for verb, w := range map[string]*httptest.ResponseRecorder{
				"validate": serve(h.HandleValidate, newRequest(http.MethodPost, "/yaml/validate", remoteClusterID, configMapYAML)),
				"diff":     serve(h.HandleDiff, newRequest(http.MethodPost, "/yaml/diff", remoteClusterID, configMapYAML)),
				"export":   exportRequest(h, remoteClusterID, "configmaps", "team-a", "settings"),
			} {
				if w.Code < 500 {
					t.Errorf("%s status = %d; want 5xx when the remote target cannot be resolved, body=%s", verb, w.Code, w.Body.String())
				}
			}
			local.assertUntouched(t)
		})
	}
}

func TestHandleExport_HeaderClusterIsTheOnlyTarget(t *testing.T) {
	// "only-local" exists on the local cluster and nowhere else. Cluster A
	// advertises configmaps too, so resolution succeeds and the Get must
	// miss on A rather than find local's object.
	fx := newFixture(t, []runtime.Object{configMap("only-local", "team-a")}, nil)
	fx.targeter.schema = newRemoteSchemaWithConfigMaps(t)

	w := exportRequest(fx.handler, remoteClusterID, "configmaps", "team-a", "only-local")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 from cluster A, body=%s", w.Code, w.Body.String())
	}
	fx.local.assertUntouched(t)
}

func newRemoteSchemaWithConfigMaps(t *testing.T) *k8s.TargetSchema {
	t.Helper()
	ts := newRemoteSchema(t)
	disc := ts.Discovery.(*fakediscovery.FakeDiscovery)
	disc.Resources = append(disc.Resources, &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "configmaps", SingularName: "configmap", Namespaced: true, Kind: "ConfigMap"}},
	})
	return ts
}

// partialDiscovery returns client-go's partial-result-plus-error shape: the
// groups that loaded, alongside an *ErrGroupDiscoveryFailed for one that
// did not.
type partialDiscovery struct {
	discovery.DiscoveryInterface
	lists []*metav1.APIResourceList
}

func (p *partialDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, p.lists, &discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{
		{Group: "broken.example.com", Version: "v1"}: errors.New("the server is currently unable to handle the request"),
	}}
}

func TestResolveGVR_PartialDiscoveryError(t *testing.T) {
	disc := &partialDiscovery{lists: []*metav1.APIResourceList{{
		GroupVersion: "example.com/v1",
		APIResources: []metav1.APIResource{{Name: "widgets", Kind: "Widget", Namespaced: true}},
	}}}

	gvr, err := resolveGVR(disc, "Widgets")
	if err != nil {
		t.Fatalf("resolveGVR: %v; want the loaded group to resolve despite a different group failing", err)
	}
	if gvr != widgetGVR {
		t.Errorf("gvr = %v; want %v", gvr, widgetGVR)
	}

	if _, err := resolveGVR(disc, "gadgets"); err == nil {
		t.Error("resolveGVR(gadgets) = nil error; want not-found for a resource absent from the partial list")
	}

	if _, err := resolveGVR(&partialDiscovery{}, "widgets"); err == nil {
		t.Error("resolveGVR with no lists at all = nil error; want the discovery error surfaced")
	}
}

func TestHandleValidate_LocalPathUnchanged(t *testing.T) {
	fx := newFixture(t, nil, nil)

	w := serve(fx.handler.HandleValidate, newRequest(http.MethodPost, "/yaml/validate", "", configMapYAML))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}

	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	gotKeys := map[string]bool{}
	for k := range env.Data {
		gotKeys[k] = true
	}
	for _, k := range []string{"documents", "valid", "targetCluster", "targetGeneration"} {
		if !gotKeys[k] {
			t.Errorf("response is missing %q", k)
		}
		delete(gotKeys, k)
	}
	if len(gotKeys) != 0 {
		t.Errorf("response has unexpected keys %v; only the two pin fields may be added", gotKeys)
	}

	const wantDocuments = `[{"index":0,"kind":"ConfigMap","name":"settings","namespace":"team-a","valid":true}]`
	if got := string(env.Data["documents"]); got != wantDocuments {
		t.Errorf("documents = %s; want %s", got, wantDocuments)
	}
	if got := string(env.Data["valid"]); got != "true" {
		t.Errorf("valid = %s; want true", got)
	}
	if len(fx.remoteDyn.Actions()) != 0 {
		t.Error("remote dynamic client recorded actions on a local request")
	}
}
