package topology

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/servicemesh"
)

// fakeLister returns a fixed set of services in a namespace and empties for
// every other resource kind. Enough for D1's overlay path which only needs
// Service nodes in the nameIndex.
type fakeLister struct {
	services []*corev1.Service
}

func (f *fakeLister) ListPods(_ context.Context, _ string) ([]*corev1.Pod, error) {
	return nil, nil
}
func (f *fakeLister) ListServices(_ context.Context, namespace string) ([]*corev1.Service, error) {
	out := make([]*corev1.Service, 0, len(f.services))
	for _, s := range f.services {
		if s.Namespace == namespace {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeLister) ListDeployments(_ context.Context, _ string) ([]*appsv1.Deployment, error) {
	return nil, nil
}
func (f *fakeLister) ListReplicaSets(_ context.Context, _ string) ([]*appsv1.ReplicaSet, error) {
	return nil, nil
}
func (f *fakeLister) ListStatefulSets(_ context.Context, _ string) ([]*appsv1.StatefulSet, error) {
	return nil, nil
}
func (f *fakeLister) ListDaemonSets(_ context.Context, _ string) ([]*appsv1.DaemonSet, error) {
	return nil, nil
}
func (f *fakeLister) ListJobs(_ context.Context, _ string) ([]*batchv1.Job, error) {
	return nil, nil
}
func (f *fakeLister) ListCronJobs(_ context.Context, _ string) ([]*batchv1.CronJob, error) {
	return nil, nil
}
func (f *fakeLister) ListIngresses(_ context.Context, _ string) ([]*networkingv1.Ingress, error) {
	return nil, nil
}
func (f *fakeLister) ListConfigMaps(_ context.Context, _ string) ([]*corev1.ConfigMap, error) {
	return nil, nil
}
func (f *fakeLister) ListPVCs(_ context.Context, _ string) ([]*corev1.PersistentVolumeClaim, error) {
	return nil, nil
}
func (f *fakeLister) ListHPAs(_ context.Context, _ string) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	return nil, nil
}

// fakeMeshProvider returns canned routes (or an error) so we can drive
// the overlay path deterministically. detected defaults to true so most
// existing tests don't have to opt in; tests covering the no-mesh path
// set it explicitly.
type fakeMeshProvider struct {
	routes      []servicemesh.TrafficRoute
	err         error
	detectedSet bool
	detected    bool
}

func (p *fakeMeshProvider) Routes(_ context.Context) ([]servicemesh.TrafficRoute, error) {
	return p.routes, p.err
}

func (p *fakeMeshProvider) MeshDetected(_ context.Context) bool {
	if p.detectedSet {
		return p.detected
	}
	return true
}

func svc(namespace, name, uid string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID(uid)},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
	}
}

func testUser() *auth.User {
	return &auth.User{KubernetesUsername: "u", KubernetesGroups: []string{"g"}}
}

func newTestBuilder(provider MeshRouteProvider, services ...*corev1.Service) *Builder {
	return NewBuilder(&fakeLister{services: services}, provider, slog.Default())
}

// TestBuilder_DefaultPath_NoOverlayField confirms the byte-level invariant
// that pre-D1 callers must observe: without ?overlay=, the response carries
// no Overlay value, no mesh edges, and Phase 7B clients see zero behavioral
// change.
func TestBuilder_DefaultPath_NoOverlayField(t *testing.T) {
	b := newTestBuilder(&fakeMeshProvider{
		routes: []servicemesh.TrafficRoute{
			{Mesh: servicemesh.MeshIstio, Kind: "VirtualService", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		},
	}, svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	graph, err := b.BuildNamespaceGraph(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker())
	if err != nil {
		t.Fatalf("BuildNamespaceGraph: %v", err)
	}
	if graph.Overlay != "" {
		t.Errorf("Overlay = %q, want empty string (no-overlay path must not set Overlay)", graph.Overlay)
	}
	for _, e := range graph.Edges {
		if e.Type == EdgeMeshVS || e.Type == EdgeMeshSP {
			t.Errorf("found mesh edge %+v on no-overlay path; default response must be byte-identical", e)
		}
	}
}

func TestBuilder_OverlayMesh_HappyPath(t *testing.T) {
	b := newTestBuilder(&fakeMeshProvider{
		routes: []servicemesh.TrafficRoute{
			{Mesh: servicemesh.MeshIstio, Kind: "VirtualService", Namespace: "foo", Name: "vs", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		},
	}, svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if graph.Overlay != "mesh" {
		t.Errorf("Overlay = %q, want %q", graph.Overlay, "mesh")
	}
	if !findEdge(graph.Edges, "uid-a", "uid-b", EdgeMeshVS) {
		t.Errorf("missing mesh_vs edge a->b in graph edges %+v", graph.Edges)
	}
}

func TestBuilder_OverlayMesh_NilProviderUnavailable(t *testing.T) {
	b := NewBuilder(&fakeLister{services: []*corev1.Service{svc("foo", "a", "uid-a")}}, nil, slog.Default())

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if graph.Overlay != "unavailable" {
		t.Errorf("Overlay = %q, want %q (nil provider must degrade)", graph.Overlay, "unavailable")
	}
	for _, e := range graph.Edges {
		if e.Type == EdgeMeshVS || e.Type == EdgeMeshSP {
			t.Errorf("unexpected mesh edge %+v with nil provider", e)
		}
	}
}

func TestBuilder_OverlayMesh_ProviderErrorUnavailable(t *testing.T) {
	b := newTestBuilder(&fakeMeshProvider{err: errors.New("transient")},
		svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if graph.Overlay != "unavailable" {
		t.Errorf("Overlay = %q, want %q (provider error must degrade, not 5xx)", graph.Overlay, "unavailable")
	}
}

func TestBuilder_OverlayMesh_DetectedButNoRoutesSetsMesh(t *testing.T) {
	// Mesh is installed (MeshDetected=true) but no routes have been
	// declared yet. The overlay reports "mesh" (we asked a real mesh);
	// the empty-edges case is the user's signal that nothing is configured.
	b := newTestBuilder(&fakeMeshProvider{routes: nil},
		svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if graph.Overlay != OverlayMesh {
		t.Errorf("Overlay = %q, want %q (mesh installed, no routes — still 'mesh')", graph.Overlay, OverlayMesh)
	}
}

func TestBuilder_OverlayMesh_NotDetectedYieldsUnavailable(t *testing.T) {
	// No mesh installed in the cluster. The frontend disable path keys
	// off "unavailable", so this case must NOT report OverlayMesh — that
	// would leave the user thinking their mesh is silent when in fact
	// they don't have a mesh at all.
	b := newTestBuilder(&fakeMeshProvider{detectedSet: true, detected: false},
		svc("foo", "a", "uid-a"))

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if graph.Overlay != OverlayUnavailable {
		t.Errorf("Overlay = %q, want %q (no mesh installed must yield unavailable)", graph.Overlay, OverlayUnavailable)
	}
}

func TestBuilder_OverlayMesh_RBACDeniedYieldsNoEdges(t *testing.T) {
	b := newTestBuilder(&fakeMeshProvider{
		routes: []servicemesh.TrafficRoute{
			{Mesh: servicemesh.MeshIstio, Kind: "VirtualService", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		},
	}, svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysDenyAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if graph.Overlay != "mesh" {
		t.Errorf("Overlay = %q, want %q (RBAC denial still applies the overlay, just with no edges)", graph.Overlay, "mesh")
	}
	for _, e := range graph.Edges {
		if e.Type == EdgeMeshVS || e.Type == EdgeMeshSP {
			t.Errorf("user denied list-virtualservices but got mesh edge %+v", e)
		}
	}
}

func TestBuilder_OverlayMesh_OnlyAllowedCRDContributesEdges(t *testing.T) {
	// Mix Istio + Linkerd routes. Allow-all checker → both contribute.
	// This test asserts the allow-all baseline; the partial-RBAC case
	// is exercised in TestBuilder_OverlayMesh_PartialRBAC.
	b := newTestBuilder(&fakeMeshProvider{
		routes: []servicemesh.TrafficRoute{
			{Mesh: servicemesh.MeshIstio, Kind: "VirtualService", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
			{Mesh: servicemesh.MeshLinkerd, Kind: "ServiceProfile", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		},
	}, svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if !findEdge(graph.Edges, "uid-a", "uid-b", EdgeMeshVS) {
		t.Error("missing mesh_vs edge")
	}
	if !findEdge(graph.Edges, "uid-a", "uid-b", EdgeMeshSP) {
		t.Error("missing mesh_sp edge")
	}
}

func TestBuilder_OverlayMesh_PartialRBAC(t *testing.T) {
	// User can list networking.istio.io/virtualservices but NOT
	// linkerd.io/serviceprofiles. The overlay must surface mesh_vs
	// edges and silently drop mesh_sp edges; the headline guarantee
	// of Phase D's per-CRD-group RBAC story.
	checker := resources.NewPredicateAccessChecker(func(_, apiGroup, resource, _ string) bool {
		return apiGroup == "networking.istio.io" && resource == "virtualservices"
	})

	b := newTestBuilder(&fakeMeshProvider{
		routes: []servicemesh.TrafficRoute{
			{Mesh: servicemesh.MeshIstio, Kind: "VirtualService", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
			{Mesh: servicemesh.MeshLinkerd, Kind: "ServiceProfile", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		},
	}, svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), checker, "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	if graph.Overlay != OverlayMesh {
		t.Errorf("Overlay = %q, want %q", graph.Overlay, OverlayMesh)
	}
	if !findEdge(graph.Edges, "uid-a", "uid-b", EdgeMeshVS) {
		t.Error("missing mesh_vs edge — VS group was allowed but no edges emitted")
	}
	if findEdge(graph.Edges, "uid-a", "uid-b", EdgeMeshSP) {
		t.Error("found mesh_sp edge despite SP group denial — RBAC gate is not per-(apiGroup, resource)")
	}
}

func TestBuilder_OverlayMesh_TruncationFlagged(t *testing.T) {
	// To exercise the mesh-edge cap (maxMeshEdges = 2000) without tripping
	// the unrelated node cap (maxNodes = 2000), use 60 services and one VS
	// per service that routes to every other service. That yields up to
	// 60 * 59 = 3540 candidate edges with only 60 nodes — well over the
	// edge cap and well under the node cap.
	const n = 60
	services := make([]*corev1.Service, n)
	names := make([]string, n)
	for i := range services {
		names[i] = "s" + itoa(i)
		services[i] = svc("foo", names[i], "uid-"+names[i])
	}

	routes := make([]servicemesh.TrafficRoute, n)
	for i := range routes {
		dests := make([]servicemesh.RouteDestination, 0, n-1)
		for j, target := range names {
			if j == i {
				continue
			}
			dests = append(dests, servicemesh.RouteDestination{Host: target})
		}
		routes[i] = servicemesh.TrafficRoute{
			Mesh:         servicemesh.MeshIstio,
			Kind:         "VirtualService",
			Namespace:    "foo",
			Name:         "vs-" + names[i],
			Hosts:        []string{names[i]},
			Destinations: dests,
		}
	}

	b := newTestBuilder(&fakeMeshProvider{routes: routes}, services...)

	graph, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "mesh")
	if err != nil {
		t.Fatalf("BuildNamespaceGraphWithOverlay: %v", err)
	}
	// Mesh-edge truncation flips EdgesTruncated specifically — the
	// existing Truncated flag stays reserved for the maxNodes cap.
	if !graph.EdgesTruncated {
		t.Error("EdgesTruncated = false, want true at mesh-edge cap")
	}
	if graph.Truncated {
		t.Error("Truncated = true, want false (node cap not hit)")
	}

	var meshEdges int
	for _, e := range graph.Edges {
		if e.Type == EdgeMeshVS {
			meshEdges++
		}
	}
	if meshEdges != maxMeshEdges {
		t.Errorf("mesh edges = %d, want %d", meshEdges, maxMeshEdges)
	}
}

func TestBuilder_OverlayInvalidValueReturnsError(t *testing.T) {
	b := newTestBuilder(nil, svc("foo", "a", "uid-a"))

	_, err := b.BuildNamespaceGraphWithOverlay(context.Background(), "foo", testUser(), resources.NewAlwaysAllowAccessChecker(), "garbage")
	if err == nil {
		t.Fatal("expected error for unsupported overlay")
	}
	if !strings.HasPrefix(err.Error(), "unsupported overlay") {
		t.Errorf("error = %q, want prefix \"unsupported overlay\" (handler dispatches on this)", err.Error())
	}
}

// TestHandler_NoOverlayParamUnchanged covers the same byte-level invariant at
// the HTTP layer: no ?overlay= → no Overlay field in the response.
func TestHandler_NoOverlayParamUnchanged(t *testing.T) {
	b := newTestBuilder(&fakeMeshProvider{
		routes: []servicemesh.TrafficRoute{
			{Mesh: servicemesh.MeshIstio, Kind: "VirtualService", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		},
	}, svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	h := &Handler{Builder: b, AccessChecker: resources.NewAlwaysAllowAccessChecker(), Logger: slog.Default()}
	w := callTopologyHandler(t, h, "foo", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), `"overlay"`) {
		t.Errorf("response contains \"overlay\" field on no-overlay path; want field absent.\nbody: %s", w.Body.String())
	}
}

func TestHandler_InvalidOverlayReturns400(t *testing.T) {
	b := newTestBuilder(nil, svc("foo", "a", "uid-a"))
	h := &Handler{Builder: b, AccessChecker: resources.NewAlwaysAllowAccessChecker(), Logger: slog.Default()}

	w := callTopologyHandler(t, h, "foo", "garbage")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid overlay value", w.Code)
	}
}

func TestHandler_OverlayMeshIncludesEdges(t *testing.T) {
	b := newTestBuilder(&fakeMeshProvider{
		routes: []servicemesh.TrafficRoute{
			{Mesh: servicemesh.MeshIstio, Kind: "VirtualService", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		},
	}, svc("foo", "a", "uid-a"), svc("foo", "b", "uid-b"))

	h := &Handler{Builder: b, AccessChecker: resources.NewAlwaysAllowAccessChecker(), Logger: slog.Default()}
	w := callTopologyHandler(t, h, "foo", "mesh")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"overlay":"mesh"`) {
		t.Errorf("response missing overlay=\"mesh\"; body: %s", body)
	}
	if !strings.Contains(body, string(EdgeMeshVS)) {
		t.Errorf("response missing mesh_vs edge type; body: %s", body)
	}
}

// callTopologyHandler invokes h.HandleNamespaceGraph for a synthetic request
// at /api/v1/topology/{namespace}?overlay={overlay}.
func callTopologyHandler(t *testing.T, h *Handler, namespace, overlay string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/topology/" + namespace
	if overlay != "" {
		url += "?overlay=" + overlay
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), testUser()))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", namespace)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.HandleNamespaceGraph(w, req)
	return w
}
