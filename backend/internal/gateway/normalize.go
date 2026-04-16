package gateway

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// stringFrom safely extracts a string value from a map by key.
// Returns "" if the map is nil or the key is absent / not a string.
func stringFrom(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// intFrom safely extracts an int from a map by key.
// Handles both int and float64 (JSON unmarshalling produces float64).
// Returns 0 if the map is nil or the key is absent / not numeric.
func intFrom(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// extractConditions reads status conditions from a nested path.
// Used for both top-level status.conditions and per-listener/per-parent conditions.
func extractConditions(obj map[string]any, path ...string) []Condition {
	val, found, err := unstructured.NestedSlice(obj, path...)
	if err != nil || !found {
		return nil
	}
	var out []Condition
	for _, item := range val {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		c := Condition{
			Type:    stringFrom(cm, "type"),
			Status:  stringFrom(cm, "status"),
			Reason:  stringFrom(cm, "reason"),
			Message: stringFrom(cm, "message"),
		}
		if s, _, _ := unstructured.NestedString(cm, "lastTransitionTime"); s != "" {
			c.LastTransitionTime = s
		}
		out = append(out, c)
	}
	return out
}

// extractParentRefs reads spec.parentRefs from an unstructured object.
// Handles defaults: Group defaults to "gateway.networking.k8s.io", Kind defaults to "Gateway".
func extractParentRefs(u *unstructured.Unstructured) []ParentRef {
	refs, found, err := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
	if err != nil || !found {
		return nil
	}
	var out []ParentRef
	for _, item := range refs {
		rm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		group := stringFrom(rm, "group")
		if group == "" {
			group = APIGroup
		}
		kind := stringFrom(rm, "kind")
		if kind == "" {
			kind = "Gateway"
		}
		out = append(out, ParentRef{
			Group:       group,
			Kind:        kind,
			Name:        stringFrom(rm, "name"),
			Namespace:   stringFrom(rm, "namespace"),
			SectionName: stringFrom(rm, "sectionName"),
		})
	}
	return out
}

// extractBackendRefs reads backendRefs from a slice of unstructured items.
// Handles defaults: Group defaults to "" (core), Kind defaults to "Service".
func extractBackendRefs(items []any) []BackendRef {
	var out []BackendRef
	for _, item := range items {
		rm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind := stringFrom(rm, "kind")
		if kind == "" {
			kind = "Service"
		}
		ref := BackendRef{
			Group:     stringFrom(rm, "group"),
			Kind:      kind,
			Name:      stringFrom(rm, "name"),
			Namespace: stringFrom(rm, "namespace"),
		}
		if p := intFrom(rm, "port"); p != 0 {
			port := p
			ref.Port = &port
		}
		if _, ok := rm["weight"]; ok {
			wt := intFrom(rm, "weight")
			ref.Weight = &wt
		}
		out = append(out, ref)
	}
	return out
}

// aggregateRouteConditions collects conditions from status.parents[] and deduplicates
// by type, keeping the first occurrence with a non-empty status.
func aggregateRouteConditions(obj map[string]any) []Condition {
	parents, found, err := unstructured.NestedSlice(obj, "status", "parents")
	if err != nil || !found {
		return nil
	}
	seen := map[string]bool{}
	var out []Condition
	for _, p := range parents {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		conds := extractConditions(pm, "conditions")
		for _, c := range conds {
			if seen[c.Type] {
				continue
			}
			if c.Status != "" {
				seen[c.Type] = true
			}
			out = append(out, c)
		}
	}
	return out
}

// normalizeGatewayClass converts an unstructured GatewayClass into a GatewayClassSummary.
func normalizeGatewayClass(u *unstructured.Unstructured) GatewayClassSummary {
	obj := u.Object

	controllerName, _, _ := unstructured.NestedString(obj, "spec", "controllerName")
	description, _, _ := unstructured.NestedString(obj, "spec", "description")

	return GatewayClassSummary{
		Name:           u.GetName(),
		ControllerName: controllerName,
		Description:    description,
		Conditions:     extractConditions(obj, "status", "conditions"),
		Age:            u.GetCreationTimestamp().Time,
	}
}

// normalizeGateway converts an unstructured Gateway into a GatewaySummary.
// parseListeners extracts listeners from spec.listeners[], optionally including detail fields (TLS, allowedRoutes).
// It also merges per-listener attachedRoutes counts (and conditions if detail=true) from status.listeners[].
// Returns the listeners slice and total attached route count.
func parseListeners(obj map[string]any, detail bool) ([]Listener, int) {
	specListeners, _, _ := unstructured.NestedSlice(obj, "spec", "listeners")
	listeners := make([]Listener, 0, len(specListeners))
	for _, item := range specListeners {
		lm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		l := Listener{
			Name:     stringFrom(lm, "name"),
			Port:     intFrom(lm, "port"),
			Protocol: stringFrom(lm, "protocol"),
			Hostname: stringFrom(lm, "hostname"),
		}
		if detail {
			if tls, ok := lm["tls"].(map[string]any); ok {
				l.TLSMode = stringFrom(tls, "mode")
				if certRefs, ok := tls["certificateRefs"].([]any); ok && len(certRefs) > 0 {
					if cr, ok := certRefs[0].(map[string]any); ok {
						ns := stringFrom(cr, "namespace")
						name := stringFrom(cr, "name")
						if ns != "" {
							l.CertificateRef = ns + "/" + name
						} else {
							l.CertificateRef = name
						}
					}
				}
			}
			if ar, ok := lm["allowedRoutes"].(map[string]any); ok {
				l.AllowedRoutes = stringifyAllowedRoutes(ar)
			}
		}
		listeners = append(listeners, l)
	}

	statusListeners, _, _ := unstructured.NestedSlice(obj, "status", "listeners")
	attachedTotal := 0
	for _, item := range statusListeners {
		slm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringFrom(slm, "name")
		count := intFrom(slm, "attachedRoutes")
		attachedTotal += count
		for i := range listeners {
			if listeners[i].Name == name {
				listeners[i].AttachedRouteCount = count
				if detail {
					listeners[i].Conditions = extractConditions(slm, "conditions")
				}
				break
			}
		}
	}

	return listeners, attachedTotal
}

// parseAddresses extracts address values from status.addresses[].
func parseAddresses(obj map[string]any) []string {
	addrSlice, _, _ := unstructured.NestedSlice(obj, "status", "addresses")
	var addresses []string
	for _, item := range addrSlice {
		am, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if v := stringFrom(am, "value"); v != "" {
			addresses = append(addresses, v)
		}
	}
	return addresses
}

func normalizeGateway(u *unstructured.Unstructured) GatewaySummary {
	obj := u.Object
	className, _, _ := unstructured.NestedString(obj, "spec", "gatewayClassName")
	listeners, attachedTotal := parseListeners(obj, false)

	return GatewaySummary{
		Name:               u.GetName(),
		Namespace:          u.GetNamespace(),
		GatewayClassName:   className,
		Listeners:          listeners,
		Addresses:          parseAddresses(obj),
		AttachedRouteCount: attachedTotal,
		Conditions:         extractConditions(obj, "status", "conditions"),
		Age:                u.GetCreationTimestamp().Time,
	}
}

// normalizeGatewayDetail converts an unstructured Gateway into a GatewayDetail
// with richer listener data. AttachedRoutes starts empty (filled by handler from cache).
func normalizeGatewayDetail(u *unstructured.Unstructured) GatewayDetail {
	obj := u.Object
	className, _, _ := unstructured.NestedString(obj, "spec", "gatewayClassName")
	listeners, attachedTotal := parseListeners(obj, true)

	return GatewayDetail{
		GatewaySummary: GatewaySummary{
			Name:               u.GetName(),
			Namespace:          u.GetNamespace(),
			GatewayClassName:   className,
			Listeners:          listeners,
			Addresses:          parseAddresses(obj),
			AttachedRouteCount: attachedTotal,
			Conditions:         extractConditions(obj, "status", "conditions"),
			Age:                u.GetCreationTimestamp().Time,
		},
		// AttachedRoutes left empty — filled by handler from cache.
	}
}

// stringifyAllowedRoutes converts an allowedRoutes map into a human-readable string.
func stringifyAllowedRoutes(ar map[string]any) string {
	var parts []string

	if kinds, ok := ar["kinds"].([]any); ok {
		for _, k := range kinds {
			km, ok := k.(map[string]any)
			if !ok {
				continue
			}
			kind := stringFrom(km, "kind")
			group := stringFrom(km, "group")
			if group != "" && group != APIGroup {
				kind = group + "/" + kind
			}
			if kind != "" {
				parts = append(parts, kind)
			}
		}
	}

	if ns, ok := ar["namespaces"].(map[string]any); ok {
		from := stringFrom(ns, "from")
		if from != "" {
			parts = append(parts, "from:"+from)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// normalizeHTTPRoute converts an unstructured HTTPRoute into an HTTPRouteSummary.
func normalizeHTTPRoute(u *unstructured.Unstructured) HTTPRouteSummary {
	obj := u.Object

	hostnames, _, _ := unstructured.NestedStringSlice(obj, "spec", "hostnames")

	// Count total backend refs across all rules.
	backendCount := 0
	rules, _, _ := unstructured.NestedSlice(obj, "spec", "rules")
	for _, r := range rules {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if refs, ok := rm["backendRefs"].([]any); ok {
			backendCount += len(refs)
		}
	}

	return HTTPRouteSummary{
		Name:         u.GetName(),
		Namespace:    u.GetNamespace(),
		Hostnames:    hostnames,
		ParentRefs:   extractParentRefs(u),
		BackendCount: backendCount,
		Conditions:   aggregateRouteConditions(obj),
		Age:          u.GetCreationTimestamp().Time,
	}
}

// normalizeHTTPRouteDetail converts an unstructured HTTPRoute into an HTTPRouteDetail.
func normalizeHTTPRouteDetail(u *unstructured.Unstructured) HTTPRouteDetail {
	obj := u.Object

	hostnames, _, _ := unstructured.NestedStringSlice(obj, "spec", "hostnames")

	rawRules, _, _ := unstructured.NestedSlice(obj, "spec", "rules")
	var httpRules []HTTPRouteRule
	backendCount := 0

	for _, r := range rawRules {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}

		rule := HTTPRouteRule{}

		// Matches.
		if matches, ok := rm["matches"].([]any); ok {
			for _, m := range matches {
				mm, ok := m.(map[string]any)
				if !ok {
					continue
				}
				match := HTTPRouteMatch{}

				if path, ok := mm["path"].(map[string]any); ok {
					match.PathType = stringFrom(path, "type")
					match.PathValue = stringFrom(path, "value")
				}

				if headers, ok := mm["headers"].([]any); ok {
					for _, h := range headers {
						hm, ok := h.(map[string]any)
						if !ok {
							continue
						}
						match.Headers = append(match.Headers, stringFrom(hm, "name")+"="+stringFrom(hm, "value"))
					}
				}

				match.Method = stringFrom(mm, "method")

				if qps, ok := mm["queryParams"].([]any); ok {
					for _, qp := range qps {
						qpm, ok := qp.(map[string]any)
						if !ok {
							continue
						}
						match.QueryParams = append(match.QueryParams, stringFrom(qpm, "name")+"="+stringFrom(qpm, "value"))
					}
				}

				rule.Matches = append(rule.Matches, match)
			}
		}

		// Filters.
		if filters, ok := rm["filters"].([]any); ok {
			for _, f := range filters {
				fm, ok := f.(map[string]any)
				if !ok {
					continue
				}
				filter := HTTPRouteFilter{
					Type:    stringFrom(fm, "type"),
					Details: buildFilterDetails(fm),
				}
				rule.Filters = append(rule.Filters, filter)
			}
		}

		// BackendRefs.
		if refs, ok := rm["backendRefs"].([]any); ok {
			rule.BackendRefs = extractBackendRefs(refs)
			backendCount += len(refs)
		}

		httpRules = append(httpRules, rule)
	}

	return HTTPRouteDetail{
		Name:         u.GetName(),
		Namespace:    u.GetNamespace(),
		Hostnames:    hostnames,
		ParentRefs:   extractParentRefs(u),
		BackendCount: backendCount,
		Conditions:   aggregateRouteConditions(obj),
		Age:          u.GetCreationTimestamp().Time,
		Rules:        httpRules,
	}
}

// buildFilterDetails builds a human-readable details string from an HTTPRoute filter config.
func buildFilterDetails(fm map[string]any) string {
	filterType := stringFrom(fm, "type")
	switch filterType {
	case "RequestHeaderModifier":
		return describeHeaderModifier(fm, "requestHeaderModifier")
	case "ResponseHeaderModifier":
		return describeHeaderModifier(fm, "responseHeaderModifier")
	case "RequestRedirect":
		if rr, ok := fm["requestRedirect"].(map[string]any); ok {
			var parts []string
			if scheme := stringFrom(rr, "scheme"); scheme != "" {
				parts = append(parts, "scheme:"+scheme)
			}
			if host := stringFrom(rr, "hostname"); host != "" {
				parts = append(parts, "host:"+host)
			}
			if code := intFrom(rr, "statusCode"); code != 0 {
				parts = append(parts, fmt.Sprintf("code:%d", code))
			}
			return strings.Join(parts, " ")
		}
	case "URLRewrite":
		if uw, ok := fm["urlRewrite"].(map[string]any); ok {
			var parts []string
			if host := stringFrom(uw, "hostname"); host != "" {
				parts = append(parts, "host:"+host)
			}
			if pp, ok := uw["path"].(map[string]any); ok {
				parts = append(parts, stringFrom(pp, "type")+":"+stringFrom(pp, "value"))
			}
			return strings.Join(parts, " ")
		}
	case "RequestMirror":
		if rm, ok := fm["requestMirror"].(map[string]any); ok {
			if br, ok := rm["backendRef"].(map[string]any); ok {
				return stringFrom(br, "name")
			}
		}
	case "ExtensionRef":
		if er, ok := fm["extensionRef"].(map[string]any); ok {
			return stringFrom(er, "group") + "/" + stringFrom(er, "kind") + "/" + stringFrom(er, "name")
		}
	}
	return ""
}

// describeHeaderModifier summarizes a header modifier filter.
func describeHeaderModifier(fm map[string]any, key string) string {
	hm, ok := fm[key].(map[string]any)
	if !ok {
		return ""
	}
	var parts []string
	if sets, ok := hm["set"].([]any); ok {
		for _, s := range sets {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			parts = append(parts, "set:"+stringFrom(sm, "name"))
		}
	}
	if adds, ok := hm["add"].([]any); ok {
		for _, a := range adds {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			parts = append(parts, "add:"+stringFrom(am, "name"))
		}
	}
	if removes, ok := hm["remove"].([]any); ok {
		for _, r := range removes {
			if s, ok := r.(string); ok {
				parts = append(parts, "remove:"+s)
			}
		}
	}
	return strings.Join(parts, " ")
}

// normalizeGRPCRouteDetail converts an unstructured GRPCRoute into a GRPCRouteDetail.
func normalizeGRPCRouteDetail(u *unstructured.Unstructured) GRPCRouteDetail {
	obj := u.Object

	rawRules, _, _ := unstructured.NestedSlice(obj, "spec", "rules")
	var grpcRules []GRPCRouteRule

	for _, r := range rawRules {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}

		rule := GRPCRouteRule{}

		// Matches.
		if matches, ok := rm["matches"].([]any); ok {
			for _, m := range matches {
				mm, ok := m.(map[string]any)
				if !ok {
					continue
				}
				match := GRPCRouteMatch{}

				if method, ok := mm["method"].(map[string]any); ok {
					match.Service = stringFrom(method, "service")
					match.Method = stringFrom(method, "method")
				}

				if headers, ok := mm["headers"].([]any); ok {
					for _, h := range headers {
						hm, ok := h.(map[string]any)
						if !ok {
							continue
						}
						match.Headers = append(match.Headers, stringFrom(hm, "name")+"="+stringFrom(hm, "value"))
					}
				}

				rule.Matches = append(rule.Matches, match)
			}
		}

		// BackendRefs.
		if refs, ok := rm["backendRefs"].([]any); ok {
			rule.BackendRefs = extractBackendRefs(refs)
		}

		grpcRules = append(grpcRules, rule)
	}

	return GRPCRouteDetail{
		Name:       u.GetName(),
		Namespace:  u.GetNamespace(),
		ParentRefs: extractParentRefs(u),
		Rules:      grpcRules,
		Conditions: aggregateRouteConditions(obj),
		Age:        u.GetCreationTimestamp().Time,
	}
}

// normalizeRoute converts an unstructured route resource into a generic RouteSummary.
// Used for list views of any route type.
func normalizeRoute(u *unstructured.Unstructured, kind string) RouteSummary {
	obj := u.Object

	hostnames, _, _ := unstructured.NestedStringSlice(obj, "spec", "hostnames")

	return RouteSummary{
		Kind:       kind,
		Name:       u.GetName(),
		Namespace:  u.GetNamespace(),
		Hostnames:  hostnames,
		ParentRefs: extractParentRefs(u),
		Conditions: aggregateRouteConditions(obj),
		Age:        u.GetCreationTimestamp().Time,
	}
}

// normalizeSimpleRouteDetail converts an unstructured TCP, TLS, or UDP route into
// a SimpleRouteDetail. Hostnames are only populated for TLSRoute.
func normalizeSimpleRouteDetail(u *unstructured.Unstructured, kind string) SimpleRouteDetail {
	obj := u.Object

	hostnames, _, _ := unstructured.NestedStringSlice(obj, "spec", "hostnames")

	// TCP/UDP/TLS routes have rules with backendRefs — aggregate from all rules.
	var backendRefs []BackendRef
	rules, _, _ := unstructured.NestedSlice(obj, "spec", "rules")
	for _, rule := range rules {
		rm, ok := rule.(map[string]any)
		if !ok {
			continue
		}
		if refs, ok := rm["backendRefs"].([]any); ok {
			backendRefs = append(backendRefs, extractBackendRefs(refs)...)
		}
	}

	return SimpleRouteDetail{
		Kind:        kind,
		Name:        u.GetName(),
		Namespace:   u.GetNamespace(),
		Hostnames:   hostnames,
		ParentRefs:  extractParentRefs(u),
		BackendRefs: backendRefs,
		Conditions:  aggregateRouteConditions(obj),
		Age:         u.GetCreationTimestamp().Time,
	}
}
