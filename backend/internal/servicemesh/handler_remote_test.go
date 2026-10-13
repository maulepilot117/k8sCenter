package servicemesh

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const remoteCluster = "remote-1"

// allMeshCRDs are every GVR the package lists, with their Kind.
var allMeshCRDs = map[schema.GroupVersionResource]string{
	IstioVirtualServiceGVR:          "VirtualService",
	IstioDestinationRuleGVR:         "DestinationRule",
	IstioGatewayGVR:                 "Gateway",
	IstioPeerAuthenticationGVR:      "PeerAuthentication",
	IstioAuthorizationPolicyGVR:     "AuthorizationPolicy",
	LinkerdServiceProfileGVR:        "ServiceProfile",
	LinkerdServerGVR:                "Server",
	LinkerdHTTPRouteGVR:             "HTTPRoute",
	LinkerdAuthorizationPolicyGVR:   "AuthorizationPolicy",
	LinkerdMeshTLSAuthenticationGVR: "MeshTLSAuthentication",
}

// fakeCluster is one fake cluster: its discovery, dynamic and typed clients.
type fakeCluster struct {
	disc  *fakediscovery.FakeDiscovery
	dyn   *dynfake.FakeDynamicClient
	typed *kfake.Clientset
	// discOverride, when set, is the discovery handed out instead of disc.
	discOverride discovery.DiscoveryInterface
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. A target
// error makes every resolution fail.
type fakeClients struct {
	clusters  map[string]*fakeCluster
	targetErr error
	resolved  atomic.Int32
}

func (f *fakeClients) cluster(id string) (*fakeCluster, error) {
	f.resolved.Add(1)
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.typed, nil
}

func (f *fakeClients) DynamicClientForCluster(_ context.Context, id, _ string, _ []string) (dynamic.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.dyn, nil
}

func (f *fakeClients) TargetSchemaFor(_ context.Context, id, _ string, _ []string) (*k8s.TargetSchema, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	var disc discovery.DiscoveryInterface = c.disc
	if c.discOverride != nil {
		disc = c.discOverride
	}
	return &k8s.TargetSchema{ClusterID: id, Discovery: disc, Invalidate: func() {}}, nil
}

// meshLists is the discovery of a cluster serving the named meshes at the
// versions this package lists.
func meshLists(meshes ...MeshType) []*metav1.APIResourceList {
	byGV := map[string]*metav1.APIResourceList{}
	var out []*metav1.APIResourceList
	for gvr, kind := range allMeshCRDs {
		isIstio := strings.HasSuffix(gvr.Group, "istio.io")
		want := false
		for _, m := range meshes {
			if (m == MeshIstio) == isIstio {
				want = true
			}
		}
		if !want {
			continue
		}
		gv := gvr.GroupVersion().String()
		l, ok := byGV[gv]
		if !ok {
			l = &metav1.APIResourceList{GroupVersion: gv}
			byGV[gv] = l
			out = append(out, l)
		}
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: gvr.Resource, Kind: kind, Namespaced: true})
	}
	return out
}

func newFakeCluster(t *testing.T, lists []*metav1.APIResourceList, objs []runtime.Object, pods ...runtime.Object) *fakeCluster {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{}
	for gvr, kind := range allMeshCRDs {
		scheme.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind), &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind+"List"), &unstructured.UnstructuredList{})
		listKinds[gvr] = kind + "List"
	}
	return &fakeCluster{
		disc:  &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: lists}},
		dyn:   dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...),
		typed: kfake.NewClientset(pods...),
	}
}

// remoteFixture is a Handler whose remote cluster serves remote. The local
// cluster is seeded with its own objects behind every local seam (the
// service-account dynamic client, the typed override and the ClusterClients
// entry), and the local Discoverer reports Linkerd only, so any local read on
// the remote path shows up either in a recorded action or in the answer.
type remoteFixture struct {
	h        *Handler
	clients  *fakeClients
	localDyn *dynfake.FakeDynamicClient
	localCS  *kfake.Clientset
	promHits *atomic.Int32
}

func newRemoteFixture(t *testing.T, remote *fakeCluster) *remoteFixture {
	t.Helper()
	localObjs := []runtime.Object{virtualService("default", "local-vs", []string{"local.example"}, nil)}
	local := newFakeCluster(t, meshLists(MeshIstio), localObjs)
	clients := &fakeClients{clusters: map[string]*fakeCluster{remoteCluster: remote, k8s.LocalClusterID: local}}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	}))
	t.Cleanup(srv.Close)
	pc, err := monitoring.NewPrometheusClientWithTransport(srv.URL, http.DefaultTransport)
	if err != nil {
		t.Fatalf("prom client: %v", err)
	}

	localDyn := newIstioFakeDynClient(localObjs...)
	localCS := kfake.NewClientset(injectedPod("default", "local-6d4b7-abc", map[string]string{"app": "local"}))
	h := &Handler{
		Discoverer:         seededDiscoverer(MeshStatus{Detected: MeshLinkerd, Linkerd: &MeshInfo{Installed: true}}),
		AccessChecker:      resources.NewAlwaysAllowAccessChecker(),
		Clients:            clients,
		Presence:           k8s.NewPresence(clients),
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		dynOverride:        localDyn,
		clientsetOverride:  localCS,
		promClientOverride: pc,
	}
	return &remoteFixture{h: h, clients: clients, localDyn: localDyn, localCS: localCS, promHits: &hits}
}

// localActions counts every call that reached a local cluster seam.
func (f *remoteFixture) localActions() int {
	l := f.clients.clusters[k8s.LocalClusterID]
	return len(f.localDyn.Actions()) + len(f.localCS.Actions()) + len(l.dyn.Actions()) + len(l.typed.Actions())
}

func (f *remoteFixture) assertNoLocalOrProm(t *testing.T) {
	t.Helper()
	if n := f.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
	if n := f.promHits.Load(); n != 0 {
		t.Errorf("Prometheus received %d queries, want 0", n)
	}
}

func callOn(t *testing.T, clusterID string, h http.HandlerFunc, target string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}})
	ctx = middleware.WithClusterID(ctx, clusterID)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

func call(t *testing.T, h http.HandlerFunc, target string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return callOn(t, remoteCluster, h, target, params)
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return env.Data
}

// unreachableErr is a transport failure whose text names the remote address.
var unreachableErr = &url.Error{Op: "Get", URL: "https://10.20.30.40:6443/apis",
	Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}

func assertNoLeak(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	for _, s := range []string{"10.20.30.40", "connection refused"} {
		if strings.Contains(rr.Body.String(), s) {
			t.Errorf("response leaks %q: %s", s, rr.Body.String())
		}
	}
}

type routingBody struct {
	Status   MeshStatus           `json:"status"`
	Routes   []TrafficRoute       `json:"routes"`
	Errors   map[string]string    `json:"errors"`
	Coverage []k8s.SourceCoverage `json:"coverage"`
}

func TestRemote_ListRoutesReturnsRemoteObjects(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio, MeshLinkerd), []runtime.Object{
		virtualService("shop", "remote-vs", []string{"cart.shop"}, nil),
		serviceProfile("books", "remote-sp", nil),
	})
	f := newRemoteFixture(t, remote)

	rr := call(t, f.h.HandleListRoutes, "/mesh/routing", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decode[routingBody](t, rr)
	names := map[string]bool{}
	for _, r := range body.Routes {
		names[r.Name] = true
	}
	if len(body.Routes) != 2 || !names["remote-vs"] || !names["remote-sp"] {
		t.Errorf("routes = %+v, want remote-vs and remote-sp only", body.Routes)
	}
	if body.Status.Detected != MeshBoth || body.Status.Reason != "" {
		t.Errorf("status = %+v, want the remote's detected=both with no reason", body.Status)
	}
	if len(body.Errors) != 0 || len(body.Coverage) != 0 {
		t.Errorf("errors = %v, coverage = %v, want none", body.Errors, body.Coverage)
	}
	f.assertNoLocalOrProm(t)
}

func TestRemote_ListPoliciesReturnsRemoteObjects(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), []runtime.Object{
		newPeerAuth("shop", "remote-pa", IstioMTLSStrict, nil),
	})
	f := newRemoteFixture(t, remote)

	rr := call(t, f.h.HandleListPolicies, "/mesh/policies", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decode[policiesResponse](t, rr)
	if len(body.Policies) != 1 || body.Policies[0].Name != "remote-pa" {
		t.Errorf("policies = %+v, want only remote-pa", body.Policies)
	}
	f.assertNoLocalOrProm(t)
}

// A remote that serves no mesh answers with the family's negative value and
// discovery_missing, even though the local cluster has a mesh.
func TestRemote_NoMeshOnRemoteIsNotInstalled(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, nil, nil))

	rr := call(t, f.h.HandleListRoutes, "/mesh/routing", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decode[routingBody](t, rr)
	if body.Status.Detected != MeshNone || body.Status.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("status = %+v, want detected none with reason discovery_missing", body.Status)
	}
	if body.Routes == nil || len(body.Routes) != 0 {
		t.Errorf("routes = %v, want an empty list", body.Routes)
	}
	f.assertNoLocalOrProm(t)
}

func TestRemote_StatusReadsTheRemote(t *testing.T) {
	tests := []struct {
		name       string
		lists      []*metav1.APIResourceList
		targetErr  error
		wantMesh   MeshType
		wantReason string
	}{
		{"istio on remote only", meshLists(MeshIstio), nil, MeshIstio, ""},
		{"nothing on remote", nil, nil, MeshNone, string(k8s.ReasonDiscoveryMissing)},
		{"unreachable", nil, unreachableErr, MeshNone, string(k8s.ReasonUnreachable)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRemoteFixture(t, newFakeCluster(t, tt.lists, nil))
			f.clients.targetErr = tt.targetErr

			rr := call(t, f.h.HandleStatus, "/mesh/status", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			got := decode[MeshStatusResponse](t, rr).Status
			if got.Detected != tt.wantMesh || got.Reason != tt.wantReason {
				t.Errorf("status = %+v, want detected %q reason %q", got, tt.wantMesh, tt.wantReason)
			}
			if tt.wantMesh == MeshIstio && (got.Istio == nil || !got.Istio.Installed || got.Linkerd != nil) {
				t.Errorf("status = %+v, want istio installed and no linkerd", got)
			}
			assertNoLeak(t, rr)
			f.assertNoLocalOrProm(t)
		})
	}
}

// The local status keeps its shape: no reason, the local discoverer's answer.
func TestLocal_StatusUnchanged(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, nil, nil))

	rr := callOn(t, k8s.LocalClusterID, f.h.HandleStatus, "/mesh/status", nil)
	got := decode[MeshStatusResponse](t, rr).Status
	if got.Detected != MeshLinkerd || got.Reason != "" {
		t.Errorf("local status = %+v, want the local discoverer's linkerd with no reason", got)
	}
	if n := f.clients.resolved.Load(); n != 0 {
		t.Errorf("local status resolved %d cluster clients, want 0", n)
	}
}

// Istio lists, but the remote refuses Linkerd Servers: the list is a partial
// 200 naming the failed source, with no cluster error text.
func TestRemote_FailedSourceIsDisclosed(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio, MeshLinkerd), []runtime.Object{
		virtualService("shop", "remote-vs", []string{"cart.shop"}, nil),
	})
	remote.dyn.PrependReactor("list", "servers", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(LinkerdServerGVR.GroupResource(), "", errors.New("RBAC: user admin at 10.20.30.40 cannot list"))
	})
	f := newRemoteFixture(t, remote)

	rr := call(t, f.h.HandleListRoutes, "/mesh/routing", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decode[routingBody](t, rr)
	if len(body.Routes) != 1 || body.Routes[0].Name != "remote-vs" {
		t.Errorf("routes = %+v, want remote-vs", body.Routes)
	}
	want := []k8s.SourceCoverage{{Source: "linkerd/Server", Status: k8s.CoverageStatusForbidden, ReasonCode: string(k8s.ReasonForbidden)}}
	if len(body.Coverage) != 1 || body.Coverage[0] != want[0] {
		t.Errorf("coverage = %+v, want %+v", body.Coverage, want)
	}
	if _, ok := body.Errors["linkerd/Server"]; !ok || len(body.Errors) != 1 {
		t.Errorf("errors = %v, want one entry for linkerd/Server", body.Errors)
	}
	assertNoLeak(t, rr)
	if strings.Contains(rr.Body.String(), "RBAC: user admin") {
		t.Errorf("response echoes the cluster's error text: %s", rr.Body.String())
	}
	f.assertNoLocalOrProm(t)
}

// Every list failing is a failed view, answered with the fixed remote error.
func TestRemote_AllListsFailingIs502(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), nil)
	remote.dyn.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachableErr
	})
	f := newRemoteFixture(t, remote)

	rr := call(t, f.h.HandleListRoutes, "/mesh/routing", nil)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rr.Code, rr.Body.String())
	}
	assertNoLeak(t, rr)
	f.assertNoLocalOrProm(t)
}

func TestRemote_UnreachableListIs502(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))
	f.clients.targetErr = unreachableErr

	for name, h := range map[string]http.HandlerFunc{
		"routes":   f.h.HandleListRoutes,
		"policies": f.h.HandleListPolicies,
		"mtls":     f.h.HandleMTLSPosture,
	} {
		rr := call(t, h, "/mesh/"+name, nil)
		if rr.Code != http.StatusBadGateway {
			t.Errorf("%s: status %d, want 502: %s", name, rr.Code, rr.Body.String())
		}
		assertNoLeak(t, rr)
	}
	f.assertNoLocalOrProm(t)
}

// A route type the remote stopped serving after discovery was cached counts
// as empty rather than failed.
func TestRemote_RemovedCRDIsEmptyNotFailed(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), []runtime.Object{
		virtualService("shop", "remote-vs", []string{"cart.shop"}, nil),
	})
	remote.dyn.PrependReactor("list", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(IstioGatewayGVR.GroupResource(), "")
	})
	f := newRemoteFixture(t, remote)

	rr := call(t, f.h.HandleListRoutes, "/mesh/routing", nil)
	body := decode[routingBody](t, rr)
	if rr.Code != http.StatusOK || len(body.Routes) != 1 || len(body.Coverage) != 0 || len(body.Errors) != 0 {
		t.Errorf("status %d, body %+v: want remote-vs with no failure", rr.Code, body)
	}
}

func TestRemote_GetRouteReadsTheRemote(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), []runtime.Object{
		virtualService("default", "local-vs", []string{"remote.example"}, nil),
	})
	f := newRemoteFixture(t, remote)

	rr := call(t, f.h.HandleGetRoute, "/mesh/routing/x", map[string]string{"id": "istio:default:vs:local-vs"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[TrafficRoute](t, rr)
	if len(got.Hosts) != 1 || got.Hosts[0] != "remote.example" {
		t.Errorf("hosts = %v, want the remote object's remote.example", got.Hosts)
	}
	f.assertNoLocalOrProm(t)
}

func TestRemote_GetRouteFailures(t *testing.T) {
	t.Run("unreachable", func(t *testing.T) {
		f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))
		f.clients.targetErr = unreachableErr
		rr := call(t, f.h.HandleGetRoute, "/mesh/routing/x", map[string]string{"id": "istio:default:vs:local-vs"})
		if rr.Code != http.StatusBadGateway {
			t.Errorf("status %d, want 502: %s", rr.Code, rr.Body.String())
		}
		assertNoLeak(t, rr)
		f.assertNoLocalOrProm(t)
	})
	t.Run("not found on the remote", func(t *testing.T) {
		f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))
		rr := call(t, f.h.HandleGetRoute, "/mesh/routing/x", map[string]string{"id": "istio:default:vs:local-vs"})
		if rr.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404 (the object exists only locally): %s", rr.Code, rr.Body.String())
		}
		f.assertNoLocalOrProm(t)
	})
	t.Run("transport error after resolution", func(t *testing.T) {
		remote := newFakeCluster(t, meshLists(MeshIstio), nil)
		remote.dyn.PrependReactor("get", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, unreachableErr
		})
		f := newRemoteFixture(t, remote)
		rr := call(t, f.h.HandleGetRoute, "/mesh/routing/x", map[string]string{"id": "istio:default:vs:local-vs"})
		if rr.Code != http.StatusBadGateway {
			t.Errorf("status %d, want 502: %s", rr.Code, rr.Body.String())
		}
		assertNoLeak(t, rr)
	})
}

// fakeResolver is a PrometheusResolver serving one remote cluster's bound
// Prometheus (pc), or err; neither set means the cluster has no binding.
type fakeResolver struct {
	pc       *monitoring.PrometheusClient
	err      error
	calls    atomic.Int32
	clusters []string
}

func (r *fakeResolver) PrometheusFor(_ context.Context, clusterID string) (*monitoring.PrometheusClient, error) {
	r.calls.Add(1)
	r.clusters = append(r.clusters, clusterID)
	if r.err != nil {
		return nil, r.err
	}
	if r.pc == nil {
		return nil, monitoring.ErrNoMetricsBinding
	}
	return r.pc, nil
}

// boundProm stands up the remote cluster's own Prometheus and returns a
// resolver bound to it, its query counter and its host:port.
func boundProm(t *testing.T, handler http.HandlerFunc) (*fakeResolver, *atomic.Int32, string) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	pc, err := monitoring.NewPrometheusClientWithTransport(srv.URL, http.DefaultTransport)
	if err != nil {
		t.Fatalf("remote prom client: %v", err)
	}
	return &fakeResolver{pc: pc}, &hits, srv.Listener.Addr().String()
}

// mtlsRatios answers the mTLS cross-check: shop/cart saw 9 mTLS requests
// and 1 plaintext one.
func mtlsRatios(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"destination_workload":"cart","destination_workload_namespace":"shop","connection_security_policy":"mutual_tls"},"value":[1,"9"]},
		{"metric":{"destination_workload":"cart","destination_workload_namespace":"shop","connection_security_policy":"none"},"value":[1,"1"]}]}}`)
}

func remoteMTLSCluster(t *testing.T) *fakeCluster {
	t.Helper()
	return newFakeCluster(t, meshLists(MeshIstio),
		[]runtime.Object{newPeerAuth(istioMeshRootNamespace, "default", IstioMTLSStrict, nil)},
		injectedPod("shop", "cart-6d4b7-xyz", map[string]string{"app": "cart"}))
}

// Remote mTLS posture with a binding runs the metric cross-check against the
// remote's own Prometheus: its ratios turn the STRICT cart workload mixed.
func TestRemote_MTLSPostureCrossChecksTheBoundPrometheus(t *testing.T) {
	f := newRemoteFixture(t, remoteMTLSCluster(t))
	resolver, remoteHits, _ := boundProm(t, mtlsRatios)
	f.h.Metrics = resolver

	rr := call(t, f.h.HandleMTLSPosture, "/mesh/mtls", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decode[MTLSPostureResponse](t, rr)
	if len(body.Workloads) != 1 || body.Workloads[0].Workload != "cart" ||
		body.Workloads[0].State != MTLSMixed || body.Workloads[0].Source != MTLSSourceMetric {
		t.Errorf("workloads = %+v, want the remote cart workload mixed from the metric", body.Workloads)
	}
	if msg, ok := body.Errors["prometheus-cross-check"]; ok {
		t.Errorf("errors[prometheus-cross-check] = %q, want none when the cross-check ran", msg)
	}
	if remoteHits.Load() == 0 {
		t.Error("the remote's bound Prometheus received no query")
	}
	if len(resolver.clusters) == 0 || resolver.clusters[0] != remoteCluster {
		t.Errorf("resolver asked for clusters %v, want %q", resolver.clusters, remoteCluster)
	}
	f.assertNoLocalOrProm(t)
}

// Without a binding the remote posture is still served from policies, and
// the cross-check is named as not configured. A binding that cannot be read
// degrades to the fixed cross-check failure text: the posture is still valid.
func TestRemote_MTLSPostureWithoutUsableBinding(t *testing.T) {
	const notConfigured = "metric cross-check is not configured for this cluster; posture derived from policies only"
	tests := []struct {
		name     string
		resolver PrometheusResolver
		wantMsg  string
	}{
		{"no resolver wired", nil, notConfigured},
		{"no binding", &fakeResolver{}, notConfigured},
		{"binding store fails", &fakeResolver{err: &pgconn.PgError{Message: "db at 10.20.30.40 down"}},
			"metric cross-check unavailable; posture derived from policies only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRemoteFixture(t, remoteMTLSCluster(t))
			f.h.Metrics = tt.resolver

			rr := call(t, f.h.HandleMTLSPosture, "/mesh/mtls", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			body := decode[MTLSPostureResponse](t, rr)
			if len(body.Workloads) != 1 || body.Workloads[0].Workload != "cart" ||
				body.Workloads[0].State != MTLSActive || body.Workloads[0].Source != MTLSSourcePolicy {
				t.Errorf("workloads = %+v, want the remote cart workload active from policy", body.Workloads)
			}
			if got := body.Errors["prometheus-cross-check"]; got != tt.wantMsg {
				t.Errorf("errors[prometheus-cross-check] = %q, want %q", got, tt.wantMsg)
			}
			if body.Status.Detected != MeshIstio {
				t.Errorf("status = %+v, want the remote's istio", body.Status)
			}
			assertNoLeak(t, rr)
			f.assertNoLocalOrProm(t)
		})
	}
}

// Remote golden signals read the remote's bound Prometheus and return its
// series; the local Prometheus is never asked.
func TestRemote_GoldenSignalsReadTheBoundPrometheus(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))
	stubs := newFakePromServer(t, []promStub{
		{`response_code=~"5.."`, 2},
		{"0.99", 300},
		{"0.95", 120},
		{"0.50", 40},
		{"istio_requests_total", 50},
	})
	t.Cleanup(stubs.Close)
	resolver, remoteHits, _ := boundProm(t, stubs.Config.Handler.ServeHTTP)
	f.h.Metrics = resolver

	rr := call(t, f.h.HandleGoldenSignals, "/mesh/golden-signals?namespace=shop&service=cart", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[GoldenSignalsResponse](t, rr).Signals
	want := GoldenSignals{Mesh: MeshIstio, Namespace: "shop", Service: "cart", Available: true,
		RPS: 50, ErrorRate: 0.04, P50Ms: 40, P95Ms: 120, P99Ms: 300}
	if got.Mesh != want.Mesh || got.Namespace != want.Namespace || got.Service != want.Service ||
		got.Available != want.Available || got.Reason != "" || len(got.MissingQueries) != 0 ||
		got.RPS != want.RPS || got.ErrorRate != want.ErrorRate ||
		got.P50Ms != want.P50Ms || got.P95Ms != want.P95Ms || got.P99Ms != want.P99Ms {
		t.Errorf("signals = %+v, want %+v", got, want)
	}
	if n := remoteHits.Load(); n != 6 {
		t.Errorf("remote Prometheus received %d queries, want 6", n)
	}
	if len(resolver.clusters) != 1 || resolver.clusters[0] != remoteCluster {
		t.Errorf("resolver asked for clusters %v, want [%q]", resolver.clusters, remoteCluster)
	}
	f.assertNoLocalOrProm(t)
}

// AE6: a remote cluster without a binding answers 200 with reason
// metrics_not_configured, and the local Prometheus receives no request.
func TestRemote_GoldenSignalsWithoutBindingAreNotConfigured(t *testing.T) {
	tests := []struct {
		name     string
		resolver PrometheusResolver
	}{
		{"no resolver wired", nil},
		{"no binding", &fakeResolver{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))
			f.h.Metrics = tt.resolver

			rr := call(t, f.h.HandleGoldenSignals, "/mesh/golden-signals?namespace=shop&service=cart", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			body := decode[GoldenSignalsResponse](t, rr)
			if body.Signals.Available || body.Signals.Reason != "metrics_not_configured" {
				t.Errorf("signals = %+v, want unavailable with reason metrics_not_configured", body.Signals)
			}
			if body.Signals.Mesh != MeshIstio || body.Signals.Namespace != "shop" || body.Signals.Service != "cart" || body.Status.Detected != MeshIstio {
				t.Errorf("signals = %+v, status = %+v, want the remote's istio for shop/cart", body.Signals, body.Status)
			}
			f.assertNoLocalOrProm(t)
		})
	}
}

// A bound Prometheus that fails every query answers reason unreachable, and
// neither its address nor its error text reaches the body.
func TestRemote_GoldenSignalsUnreadablePrometheusIsUnreachable(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))
	resolver, remoteHits, addr := boundProm(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "upstream exploded at 10.20.30.40: connection refused")
	})
	f.h.Metrics = resolver

	rr := call(t, f.h.HandleGoldenSignals, "/mesh/golden-signals?namespace=shop&service=cart", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decode[GoldenSignalsResponse](t, rr)
	if body.Signals.Available || body.Signals.Reason != "unreachable" {
		t.Errorf("signals = %+v, want unavailable with reason unreachable", body.Signals)
	}
	if remoteHits.Load() == 0 {
		t.Error("the remote's bound Prometheus received no query")
	}
	for _, s := range []string{addr, "upstream exploded"} {
		if strings.Contains(rr.Body.String(), s) {
			t.Errorf("response leaks %q: %s", s, rr.Body.String())
		}
	}
	assertNoLeak(t, rr)
	f.assertNoLocalOrProm(t)
}

// A binding that cannot be read is a 503 through the target-error writer,
// never "not configured".
func TestRemote_GoldenSignalsBindingStoreFailureIs503(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))
	f.h.Metrics = &fakeResolver{err: &pgconn.PgError{Message: "db at 10.20.30.40 down"}}

	rr := call(t, f.h.HandleGoldenSignals, "/mesh/golden-signals?namespace=shop&service=cart", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "metrics_not_configured") {
		t.Errorf("a binding-store failure reads as not configured: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "db at") {
		t.Errorf("response leaks the store error: %s", rr.Body.String())
	}
	assertNoLeak(t, rr)
	f.assertNoLocalOrProm(t)
}

// With both meshes on the remote and no ?mesh=, the request is ambiguous:
// 400 before any binding lookup.
func TestRemote_GoldenSignalsDualMeshIs400BeforeBindingLookup(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio, MeshLinkerd), nil))
	resolver, remoteHits, _ := boundProm(t, mtlsRatios)
	f.h.Metrics = resolver

	rr := call(t, f.h.HandleGoldenSignals, "/mesh/golden-signals?namespace=shop&service=cart", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if n := resolver.calls.Load(); n != 0 {
		t.Errorf("resolver called %d times, want 0", n)
	}
	if n := remoteHits.Load(); n != 0 {
		t.Errorf("remote Prometheus received %d queries, want 0", n)
	}
	f.assertNoLocalOrProm(t)
}

// The local cluster keeps its own Prometheus for golden signals and the mTLS
// cross-check: a wired resolver is never consulted.
func TestLocal_MeshMetricsIgnoreTheResolver(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, nil, nil))
	f.h.Discoverer = seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true}})
	resolver := &fakeResolver{err: errors.New("the local cluster must not consult the resolver")}
	f.h.Metrics = resolver

	gs := callOn(t, k8s.LocalClusterID, f.h.HandleGoldenSignals, "/mesh/golden-signals?namespace=shop&service=cart", nil)
	if gs.Code != http.StatusOK {
		t.Fatalf("golden signals status %d: %s", gs.Code, gs.Body.String())
	}
	if sig := decode[GoldenSignalsResponse](t, gs).Signals; !sig.Available || sig.Reason != "" {
		t.Errorf("local signals = %+v, want available from the local Prometheus", sig)
	}
	afterSignals := f.promHits.Load()
	if afterSignals != 6 {
		t.Errorf("local Prometheus received %d golden-signal queries, want 6", afterSignals)
	}

	mtls := callOn(t, k8s.LocalClusterID, f.h.HandleMTLSPosture, "/mesh/mtls", nil)
	if mtls.Code != http.StatusOK {
		t.Fatalf("mtls status %d: %s", mtls.Code, mtls.Body.String())
	}
	if errs := decode[MTLSPostureResponse](t, mtls).Errors; errs["prometheus-cross-check"] != "" {
		t.Errorf("local mtls errors = %v, want no cross-check marker", errs)
	}
	if got := f.promHits.Load() - afterSignals; got != 1 {
		t.Errorf("local Prometheus received %d mTLS queries, want 1", got)
	}
	if n := resolver.calls.Load(); n != 0 {
		t.Errorf("resolver called %d times on the local cluster, want 0", n)
	}
}

// mesh=linkerd is validated against the remote, which serves Istio only,
// not against the local cluster, which serves Linkerd.
func TestRemote_GoldenSignalsValidatesMeshAgainstTheRemote(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio), nil))

	rr := call(t, f.h.HandleGoldenSignals, "/mesh/golden-signals?namespace=shop&service=cart&mesh=linkerd", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

// The topology overlay's provider methods ignore the selected cluster and
// read the local cache only (the topology graph is local-only).
func TestRemote_TopologyProviderStaysLocal(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), []runtime.Object{
		virtualService("shop", "remote-vs", []string{"cart.shop"}, nil),
	})
	f := newRemoteFixture(t, remote)
	f.h.Discoverer = seededDiscoverer(MeshStatus{Detected: MeshIstio, Istio: &MeshInfo{Installed: true}})
	ctx := middleware.WithClusterID(context.Background(), remoteCluster)

	routes, err := f.h.Routes(ctx)
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	if len(routes) != 1 || routes[0].Name != "local-vs" {
		t.Errorf("routes = %+v, want the local cache's local-vs", routes)
	}
	if !f.h.MeshDetected(ctx) {
		t.Error("MeshDetected = false, want the local discoverer's answer")
	}
	if n := f.clients.resolved.Load(); n != 0 {
		t.Errorf("resolved %d cluster clients, want 0", n)
	}
}

// Remote reads are cached per identity and dropped by EvictRemoteCache.
func TestRemote_CacheIsEvictedPerCluster(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), []runtime.Object{
		virtualService("shop", "first", nil, nil),
	})
	f := newRemoteFixture(t, remote)

	if got := decode[routingBody](t, call(t, f.h.HandleListRoutes, "/mesh/routing", nil)).Routes; len(got) != 1 {
		t.Fatalf("routes = %+v, want one", got)
	}
	if err := remote.dyn.Tracker().Create(IstioVirtualServiceGVR, virtualService("shop", "second", nil, nil), "shop"); err != nil {
		t.Fatal(err)
	}
	if got := decode[routingBody](t, call(t, f.h.HandleListRoutes, "/mesh/routing", nil)).Routes; len(got) != 1 {
		t.Errorf("routes = %+v, want the cached single route", got)
	}
	f.h.EvictRemoteCache(remoteCluster)
	if got := decode[routingBody](t, call(t, f.h.HandleListRoutes, "/mesh/routing", nil)).Routes; len(got) != 2 {
		t.Errorf("routes = %+v, want both after eviction", got)
	}
}

func controlPlaneObjects() []runtime.Object {
	return []runtime.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod", Namespace: istioSystemNS, Labels: map[string]string{"app": "istiod"}},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "discovery", Image: "docker.io/istio/pilot:1.25.1"}},
			}}},
		},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ztunnel", Namespace: istioSystemNS, Labels: map[string]string{"app": "ztunnel"}}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "linkerd-identity", Namespace: linkerdControlNS,
				Labels: map[string]string{"linkerd.io/control-plane-component": "identity", "linkerd.io/control-plane-version": "edge-25.4.1"}},
		},
	}
}

// The remote's control plane fills in version, namespace and mode the way
// the local Discoverer does, read as the user on the remote.
func TestRemote_StatusReportsTheRemoteControlPlane(t *testing.T) {
	f := newRemoteFixture(t, newFakeCluster(t, meshLists(MeshIstio, MeshLinkerd), nil, controlPlaneObjects()...))

	rr := call(t, f.h.HandleStatus, "/mesh/status", nil)
	got := decode[MeshStatusResponse](t, rr).Status
	wantIstio := MeshInfo{Installed: true, Namespace: istioSystemNS, Version: "1.25.1", Mode: MeshModeAmbient}
	wantLinkerd := MeshInfo{Installed: true, Namespace: linkerdControlNS, Version: "edge-25.4.1"}
	if got.Istio == nil || *got.Istio != wantIstio || got.Linkerd == nil || *got.Linkerd != wantLinkerd {
		t.Errorf("status = istio %+v linkerd %+v, want %+v and %+v", got.Istio, got.Linkerd, wantIstio, wantLinkerd)
	}

	// The routing list's status names the same control plane.
	body := decode[routingBody](t, call(t, f.h.HandleListRoutes, "/mesh/routing", nil))
	if body.Status.Istio == nil || *body.Status.Istio != wantIstio {
		t.Errorf("routing status istio = %+v, want %+v", body.Status.Istio, wantIstio)
	}
	f.assertNoLocalOrProm(t)
}

// A control plane the user cannot read leaves the defaults rather than
// failing the status.
func TestRemote_UnreadableControlPlaneKeepsDefaults(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), nil, controlPlaneObjects()...)
	remote.typed.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("denied"))
	})
	f := newRemoteFixture(t, remote)

	got := decode[MeshStatusResponse](t, call(t, f.h.HandleStatus, "/mesh/status", nil)).Status
	want := MeshInfo{Installed: true, Version: versionUnknown, Mode: MeshModeSidecar}
	if got.Istio == nil || *got.Istio != want {
		t.Errorf("istio = %+v, want the defaults %+v", got.Istio, want)
	}
}

// Remote status is cached per identity, so a status poll does not re-list
// the control plane each time, and eviction drops it.
func TestRemote_StatusIsCachedUntilEvicted(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), nil, controlPlaneObjects()...)
	f := newRemoteFixture(t, remote)

	call(t, f.h.HandleStatus, "/mesh/status", nil)
	first := len(remote.typed.Actions())
	if first == 0 {
		t.Fatal("status did not read the remote control plane")
	}
	call(t, f.h.HandleStatus, "/mesh/status", nil)
	if n := len(remote.typed.Actions()); n != first {
		t.Errorf("second status made %d more control-plane reads, want 0", n-first)
	}
	f.h.EvictRemoteCache(remoteCluster)
	call(t, f.h.HandleStatus, "/mesh/status", nil)
	if n := len(remote.typed.Actions()); n == first {
		t.Error("status after eviction did not re-read the control plane")
	}
}

// The status route and the list snapshot share one read of the remote's
// discovery and control plane.
func TestRemote_StatusAndListsShareOneControlPlaneRead(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), nil, controlPlaneObjects()...)
	f := newRemoteFixture(t, remote)

	call(t, f.h.HandleStatus, "/mesh/status", nil)
	afterStatus := len(remote.typed.Actions())
	call(t, f.h.HandleListRoutes, "/mesh/routing", nil)
	call(t, f.h.HandleListPolicies, "/mesh/policies", nil)
	if n := len(remote.typed.Actions()); n != afterStatus {
		t.Errorf("lists made %d more control-plane reads, want 0", n-afterStatus)
	}
}

// A remote mTLS request the user may not make is refused before any mesh
// list reaches the remote.
func TestRemote_MTLSDeniedListsNothing(t *testing.T) {
	remote := newFakeCluster(t, meshLists(MeshIstio), nil)
	f := newRemoteFixture(t, remote)
	f.h.AccessChecker = resources.NewAlwaysDenyAccessChecker()

	rr := call(t, f.h.HandleMTLSPosture, "/mesh/mtls", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if n := len(remote.dyn.Actions()); n != 0 {
		t.Errorf("remote recorded %d mesh list actions, want 0", n)
	}
}

// brokenDiscovery lists the served resources but reports the listed groups
// as failed, or fails outright when lists is nil: an aggregated APIService
// that is down, or an API server that cannot serve discovery at all.
type brokenDiscovery struct {
	*fakediscovery.FakeDiscovery
	lists  []*metav1.APIResourceList
	failed []schema.GroupVersion
}

func (d brokenDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	if d.lists == nil {
		return nil, nil, errors.New("the server is currently unable to handle the request")
	}
	groups := map[schema.GroupVersion]error{}
	for _, gv := range d.failed {
		groups[gv] = errors.New("the server is currently unable to handle the request")
	}
	return nil, d.lists, &discovery.ErrGroupDiscoveryFailed{Groups: groups}
}

// A remote whose discovery cannot be read, wholly or for one mesh group,
// is unknown, never "no mesh": status says why and lists fail with 502.
func TestRemote_DiscoveryFailureIsUnknownNotAbsent(t *testing.T) {
	tests := []struct {
		name string
		disc func(*fakediscovery.FakeDiscovery) discovery.DiscoveryInterface
	}{
		{"whole discovery fails", func(fd *fakediscovery.FakeDiscovery) discovery.DiscoveryInterface {
			return brokenDiscovery{FakeDiscovery: fd}
		}},
		{"linkerd group fails", func(fd *fakediscovery.FakeDiscovery) discovery.DiscoveryInterface {
			return brokenDiscovery{FakeDiscovery: fd, lists: meshLists(MeshIstio),
				failed: []schema.GroupVersion{LinkerdServerGVR.GroupVersion()}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := newFakeCluster(t, meshLists(MeshIstio), nil)
			remote.discOverride = tt.disc(remote.disc)
			f := newRemoteFixture(t, remote)

			status := decode[MeshStatusResponse](t, call(t, f.h.HandleStatus, "/mesh/status", nil)).Status
			if status.Detected != MeshNone || status.Reason != string(k8s.ReasonDiscoveryUnavailable) {
				t.Errorf("status = %+v, want detected none with reason discovery_unavailable", status)
			}
			rr := call(t, f.h.HandleListRoutes, "/mesh/routing", nil)
			if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), string(k8s.ReasonDiscoveryUnavailable)) {
				t.Errorf("routing: status %d, want 502 discovery_unavailable: %s", rr.Code, rr.Body.String())
			}
			f.assertNoLocalOrProm(t)
		})
	}
}
