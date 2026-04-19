package servicemesh

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// istioKindCodes maps CRD Kind → short code used in composite IDs. Matches
// the plan's scheme ("vs", "dr", "gw", "pa", "ap"). Unknown kinds fall back
// to lowercased Kind — keeps the function total without silent dropping.
var istioKindCodes = map[string]string{
	"VirtualService":      "vs",
	"DestinationRule":     "dr",
	"Gateway":             "gw",
	"PeerAuthentication":  "pa",
	"AuthorizationPolicy": "ap",
}

func istioCompositeID(kind, namespace, name string) string {
	code, ok := istioKindCodes[kind]
	if !ok {
		code = strings.ToLower(kind)
	}
	return fmt.Sprintf("istio:%s:%s:%s", namespace, code, name)
}

// normalizeIstioRoute dispatches to the per-kind normalizer. The dispatch
// shape keeps istio.go's list loop simple and makes future kind additions
// (e.g., ServiceEntry) a one-line change.
func normalizeIstioRoute(obj *unstructured.Unstructured, kind string) TrafficRoute {
	switch kind {
	case "VirtualService":
		return normalizeIstioVirtualService(obj)
	case "DestinationRule":
		return normalizeIstioDestinationRule(obj)
	case "Gateway":
		return normalizeIstioGateway(obj)
	}
	return TrafficRoute{
		ID:        istioCompositeID(kind, obj.GetNamespace(), obj.GetName()),
		Mesh:      MeshIstio,
		Kind:      kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Raw:       obj.Object,
	}
}

func normalizeIstioVirtualService(obj *unstructured.Unstructured) TrafficRoute {
	name, ns := obj.GetName(), obj.GetNamespace()
	hosts, _, _ := unstructured.NestedStringSlice(obj.Object, "spec", "hosts")
	gateways, _, _ := unstructured.NestedStringSlice(obj.Object, "spec", "gateways")

	// VirtualService carries destinations under spec.{http,tls,tcp}[*].route[*].destination.
	// All three protocol branches share the same nested shape, so we walk them uniformly.
	var destinations []RouteDestination
	for _, protocol := range []string{"http", "tls", "tcp"} {
		routes, _, _ := unstructured.NestedSlice(obj.Object, "spec", protocol)
		for _, r := range routes {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			inner, _, _ := unstructured.NestedSlice(rm, "route")
			for _, ir := range inner {
				irMap, ok := ir.(map[string]any)
				if !ok {
					continue
				}
				host, _, _ := unstructured.NestedString(irMap, "destination", "host")
				subset, _, _ := unstructured.NestedString(irMap, "destination", "subset")
				port, _, _ := unstructured.NestedInt64(irMap, "destination", "port", "number")
				weight, _, _ := unstructured.NestedInt64(irMap, "weight")
				destinations = append(destinations, RouteDestination{
					Host:   host,
					Subset: subset,
					Port:   port,
					Weight: weight,
				})
			}
		}
	}

	return TrafficRoute{
		ID:           istioCompositeID("VirtualService", ns, name),
		Mesh:         MeshIstio,
		Kind:         "VirtualService",
		Name:         name,
		Namespace:    ns,
		Hosts:        hosts,
		Gateways:     gateways,
		Destinations: destinations,
		Raw:          obj.Object,
	}
}

func normalizeIstioDestinationRule(obj *unstructured.Unstructured) TrafficRoute {
	name, ns := obj.GetName(), obj.GetNamespace()
	host, _, _ := unstructured.NestedString(obj.Object, "spec", "host")

	subsetsRaw, _, _ := unstructured.NestedSlice(obj.Object, "spec", "subsets")
	var subsets []string
	for _, s := range subsetsRaw {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if n, _, _ := unstructured.NestedString(sm, "name"); n != "" {
			subsets = append(subsets, n)
		}
	}

	tr := TrafficRoute{
		ID:        istioCompositeID("DestinationRule", ns, name),
		Mesh:      MeshIstio,
		Kind:      "DestinationRule",
		Name:      name,
		Namespace: ns,
		Subsets:   subsets,
		Raw:       obj.Object,
	}
	if host != "" {
		tr.Hosts = []string{host}
	}
	return tr
}

func normalizeIstioGateway(obj *unstructured.Unstructured) TrafficRoute {
	name, ns := obj.GetName(), obj.GetNamespace()

	// A Gateway's spec.servers[*].hosts is a per-listener list; flatten + dedupe
	// so the UI can render a single host column.
	hostSet := map[string]struct{}{}
	serversRaw, _, _ := unstructured.NestedSlice(obj.Object, "spec", "servers")
	for _, s := range serversRaw {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		hs, _, _ := unstructured.NestedStringSlice(sm, "hosts")
		for _, h := range hs {
			hostSet[h] = struct{}{}
		}
	}
	hosts := make([]string, 0, len(hostSet))
	for h := range hostSet {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	return TrafficRoute{
		ID:        istioCompositeID("Gateway", ns, name),
		Mesh:      MeshIstio,
		Kind:      "Gateway",
		Name:      name,
		Namespace: ns,
		Hosts:     hosts,
		Raw:       obj.Object,
	}
}

func normalizeIstioPolicy(obj *unstructured.Unstructured, kind string) MeshedPolicy {
	switch kind {
	case "PeerAuthentication":
		return normalizeIstioPeerAuth(obj)
	case "AuthorizationPolicy":
		return normalizeIstioAuthzPolicy(obj)
	}
	return MeshedPolicy{
		ID:        istioCompositeID(kind, obj.GetNamespace(), obj.GetName()),
		Mesh:      MeshIstio,
		Kind:      kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Raw:       obj.Object,
	}
}

func normalizeIstioPeerAuth(obj *unstructured.Unstructured) MeshedPolicy {
	name, ns := obj.GetName(), obj.GetNamespace()
	mode, _, _ := unstructured.NestedString(obj.Object, "spec", "mtls", "mode")
	if mode == "" {
		mode = "UNSET"
	}

	return MeshedPolicy{
		ID:        istioCompositeID("PeerAuthentication", ns, name),
		Mesh:      MeshIstio,
		Kind:      "PeerAuthentication",
		Name:      name,
		Namespace: ns,
		MTLSMode:  mode,
		Selector:  stringifyMatchLabels(obj.Object, "spec", "selector", "matchLabels"),
		Raw:       obj.Object,
	}
}

func normalizeIstioAuthzPolicy(obj *unstructured.Unstructured) MeshedPolicy {
	name, ns := obj.GetName(), obj.GetNamespace()

	action, _, _ := unstructured.NestedString(obj.Object, "spec", "action")
	if action == "" {
		action = "ALLOW" // Istio default
	}
	rulesRaw, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")

	// Istio semantics: ALLOW + no rules = allows nothing (deny_all);
	// DENY + no rules = denies nothing (allow_all). The UI surfaces this
	// computed effect so operators don't have to infer it from the spec.
	effect := ""
	if len(rulesRaw) == 0 {
		switch action {
		case "ALLOW":
			effect = "deny_all"
		case "DENY":
			effect = "allow_all"
		}
	}

	return MeshedPolicy{
		ID:        istioCompositeID("AuthorizationPolicy", ns, name),
		Mesh:      MeshIstio,
		Kind:      "AuthorizationPolicy",
		Name:      name,
		Namespace: ns,
		Action:    action,
		Effect:    effect,
		Selector:  stringifyMatchLabels(obj.Object, "spec", "selector", "matchLabels"),
		RuleCount: len(rulesRaw),
		Raw:       obj.Object,
	}
}

// stringifyMatchLabels renders a nested matchLabels map as a stable,
// sorted "k=v,k=v" string for display.
func stringifyMatchLabels(obj map[string]any, path ...string) string {
	labels, found, _ := unstructured.NestedStringMap(obj, path...)
	if !found || len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ",")
}
