package resources

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const kindCiliumNetworkPolicy = "ciliumnetworkpolicies"

// CiliumPolicyRequest is the structured payload for creating a CiliumNetworkPolicy.
type CiliumPolicyRequest struct {
	Name             string            `json:"name"`
	Namespace        string            `json:"namespace"`
	EndpointSelector map[string]string `json:"endpointSelector"`
	IngressRules     []CiliumPolicyRule `json:"ingressRules"`
	EgressRules      []CiliumPolicyRule `json:"egressRules"`
}

// CiliumPolicyRule defines a single ingress or egress rule.
type CiliumPolicyRule struct {
	PeerType string            `json:"peerType"` // "endpoints", "entities", "cidr"
	Labels   map[string]string `json:"labels,omitempty"`
	Entities []string          `json:"entities,omitempty"`
	CIDRs    []string          `json:"cidrs,omitempty"`
	Ports    []CiliumPortRule  `json:"ports,omitempty"`
	Action   string            `json:"action"` // "allow", "deny"
}

// CiliumPortRule defines a port and protocol for a Cilium policy rule.
type CiliumPortRule struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // "TCP", "UDP", "SCTP", "ANY"
}

// Valid Cilium entity names.
var validEntities = map[string]bool{
	"world": true, "cluster": true, "host": true, "remote-node": true,
	"kube-apiserver": true, "health": true, "init": true, "ingress": true, "all": true,
}

// Protected namespaces that trigger warnings.
var protectedNamespaces = map[string]bool{
	"kube-system": true, "cilium": true, "k8scenter": true,
}

var k8sLabelKeyRegexp = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._\-/]{0,61}[a-zA-Z0-9])?$`)
var k8sLabelValRegexp = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9._\-]{0,61}[a-zA-Z0-9])?)?$`)

// PolicyWarning is returned in the response when a dangerous policy is detected.
type PolicyWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HandleListCiliumPolicies lists CiliumNetworkPolicies from the informer cache.
func (h *Handler) HandleListCiliumPolicies(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	ns := params.Namespace

	if !h.checkAccess(w, r, user, "list", kindCiliumNetworkPolicy, ns) {
		return
	}

	lister := h.Informers.CiliumNetworkPolicies()
	if lister == nil {
		writeError(w, http.StatusNotFound, "CiliumNetworkPolicy CRD is not installed on this cluster", "")
		return
	}

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var objs []runtime.Object
	var err error
	if ns != "" {
		objs, err = lister.ByNamespace(ns).List(sel)
	} else {
		objs, err = lister.List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "CiliumNetworkPolicy", ns, "")
		return
	}

	// Convert runtime.Object slice to *unstructured.Unstructured for pagination
	items := make([]*unstructured.Unstructured, 0, len(objs))
	for _, obj := range objs {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			items = append(items, u)
		} else {
			h.Logger.Warn("unexpected object type in CiliumNetworkPolicy lister", "type", fmt.Sprintf("%T", obj))
		}
	}

	page, cont := paginate(items, params.Limit, params.Continue)
	writeList(w, page, len(items), cont)
}

// HandleGetCiliumPolicy gets a single CiliumNetworkPolicy from the informer cache.
func (h *Handler) HandleGetCiliumPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindCiliumNetworkPolicy, ns) {
		return
	}

	lister := h.Informers.CiliumNetworkPolicies()
	if lister == nil {
		writeError(w, http.StatusNotFound, "CiliumNetworkPolicy CRD is not installed on this cluster", "")
		return
	}

	obj, err := lister.ByNamespace(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "CiliumNetworkPolicy", ns, name)
		return
	}
	writeData(w, obj)
}

// HandleDeleteCiliumPolicy deletes a CiliumNetworkPolicy.
func (h *Handler) HandleDeleteCiliumPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindCiliumNetworkPolicy, ns) {
		return
	}

	dc, err := h.impersonatingDynamic(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := dc.Resource(k8s.CiliumPolicyGVR).Namespace(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "CiliumNetworkPolicy", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "CiliumNetworkPolicy", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "CiliumNetworkPolicy", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// HandleUpdateCiliumPolicy updates an existing CiliumNetworkPolicy from a structured request.
func (h *Handler) HandleUpdateCiliumPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindCiliumNetworkPolicy, ns) {
		return
	}

	var req CiliumPolicyRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	req.Namespace = ns
	req.Name = name

	if errs := validateCiliumPolicy(&req); len(errs) > 0 {
		writeError(w, http.StatusBadRequest, "validation failed: "+strings.Join(errs, "; "), "")
		return
	}

	warnings := detectDangerousPolicy(&req)
	obj := buildCiliumPolicy(&req)

	dc, err := h.impersonatingDynamic(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	updated, err := dc.Resource(k8s.CiliumPolicyGVR).Namespace(ns).Update(r.Context(), obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "CiliumNetworkPolicy", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "CiliumNetworkPolicy", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, "CiliumNetworkPolicy", ns, name, audit.ResultSuccess)

	result := map[string]any{"resource": updated.Object}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	writeData(w, result)
}

// HandleCreateCiliumPolicy creates a CiliumNetworkPolicy from a structured request.
func (h *Handler) HandleCreateCiliumPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindCiliumNetworkPolicy, ns) {
		return
	}

	var req CiliumPolicyRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	req.Namespace = ns

	// Validate
	if errs := validateCiliumPolicy(&req); len(errs) > 0 {
		writeError(w, http.StatusBadRequest, "validation failed: "+strings.Join(errs, "; "), "")
		return
	}

	// Detect dangerous patterns
	warnings := detectDangerousPolicy(&req)

	// Build unstructured CiliumNetworkPolicy
	obj := buildCiliumPolicy(&req)

	dc, err := h.impersonatingDynamic(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	created, err := dc.Resource(k8s.CiliumPolicyGVR).Namespace(ns).Create(r.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "CiliumNetworkPolicy", ns, req.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "CiliumNetworkPolicy", ns, req.Name)
		return
	}

	h.auditWrite(r, user, audit.ActionCreate, "CiliumNetworkPolicy", ns, created.GetName(), audit.ResultSuccess)

	// Return created resource with warnings inside standard envelope data field
	result := map[string]any{"resource": created.Object}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	writeCreated(w, result)
}

// validateCiliumPolicy validates all fields of a CiliumPolicyRequest.
func validateCiliumPolicy(req *CiliumPolicyRequest) []string {
	var errs []string

	// Name validation
	if req.Name == "" {
		errs = append(errs, "name is required")
	} else if !k8sNameRegexp.MatchString(req.Name) {
		errs = append(errs, "invalid policy name: must be a valid Kubernetes name")
	}

	// Label validation
	if err := validateLabels(req.EndpointSelector); err != "" {
		errs = append(errs, "endpointSelector: "+err)
	}

	// Rule count bounds
	if len(req.IngressRules)+len(req.EgressRules) > 100 {
		errs = append(errs, "too many rules (max 100 total)")
		return errs
	}

	// Rule validation
	for i, rule := range req.IngressRules {
		for _, e := range validateRule(&rule) {
			errs = append(errs, fmt.Sprintf("ingressRules[%d]: %s", i, e))
		}
	}
	for i, rule := range req.EgressRules {
		for _, e := range validateRule(&rule) {
			errs = append(errs, fmt.Sprintf("egressRules[%d]: %s", i, e))
		}
	}

	return errs
}

func validateRule(rule *CiliumPolicyRule) []string {
	var errs []string

	// Action
	if rule.Action != "allow" && rule.Action != "deny" {
		errs = append(errs, "action must be 'allow' or 'deny'")
	}

	// PeerType
	switch rule.PeerType {
	case "endpoints":
		if len(rule.Labels) > 20 {
			errs = append(errs, "too many labels per rule (max 20)")
		} else if err := validateLabels(rule.Labels); err != "" {
			errs = append(errs, "labels: "+err)
		}
	case "entities":
		if len(rule.Entities) > len(validEntities) {
			errs = append(errs, fmt.Sprintf("too many entities (max %d)", len(validEntities)))
		}
		for _, e := range rule.Entities {
			if !validEntities[e] {
				errs = append(errs, fmt.Sprintf("invalid entity %q", e))
			}
		}
	case "cidr":
		if len(rule.CIDRs) > 50 {
			errs = append(errs, "too many CIDRs per rule (max 50)")
		}
		for _, c := range rule.CIDRs {
			if err := validateCIDR(c); err != "" {
				errs = append(errs, err)
			}
		}
	default:
		errs = append(errs, fmt.Sprintf("invalid peerType %q, must be endpoints/entities/cidr", rule.PeerType))
	}

	// Ports
	if len(rule.Ports) > 100 {
		errs = append(errs, "too many ports per rule (max 100)")
	}
	for _, p := range rule.Ports {
		if p.Port < 1 || p.Port > 65535 {
			errs = append(errs, fmt.Sprintf("port %d out of range 1-65535", p.Port))
		}
		proto := strings.ToUpper(p.Protocol)
		if proto != "TCP" && proto != "UDP" && proto != "SCTP" && proto != "ANY" {
			errs = append(errs, fmt.Sprintf("invalid protocol %q", p.Protocol))
		}
	}

	return errs
}

func validateLabels(labels map[string]string) string {
	if len(labels) > 20 {
		return "too many labels (max 20)"
	}
	for k, v := range labels {
		if len(k) > 63 || !k8sLabelKeyRegexp.MatchString(k) {
			return fmt.Sprintf("invalid label key %q", k)
		}
		if len(v) > 63 || !k8sLabelValRegexp.MatchString(v) {
			return fmt.Sprintf("invalid label value %q for key %q", v, k)
		}
	}
	return ""
}

func validateCIDR(cidr string) string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Sprintf("invalid CIDR %q: %v", cidr, err)
	}
	// Reject loopback
	if ipNet.IP.IsLoopback() {
		return fmt.Sprintf("loopback CIDR %q is not allowed", cidr)
	}
	return ""
}

// detectDangerousPolicy returns warnings for potentially dangerous policy patterns.
func detectDangerousPolicy(req *CiliumPolicyRequest) []PolicyWarning {
	var warnings []PolicyWarning

	// Warn on protected namespaces
	if protectedNamespaces[req.Namespace] {
		warnings = append(warnings, PolicyWarning{
			Code:    "protected_namespace",
			Message: fmt.Sprintf("Policy targets protected namespace %q — verify this is intentional", req.Namespace),
		})
	}

	// Warn on deny-all (empty endpoint selector + deny rules)
	hasDeny := false
	for _, r := range req.IngressRules {
		if r.Action == "deny" {
			hasDeny = true
			break
		}
	}
	if !hasDeny {
		for _, r := range req.EgressRules {
			if r.Action == "deny" {
				hasDeny = true
				break
			}
		}
	}
	if hasDeny && len(req.EndpointSelector) == 0 {
		warnings = append(warnings, PolicyWarning{
			Code:    "broad_deny",
			Message: "Deny rule with empty endpoint selector will affect ALL pods in the namespace",
		})
	}

	// Warn on 0.0.0.0/0 CIDR
	for _, rules := range [][]CiliumPolicyRule{req.IngressRules, req.EgressRules} {
		for _, rule := range rules {
			for _, c := range rule.CIDRs {
				if c == "0.0.0.0/0" || c == "::/0" {
					warnings = append(warnings, PolicyWarning{
						Code:    "wide_cidr",
						Message: fmt.Sprintf("CIDR %s matches all addresses", c),
					})
				}
			}
		}
	}

	return warnings
}

// buildCiliumPolicy constructs an unstructured CiliumNetworkPolicy from the request.
func buildCiliumPolicy(req *CiliumPolicyRequest) *unstructured.Unstructured {
	spec := map[string]any{}

	// Endpoint selector
	if len(req.EndpointSelector) > 0 {
		spec["endpointSelector"] = map[string]any{
			"matchLabels": toStringAnyMap(req.EndpointSelector),
		}
	} else {
		spec["endpointSelector"] = map[string]any{}
	}

	// Ingress rules
	if len(req.IngressRules) > 0 {
		ingress, ingressDeny := buildDirectionalRules(req.IngressRules, "ingress")
		if len(ingress) > 0 {
			spec["ingress"] = ingress
		}
		if len(ingressDeny) > 0 {
			spec["ingressDeny"] = ingressDeny
		}
	}

	// Egress rules
	if len(req.EgressRules) > 0 {
		egress, egressDeny := buildDirectionalRules(req.EgressRules, "egress")
		if len(egress) > 0 {
			spec["egress"] = egress
		}
		if len(egressDeny) > 0 {
			spec["egressDeny"] = egressDeny
		}
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":      req.Name,
				"namespace": req.Namespace,
			},
			"spec": spec,
		},
	}

	return obj
}

// buildDirectionalRules builds rules with correct Cilium key names for the given direction.
// Ingress uses "from*" keys, egress uses "to*" keys.
func buildDirectionalRules(rules []CiliumPolicyRule, direction string) (allow []any, deny []any) {
	fromTo := "from"
	if direction == "egress" {
		fromTo = "to"
	}

	for _, r := range rules {
		rule := map[string]any{}

		switch r.PeerType {
		case "endpoints":
			rule[fromTo+"Endpoints"] = []any{
				map[string]any{"matchLabels": toStringAnyMap(r.Labels)},
			}
		case "entities":
			rule[fromTo+"Entities"] = toAnySlice(r.Entities)
		case "cidr":
			rule[fromTo+"CIDR"] = toAnySlice(r.CIDRs)
		}

		if len(r.Ports) > 0 {
			ports := make([]any, len(r.Ports))
			for i, p := range r.Ports {
				portStr := fmt.Sprintf("%d", p.Port)
				proto := strings.ToUpper(p.Protocol)
				if proto == "" || proto == "ANY" {
					proto = "ANY"
				}
				ports[i] = map[string]any{
					"port":     portStr,
					"protocol": proto,
				}
			}
			rule["toPorts"] = []any{
				map[string]any{"ports": ports},
			}
		}

		if r.Action == "deny" {
			deny = append(deny, rule)
		} else {
			allow = append(allow, rule)
		}
	}
	return
}

func toStringAnyMap(m map[string]string) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func toAnySlice(s []string) []any {
	result := make([]any, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}

