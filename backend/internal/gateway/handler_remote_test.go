package gateway

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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const remoteCluster = "remote-1"

// fakeCluster is one fake cluster: its discovery, dynamic and typed clients.
type fakeCluster struct {
	disc  *fakediscovery.FakeDiscovery
	dyn   *dynfake.FakeDynamicClient
	typed *kfake.Clientset
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. A
// target error makes every resolution for that cluster fail.
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
	return &k8s.TargetSchema{ClusterID: id, Discovery: c.disc, Invalidate: func() {}}, nil
}

var listKinds = map[schema.GroupVersionResource]string{
	GatewayClassGVR: "GatewayClassList",
	GatewayGVR:      "GatewayList",
	HTTPRouteGVR:    "HTTPRouteList",
	GRPCRouteGVR:    "GRPCRouteList",
	TCPRouteGVR:     "TCPRouteList",
	{Group: APIGroup, Version: "v1alpha2", Resource: "tlsroutes"}: "TLSRouteList",
	{Group: APIGroup, Version: "v1", Resource: "tlsroutes"}:       "TLSRouteList",
	UDPRouteGVR: "UDPRouteList",
}

func gwObj(apiVersion, kind, ns, name string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": ns},
	}}
	if spec != nil {
		u.Object["spec"] = spec
	}
	return u
}

func resourceList(gv string, kinds map[string]string) *metav1.APIResourceList {
	l := &metav1.APIResourceList{GroupVersion: gv}
	for kind, resource := range kinds {
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: resource, Kind: kind, Namespaced: kind != "GatewayClass"})
	}
	return l
}

// v1Lists serves Gateway API v1 core kinds, plus TLSRoute at tlsVersion
// ("" for none).
func v1Lists(tlsVersion string) []*metav1.APIResourceList {
	v1 := map[string]string{"GatewayClass": "gatewayclasses", "Gateway": "gateways", "HTTPRoute": "httproutes"}
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "services", Kind: "Service", Namespaced: true}}}}
	switch tlsVersion {
	case "v1":
		v1["TLSRoute"] = "tlsroutes"
	case "v1alpha2":
		lists = append(lists, resourceList(APIGroup+"/v1alpha2", map[string]string{"TLSRoute": "tlsroutes"}))
	}
	return append(lists, resourceList(APIGroup+"/v1", v1))
}

// newFakeCluster seeds each object under the resource kindToResource names
// for it: the fake's own kind-to-resource guess does not track Gateway.
func newFakeCluster(t *testing.T, lists []*metav1.APIResourceList, objs ...*unstructured.Unstructured) *fakeCluster {
	t.Helper()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds)
	for _, obj := range objs {
		gvr := obj.GroupVersionKind().GroupVersion().WithResource(kindToResource[obj.GetKind()])
		if err := dyn.Tracker().Create(gvr, obj, obj.GetNamespace()); err != nil {
			t.Fatalf("seed %s: %v", obj.GetName(), err)
		}
	}
	return &fakeCluster{
		disc:  &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: lists}},
		dyn:   dyn,
		typed: kfake.NewSimpleClientset(),
	}
}

// remoteHandler builds a Handler whose remote cluster serves remoteObjs.
// The local cluster, reachable only through the same ClusterClients, is
// seeded with its own Gateway so a local read on the remote path shows up
// in its recorded actions. K8sClient and the local Discoverer are nil, so a
// service-account or local-discovery read on the remote path panics.
func remoteHandler(t *testing.T, remoteLists []*metav1.APIResourceList, remoteObjs ...*unstructured.Unstructured) (*Handler, *fakeClients) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clients := &fakeClients{clusters: map[string]*fakeCluster{
		remoteCluster: newFakeCluster(t, remoteLists, remoteObjs...),
		"local": newFakeCluster(t, v1Lists(""),
			gwObj("gateway.networking.k8s.io/v1", "Gateway", "default", "local-gw", nil)),
	}}
	h := NewHandler(nil, nil, resources.NewAlwaysAllowAccessChecker(), clients, k8s.NewPresence(clients), logger)
	return h, clients
}

func localActions(c *fakeClients) int {
	l := c.clusters["local"]
	return len(l.dyn.Actions()) + len(l.typed.Actions())
}

func call(t *testing.T, h http.HandlerFunc, path string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}})
	ctx = middleware.WithClusterID(ctx, remoteCluster)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var resp struct {
		Data  T             `json:"data"`
		Error *api.APIError `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return resp.Data
}

func TestRemote_ListGatewaysReturnsRemoteObjects(t *testing.T) {
	h, clients := remoteHandler(t, v1Lists(""),
		gwObj("gateway.networking.k8s.io/v1", "Gateway", "edge", "remote-gw", nil))

	rr := call(t, h.HandleListGateways, "/gateways", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[[]GatewaySummary](t, rr)
	if len(got) != 1 || got[0].Name != "remote-gw" {
		t.Errorf("gateways = %+v, want only remote-gw", got)
	}
	if n := localActions(clients); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

// A remote serving TLSRoute only at v1alpha2 is listed through v1alpha2, and
// one serving it at v1 through v1 (the version is the remote's, not a
// hard-coded constant).
func TestRemote_TLSRouteListedAtTheRemotesVersion(t *testing.T) {
	for _, version := range []string{"v1alpha2", "v1"} {
		t.Run(version, func(t *testing.T) {
			h, _ := remoteHandler(t, v1Lists(version),
				gwObj("gateway.networking.k8s.io/"+version, "TLSRoute", "edge", "tls-"+version, nil))

			rr := call(t, h.HandleListRoutes, "/routes?kind=tlsroutes", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			got := decode[[]RouteSummary](t, rr)
			if len(got) != 1 || got[0].Name != "tls-"+version {
				t.Errorf("routes = %+v, want tls-%s", got, version)
			}
		})
	}
}

func TestRemote_RouteRelationshipsResolveOnTheRemote(t *testing.T) {
	route := gwObj("gateway.networking.k8s.io/v1", "HTTPRoute", "edge", "web", map[string]any{
		"parentRefs": []any{map[string]any{"name": "remote-gw"}},
		"rules": []any{map[string]any{
			"backendRefs": []any{map[string]any{"name": "web-svc", "port": int64(80)}},
		}},
	})
	gw := gwObj("gateway.networking.k8s.io/v1", "Gateway", "edge", "remote-gw", nil)
	gw.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Programmed", "status": "True"}}}
	h, clients := remoteHandler(t, v1Lists(""), route, gw)
	remote := clients.clusters[remoteCluster]
	if _, err := remote.typed.CoreV1().Services("edge").Create(context.Background(),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web-svc", Namespace: "edge"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	rr := call(t, h.HandleGetHTTPRoute, "/httproutes/edge/web", map[string]string{"namespace": "edge", "name": "web"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	detail := decode[HTTPRouteDetail](t, rr)
	if len(detail.ParentRefs) != 1 || len(detail.ParentRefs[0].GatewayConditions) == 0 {
		t.Errorf("parent gateway not resolved on the remote: %+v", detail.ParentRefs)
	}
	if len(detail.Rules) != 1 || len(detail.Rules[0].BackendRefs) != 1 || !detail.Rules[0].BackendRefs[0].Resolved {
		t.Errorf("backend service not resolved on the remote: %+v", detail.Rules)
	}
	if n := localActions(clients); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_UnreachableReturns502WithoutTouchingLocal(t *testing.T) {
	h, clients := remoteHandler(t, v1Lists(""))
	clients.targetErr = &url.Error{Op: "Get", URL: "https://10.20.30.40:6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}

	rr := call(t, h.HandleListGateways, "/gateways", nil)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "10.20.30.40") {
		t.Errorf("response leaks the remote address: %s", rr.Body.String())
	}
}

// One source failing on the remote fails only its own endpoint; the other
// kinds still list, and the summary, which spans every source, reports the
// failure instead of silently undercounting.
func TestRemote_ForbiddenKindFailsOnlyItsOwnEndpoint(t *testing.T) {
	h, clients := remoteHandler(t, v1Lists(""),
		gwObj("gateway.networking.k8s.io/v1", "Gateway", "edge", "remote-gw", nil),
		gwObj("gateway.networking.k8s.io/v1", "HTTPRoute", "edge", "web", nil))
	clients.clusters[remoteCluster].dyn.PrependReactor("list", "httproutes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(HTTPRouteGVR.GroupResource(), "", errors.New("no"))
	})

	if rr := call(t, h.HandleListHTTPRoutes, "/httproutes", nil); rr.Code != http.StatusForbidden {
		t.Errorf("httproutes status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	rr := call(t, h.HandleListGateways, "/gateways", nil)
	if rr.Code != http.StatusOK || len(decode[[]GatewaySummary](t, rr)) != 1 {
		t.Errorf("gateways still list: status %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(t, h.HandleSummary, "/summary", nil); rr.Code == http.StatusOK {
		t.Errorf("summary must not silently undercount a failed source: %s", rr.Body.String())
	}
}

func TestRemote_StatusFollowsTheRemotesDiscovery(t *testing.T) {
	h, _ := remoteHandler(t, v1Lists("v1alpha2"))
	st := decode[GatewayAPIStatus](t, call(t, h.HandleStatus, "/status", nil))
	if !st.Available || st.Reason != "" {
		t.Errorf("status = %+v, want available with no reason", st)
	}
	if !strings.Contains(strings.Join(st.InstalledKinds, ","), "tlsroutes") {
		t.Errorf("installed kinds = %v, want tlsroutes from the remote's v1alpha2", st.InstalledKinds)
	}

	absent, _ := remoteHandler(t, []*metav1.APIResourceList{{GroupVersion: "v1"}})
	st = decode[GatewayAPIStatus](t, call(t, absent.HandleStatus, "/status", nil))
	if st.Available || st.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("status = %+v, want not available with discovery_missing", st)
	}
	if rr := call(t, absent.HandleListGateways, "/gateways", nil); rr.Code != http.StatusOK || len(decode[[]GatewaySummary](t, rr)) != 0 {
		t.Errorf("not-installed remote must list nothing, got %d %s", rr.Code, rr.Body.String())
	}

	down, clients := remoteHandler(t, v1Lists(""))
	clients.targetErr = &net.DNSError{Err: "no such host", Name: "api.example.test"}
	st = decode[GatewayAPIStatus](t, call(t, down.HandleStatus, "/status", nil))
	if st.Available || st.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("status = %+v, want not available with unreachable", st)
	}
}

// A route's detail is read at the version the remote serves it.
func TestRemote_GetRouteUsesTheRemotesVersion(t *testing.T) {
	h, _ := remoteHandler(t, v1Lists("v1"),
		gwObj("gateway.networking.k8s.io/v1", "TLSRoute", "edge", "tls", nil))

	rr := call(t, h.HandleGetRoute, "/routes/tlsroutes/edge/tls", map[string]string{"kind": "tlsroutes", "namespace": "edge", "name": "tls"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
}

func TestStatusFromLists(t *testing.T) {
	cases := []struct {
		name      string
		lists     []*metav1.APIResourceList
		available bool
		tls       string // served TLSRoute version, "" when not installed
	}{
		{"no gateway API", []*metav1.APIResourceList{{GroupVersion: "v1"}}, false, ""},
		{"missing GatewayClass", []*metav1.APIResourceList{resourceList(APIGroup+"/v1", map[string]string{"Gateway": "gateways"})}, false, ""},
		{"core only", v1Lists(""), true, ""},
		{"TLSRoute at v1alpha2", v1Lists("v1alpha2"), true, "v1alpha2"},
		{"TLSRoute promoted to v1", append(v1Lists("v1"), resourceList(APIGroup+"/v1alpha2", map[string]string{"TLSRoute": "tlsroutes"})), true, "v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := statusFromLists(tc.lists)
			if st.Available != tc.available {
				t.Fatalf("available = %v, want %v", st.Available, tc.available)
			}
			gvr, installed := st.routeGVRs[RouteKindTLS]
			if tc.tls == "" && installed || tc.tls != "" && gvr.Version != tc.tls {
				t.Errorf("TLSRoute = %v (installed %v), want version %q", gvr, installed, tc.tls)
			}
		})
	}
}

func TestMatchesParentRefDefaultsToTheRouteNamespace(t *testing.T) {
	refs := []ParentRef{{Name: "gw"}}
	if !matchesParentRef(refs, "edge", "gw", "edge") {
		t.Error("an unset parentRef namespace must mean the route's namespace")
	}
	if matchesParentRef(refs, "edge", "gw", "other") {
		t.Error("a parentRef must not match a same-named gateway in another namespace")
	}
}
