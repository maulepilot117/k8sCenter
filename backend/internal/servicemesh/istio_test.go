package servicemesh

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// --- fake dynamic client ----------------------------------------------------

func istioScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	for gvr, kind := range map[schema.GroupVersionResource]string{
		IstioVirtualServiceGVR:      "VirtualService",
		IstioDestinationRuleGVR:     "DestinationRule",
		IstioGatewayGVR:             "Gateway",
		IstioPeerAuthenticationGVR:  "PeerAuthentication",
		IstioAuthorizationPolicyGVR: "AuthorizationPolicy",
	} {
		gvk := schema.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: kind}
		s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		s.AddKnownTypeWithName(
			schema.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: kind + "List"},
			&unstructured.UnstructuredList{},
		)
	}
	return s
}

func newIstioFakeDynClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	gvrToListKind := map[schema.GroupVersionResource]string{
		IstioVirtualServiceGVR:      "VirtualServiceList",
		IstioDestinationRuleGVR:     "DestinationRuleList",
		IstioGatewayGVR:             "GatewayList",
		IstioPeerAuthenticationGVR:  "PeerAuthenticationList",
		IstioAuthorizationPolicyGVR: "AuthorizationPolicyList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(istioScheme(), gvrToListKind, objects...)
}

// --- fixtures ---------------------------------------------------------------

func virtualService(ns, name string, hosts []string, dests []map[string]any) *unstructured.Unstructured {
	// unstructured.Unstructured requires []any for nested lists — not
	// []map[string]any — because DeepCopyJSONValue is strict about JSON types.
	destsAny := make([]any, len(dests))
	for i, d := range dests {
		destsAny[i] = d
	}
	hostsAny := make([]any, len(hosts))
	for i, h := range hosts {
		hostsAny[i] = h
	}
	spec := map[string]any{"hosts": hostsAny}
	if len(destsAny) > 0 {
		spec["http"] = []any{map[string]any{"route": destsAny}}
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "VirtualService",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec":       spec,
		},
	}
}

func destinationRule(ns, name, host string, subsets []string) *unstructured.Unstructured {
	subsetsAny := make([]any, len(subsets))
	for i, s := range subsets {
		subsetsAny[i] = map[string]any{"name": s, "labels": map[string]any{"version": s}}
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.istio.io/v1",
			"kind":       "DestinationRule",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec": map[string]any{
				"host":    host,
				"subsets": subsetsAny,
			},
		},
	}
}

func authzPolicy(ns, name, action string, rules []any) *unstructured.Unstructured {
	spec := map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "api"}}}
	if action != "" {
		spec["action"] = action
	}
	if rules != nil {
		spec["rules"] = rules
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "security.istio.io/v1",
			"kind":       "AuthorizationPolicy",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec":       spec,
		},
	}
}

// --- normalizer unit tests --------------------------------------------------

func TestIstioNormalize_VirtualServiceTwoDestinations(t *testing.T) {
	vs := virtualService("shop", "cart-vs", []string{"cart.shop.svc.cluster.local", "cart.example.com"},
		[]map[string]any{
			{"destination": map[string]any{"host": "cart-v1", "port": map[string]any{"number": int64(8080)}}, "weight": int64(80)},
			{"destination": map[string]any{"host": "cart-v2", "subset": "canary"}, "weight": int64(20)},
		})

	tr := normalizeIstioVirtualService(vs)

	if tr.Mesh != MeshIstio || tr.Kind != "VirtualService" {
		t.Errorf("Mesh/Kind = %q/%q, want istio/VirtualService", tr.Mesh, tr.Kind)
	}
	if tr.ID != "istio:shop:vs:cart-vs" {
		t.Errorf("ID = %q, want istio:shop:vs:cart-vs", tr.ID)
	}
	if len(tr.Hosts) != 2 || tr.Hosts[0] != "cart.shop.svc.cluster.local" || tr.Hosts[1] != "cart.example.com" {
		t.Errorf("Hosts = %v, want both hostnames preserved in order", tr.Hosts)
	}
	if len(tr.Destinations) != 2 {
		t.Fatalf("Destinations = %d, want 2", len(tr.Destinations))
	}
	if tr.Destinations[0].Host != "cart-v1" || tr.Destinations[0].Port != 8080 || tr.Destinations[0].Weight != 80 {
		t.Errorf("Destinations[0] = %+v, want {cart-v1, port 8080, weight 80}", tr.Destinations[0])
	}
	if tr.Destinations[1].Host != "cart-v2" || tr.Destinations[1].Subset != "canary" || tr.Destinations[1].Weight != 20 {
		t.Errorf("Destinations[1] = %+v, want {cart-v2, subset canary, weight 20}", tr.Destinations[1])
	}
}

func TestIstioNormalize_DestinationRuleSubsets(t *testing.T) {
	dr := destinationRule("shop", "cart-dr", "cart", []string{"v1", "v2", "canary"})

	tr := normalizeIstioDestinationRule(dr)

	if tr.ID != "istio:shop:dr:cart-dr" {
		t.Errorf("ID = %q, want istio:shop:dr:cart-dr", tr.ID)
	}
	if len(tr.Hosts) != 1 || tr.Hosts[0] != "cart" {
		t.Errorf("Hosts = %v, want [cart]", tr.Hosts)
	}
	if len(tr.Subsets) != 3 || tr.Subsets[0] != "v1" || tr.Subsets[2] != "canary" {
		t.Errorf("Subsets = %v, want [v1 v2 canary]", tr.Subsets)
	}
}

func TestIstioNormalize_AuthzPolicyEffects(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		rules      []any
		wantAction string
		wantEffect string
		wantCount  int
	}{
		{"default action + empty rules → deny_all", "", nil, "ALLOW", "deny_all", 0},
		{"ALLOW + empty rules → deny_all", "ALLOW", []any{}, "ALLOW", "deny_all", 0},
		{"DENY + empty rules → allow_all", "DENY", []any{}, "DENY", "allow_all", 0},
		{"ALLOW + one rule → no computed effect", "ALLOW", []any{map[string]any{"from": []any{}}}, "ALLOW", "", 1},
		{"AUDIT + empty rules → no computed effect", "AUDIT", []any{}, "AUDIT", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := normalizeIstioAuthzPolicy(authzPolicy("prod", "api-authz", tt.action, tt.rules))
			if p.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", p.Action, tt.wantAction)
			}
			if p.Effect != tt.wantEffect {
				t.Errorf("Effect = %q, want %q", p.Effect, tt.wantEffect)
			}
			if p.RuleCount != tt.wantCount {
				t.Errorf("RuleCount = %d, want %d", p.RuleCount, tt.wantCount)
			}
			if p.Selector != "app=api" {
				t.Errorf("Selector = %q, want app=api", p.Selector)
			}
			if p.ID != "istio:prod:ap:api-authz" {
				t.Errorf("ID = %q, want istio:prod:ap:api-authz", p.ID)
			}
		})
	}
}

func TestIstioNormalize_PeerAuthModeUnset(t *testing.T) {
	// PeerAuthentication with no mtls.mode falls through to UNSET so the UI
	// can distinguish "explicitly permissive" from "mesh default applies".
	pa := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "security.istio.io/v1",
		"kind":       "PeerAuthentication",
		"metadata":   map[string]any{"name": "default", "namespace": "istio-system"},
		"spec":       map[string]any{},
	}}
	p := normalizeIstioPeerAuth(pa)
	if p.MTLSMode != "UNSET" {
		t.Errorf("MTLSMode = %q, want UNSET", p.MTLSMode)
	}
}

func TestIstioNormalize_GatewayHostsFlattenDedupe(t *testing.T) {
	gw := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.istio.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": "edge", "namespace": "istio-system"},
		"spec": map[string]any{
			"servers": []any{
				map[string]any{"hosts": []any{"api.example.com", "www.example.com"}},
				map[string]any{"hosts": []any{"api.example.com", "admin.example.com"}}, // dup on purpose
			},
		},
	}}

	tr := normalizeIstioGateway(gw)
	if len(tr.Hosts) != 3 {
		t.Fatalf("Hosts = %v, want 3 deduped entries", tr.Hosts)
	}
	// Hosts are sorted for stability — assert exact order.
	want := []string{"admin.example.com", "api.example.com", "www.example.com"}
	for i, h := range want {
		if tr.Hosts[i] != h {
			t.Errorf("Hosts[%d] = %q, want %q", i, tr.Hosts[i], h)
		}
	}
}

// --- adapter integration tests ---------------------------------------------

func TestIstioList_HappyPathCollectsRoutesAndPolicies(t *testing.T) {
	vs := virtualService("shop", "cart-vs", []string{"cart"},
		[]map[string]any{{"destination": map[string]any{"host": "cart"}, "weight": int64(100)}})
	dr := destinationRule("shop", "cart-dr", "cart", []string{"v1"})
	ap := authzPolicy("shop", "deny-public", "ALLOW", nil)

	client := newIstioFakeDynClient(vs, dr, ap)
	out := ListIstio(context.Background(), client, "")

	if len(out.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", out.Errors)
	}
	if len(out.Routes) != 2 {
		t.Errorf("Routes = %d, want 2 (VS + DR)", len(out.Routes))
	}
	if len(out.Policies) != 1 {
		t.Errorf("Policies = %d, want 1 (AP)", len(out.Policies))
	}
	// Plan test scenario: ALLOW + no rules → deny_all effect.
	if out.Policies[0].Effect != "deny_all" {
		t.Errorf("Policies[0].Effect = %q, want deny_all", out.Policies[0].Effect)
	}
}

// TestIstioList_PartialResultOn403 covers the plan's error path: one CRD
// returns 403 Forbidden; the adapter still returns results for the other CRDs
// and records the failing kind in Errors.
func TestIstioList_PartialResultOn403(t *testing.T) {
	vs := virtualService("shop", "cart-vs", []string{"cart"},
		[]map[string]any{{"destination": map[string]any{"host": "cart"}}})

	client := newIstioFakeDynClient(vs)
	// Block listing DestinationRules only.
	client.PrependReactor("list", "destinationrules",
		func(_ clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("Forbidden: user cannot list destinationrules")
		})

	out := ListIstio(context.Background(), client, "")

	if len(out.Routes) != 1 || out.Routes[0].Kind != "VirtualService" {
		t.Errorf("Routes = %+v, want one VirtualService", out.Routes)
	}
	if _, ok := out.Errors["DestinationRule"]; !ok {
		t.Errorf("Errors = %v, want DestinationRule entry", out.Errors)
	}
	// Other kinds still listed (nothing to list, but should not appear in Errors).
	for _, k := range []string{"VirtualService", "Gateway", "PeerAuthentication", "AuthorizationPolicy"} {
		if _, bad := out.Errors[k]; bad {
			t.Errorf("Errors should not include %q, got: %v", k, out.Errors)
		}
	}
}

// TestIstioList_NamespaceScoping covers the plan's integration scenario:
// namespace="" returns cluster-wide, namespace="foo" returns only foo's items.
func TestIstioList_NamespaceScoping(t *testing.T) {
	aFoo := virtualService("foo", "a", []string{"a"}, nil)
	bBar := virtualService("bar", "b", []string{"b"}, nil)

	client := newIstioFakeDynClient(aFoo, bBar)

	// Cluster-wide: should see both.
	all := ListIstio(context.Background(), client, "")
	if len(all.Routes) != 2 {
		t.Errorf("cluster-wide Routes = %d, want 2", len(all.Routes))
	}

	// Scoped: should see only foo's.
	scoped := ListIstio(context.Background(), client, "foo")
	if len(scoped.Routes) != 1 {
		t.Fatalf("scoped Routes = %d, want 1", len(scoped.Routes))
	}
	if scoped.Routes[0].Namespace != "foo" {
		t.Errorf("scoped Route namespace = %q, want foo", scoped.Routes[0].Namespace)
	}
}
