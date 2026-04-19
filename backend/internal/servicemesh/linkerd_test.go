package servicemesh

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// --- fake dynamic client ----------------------------------------------------

// upstreamHTTPRouteGVR is the Gateway-API standard HTTPRoute, distinct from
// Linkerd's policy.linkerd.io flavor. Kept local to the test file because the
// production adapter intentionally never lists it.
var upstreamHTTPRouteGVR = schema.GroupVersionResource{
	Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes",
}

func linkerdScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	registrations := []struct {
		gvr  schema.GroupVersionResource
		kind string
	}{
		{LinkerdServiceProfileGVR, "ServiceProfile"},
		{LinkerdServerGVR, "Server"},
		{LinkerdHTTPRouteGVR, "HTTPRoute"},
		{LinkerdAuthorizationPolicyGVR, "AuthorizationPolicy"},
		{LinkerdMeshTLSAuthenticationGVR, "MeshTLSAuthentication"},
		// Register upstream HTTPRoute too so the fake can hold both flavors
		// and the adapter can prove it only picks up Linkerd's.
		{upstreamHTTPRouteGVR, "HTTPRoute"},
	}
	for _, r := range registrations {
		gvk := schema.GroupVersionKind{Group: r.gvr.Group, Version: r.gvr.Version, Kind: r.kind}
		s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		s.AddKnownTypeWithName(
			schema.GroupVersionKind{Group: r.gvr.Group, Version: r.gvr.Version, Kind: r.kind + "List"},
			&unstructured.UnstructuredList{},
		)
	}
	return s
}

func newLinkerdFakeDynClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	gvrToListKind := map[schema.GroupVersionResource]string{
		LinkerdServiceProfileGVR:        "ServiceProfileList",
		LinkerdServerGVR:                "ServerList",
		LinkerdHTTPRouteGVR:             "HTTPRouteList",
		LinkerdAuthorizationPolicyGVR:   "AuthorizationPolicyList",
		LinkerdMeshTLSAuthenticationGVR: "MeshTLSAuthenticationList",
		upstreamHTTPRouteGVR:            "HTTPRouteList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(linkerdScheme(), gvrToListKind, objects...)
}

// --- fixtures ---------------------------------------------------------------

func serviceProfile(ns, name string, routes []map[string]any) *unstructured.Unstructured {
	routesAny := make([]any, len(routes))
	for i, r := range routes {
		routesAny[i] = r
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "linkerd.io/v1alpha2",
		"kind":       "ServiceProfile",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"routes": routesAny},
	}}
}

func linkerdServer(ns, name string, matchLabels map[string]string) *unstructured.Unstructured {
	labels := make(map[string]any, len(matchLabels))
	for k, v := range matchLabels {
		labels[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "policy.linkerd.io/v1beta3",
		"kind":       "Server",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": labels},
			"port":        "http",
		},
	}}
}

func linkerdHTTPRoute(ns, name string, parents []string, matches []map[string]any) *unstructured.Unstructured {
	parentsAny := make([]any, len(parents))
	for i, p := range parents {
		parentsAny[i] = map[string]any{"name": p, "kind": "Server", "group": "policy.linkerd.io"}
	}
	matchesAny := make([]any, len(matches))
	for i, m := range matches {
		matchesAny[i] = m
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "policy.linkerd.io/v1beta1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"parentRefs": parentsAny,
			"rules":      []any{map[string]any{"matches": matchesAny}},
		},
	}}
}

func upstreamHTTPRoute(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{},
	}}
}

// --- normalizer tests -------------------------------------------------------

func TestLinkerdNormalize_ServiceProfileThreeRoutes(t *testing.T) {
	sp := serviceProfile("prod", "webapp.prod.svc.cluster.local", []map[string]any{
		{"name": "GET /", "condition": map[string]any{"method": "GET", "pathRegex": "/"}},
		{"name": "POST /login", "condition": map[string]any{"method": "POST", "pathRegex": "/login"}},
		{"name": "default", "condition": map[string]any{"pathRegex": ".*"}},
	})

	tr := normalizeServiceProfile(sp)

	if tr.Mesh != MeshLinkerd || tr.Kind != "ServiceProfile" {
		t.Errorf("Mesh/Kind = %q/%q, want linkerd/ServiceProfile", tr.Mesh, tr.Kind)
	}
	if tr.ID != "linkerd:prod:sp:webapp.prod.svc.cluster.local" {
		t.Errorf("ID = %q, unexpected", tr.ID)
	}
	if len(tr.Hosts) != 1 || tr.Hosts[0] != "webapp.prod.svc.cluster.local" {
		t.Errorf("Hosts = %v, want [webapp.prod.svc.cluster.local]", tr.Hosts)
	}
	if len(tr.Matchers) != 3 {
		t.Fatalf("Matchers = %d, want 3", len(tr.Matchers))
	}
	wantMatchers := []RouteMatcher{
		{Name: "GET /", Method: "GET", PathRegex: "/"},
		{Name: "POST /login", Method: "POST", PathRegex: "/login"},
		{Name: "default", PathRegex: ".*"},
	}
	for i, want := range wantMatchers {
		if tr.Matchers[i] != want {
			t.Errorf("Matchers[%d] = %+v, want %+v", i, tr.Matchers[i], want)
		}
	}
}

// TestLinkerdNormalize_ServiceProfileEmptyRoutes covers the plan's edge case:
// a ServiceProfile with no routes must emit Matchers as an empty slice, not
// nil, so JSON clients see `matchers: []` and can render an empty state
// consistently.
func TestLinkerdNormalize_ServiceProfileEmptyRoutes(t *testing.T) {
	sp := serviceProfile("prod", "empty.prod", nil)

	tr := normalizeServiceProfile(sp)

	if tr.Matchers == nil {
		t.Fatal("Matchers is nil; want empty slice for parity with populated case")
	}
	if len(tr.Matchers) != 0 {
		t.Errorf("Matchers = %v, want empty slice", tr.Matchers)
	}
}

func TestLinkerdNormalize_ServerLabelSelector(t *testing.T) {
	srv := linkerdServer("prod", "webapp-server", map[string]string{
		"app":     "webapp",
		"version": "v1",
	})

	tr := normalizeLinkerdServer(srv)

	if tr.Kind != "Server" || tr.Mesh != MeshLinkerd {
		t.Errorf("Kind/Mesh = %q/%q, want Server/linkerd", tr.Kind, tr.Mesh)
	}
	if tr.ID != "linkerd:prod:srv:webapp-server" {
		t.Errorf("ID = %q, unexpected", tr.ID)
	}
	// stringifyMatchLabels sorts keys alphabetically → deterministic string.
	if tr.Selector != "app=webapp,version=v1" {
		t.Errorf("Selector = %q, want app=webapp,version=v1", tr.Selector)
	}
}

func TestLinkerdNormalize_HTTPRouteMatchers(t *testing.T) {
	hr := linkerdHTTPRoute("prod", "webapp-routes", []string{"webapp-server"},
		[]map[string]any{
			{"method": "GET", "path": map[string]any{"type": "PathPrefix", "value": "/api"}},
			{"method": "POST", "path": map[string]any{"type": "Exact", "value": "/login"}},
		})

	tr := normalizeLinkerdHTTPRoute(hr)

	if len(tr.Gateways) != 1 || tr.Gateways[0] != "webapp-server" {
		t.Errorf("Gateways = %v, want [webapp-server]", tr.Gateways)
	}
	if len(tr.Matchers) != 2 {
		t.Fatalf("Matchers = %d, want 2", len(tr.Matchers))
	}
	if tr.Matchers[0].PathPrefix != "/api" || tr.Matchers[0].Method != "GET" {
		t.Errorf("Matchers[0] = %+v, want {GET PathPrefix=/api}", tr.Matchers[0])
	}
	if tr.Matchers[1].PathExact != "/login" || tr.Matchers[1].Method != "POST" {
		t.Errorf("Matchers[1] = %+v, want {POST PathExact=/login}", tr.Matchers[1])
	}
}

func TestLinkerdNormalize_AuthzPolicyTargetRef(t *testing.T) {
	ap := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "policy.linkerd.io/v1alpha1",
		"kind":       "AuthorizationPolicy",
		"metadata":   map[string]any{"name": "allow-prom", "namespace": "prod"},
		"spec": map[string]any{
			"targetRef": map[string]any{"kind": "Server", "name": "webapp-server", "group": "policy.linkerd.io"},
			"requiredAuthenticationRefs": []any{
				map[string]any{"kind": "MeshTLSAuthentication", "name": "prom-mtls"},
			},
		},
	}}

	p := normalizeLinkerdAuthzPolicy(ap)

	if p.Action != "ALLOW" {
		t.Errorf("Action = %q, want ALLOW (Linkerd AP is allow-list semantics)", p.Action)
	}
	if p.Selector != "Server/webapp-server" {
		t.Errorf("Selector = %q, want Server/webapp-server", p.Selector)
	}
	if p.RuleCount != 1 {
		t.Errorf("RuleCount = %d, want 1 (one required authn ref)", p.RuleCount)
	}
}

func TestLinkerdNormalize_MeshTLSAuthIdentities(t *testing.T) {
	mtls := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "policy.linkerd.io/v1alpha1",
		"kind":       "MeshTLSAuthentication",
		"metadata":   map[string]any{"name": "prom-mtls", "namespace": "prod"},
		"spec": map[string]any{
			"identities": []any{
				"prometheus.linkerd-viz.serviceaccount.identity.linkerd.cluster.local",
				"grafana.linkerd-viz.serviceaccount.identity.linkerd.cluster.local",
			},
		},
	}}

	p := normalizeLinkerdMeshTLSAuth(mtls)

	if p.Kind != "MeshTLSAuthentication" || p.Mesh != MeshLinkerd {
		t.Errorf("Kind/Mesh = %q/%q", p.Kind, p.Mesh)
	}
	if p.RuleCount != 2 {
		t.Errorf("RuleCount = %d, want 2 (two identities)", p.RuleCount)
	}
}

// --- adapter integration tests ---------------------------------------------

func TestLinkerdList_HappyPathSplitsRoutesAndPolicies(t *testing.T) {
	sp := serviceProfile("prod", "webapp.prod", []map[string]any{
		{"name": "GET /", "condition": map[string]any{"method": "GET", "pathRegex": "/"}},
	})
	srv := linkerdServer("prod", "webapp-server", map[string]string{"app": "webapp"})

	client := newLinkerdFakeDynClient(sp, srv)
	out := ListLinkerd(context.Background(), client, "")

	if len(out.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", out.Errors)
	}
	if len(out.Routes) != 2 {
		t.Errorf("Routes = %d, want 2 (ServiceProfile + Server)", len(out.Routes))
	}
	if len(out.Policies) != 0 {
		t.Errorf("Policies = %d, want 0", len(out.Policies))
	}
}

// TestLinkerdList_UpstreamHTTPRouteIgnored covers the plan's edge case: the
// Linkerd HTTPRoute CRD lives at policy.linkerd.io, and the upstream
// Gateway-API HTTPRoute at gateway.networking.k8s.io. The adapter must only
// surface Linkerd's flavor even when both exist on the cluster.
func TestLinkerdList_UpstreamHTTPRouteIgnored(t *testing.T) {
	linkerdHR := linkerdHTTPRoute("prod", "linkerd-route", []string{"webapp-server"},
		[]map[string]any{{"method": "GET", "path": map[string]any{"type": "PathPrefix", "value": "/"}}})
	upstreamHR := upstreamHTTPRoute("prod", "upstream-route")

	client := newLinkerdFakeDynClient(linkerdHR, upstreamHR)
	out := ListLinkerd(context.Background(), client, "")

	// Among routes, only one HTTPRoute — and it must be the Linkerd one.
	var httpRoutes []TrafficRoute
	for _, r := range out.Routes {
		if r.Kind == "HTTPRoute" {
			httpRoutes = append(httpRoutes, r)
		}
	}
	if len(httpRoutes) != 1 {
		t.Fatalf("HTTPRoutes in result = %d, want 1 (upstream should not appear)", len(httpRoutes))
	}
	if httpRoutes[0].Name != "linkerd-route" {
		t.Errorf("HTTPRoute[0].Name = %q, want linkerd-route", httpRoutes[0].Name)
	}
}
