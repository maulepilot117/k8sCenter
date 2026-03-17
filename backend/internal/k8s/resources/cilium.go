package resources

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const kindCiliumNetworkPolicy = "ciliumnetworkpolicies"

var ciliumPolicyGVR = schema.GroupVersionResource{
	Group:    "cilium.io",
	Version:  "v2",
	Resource: "ciliumnetworkpolicies",
}

// CiliumPolicyRequest is the structured payload for creating a CiliumNetworkPolicy.
type CiliumPolicyRequest struct {
	Name             string            `json:"name"`
	Namespace        string            `json:"namespace"`
	EndpointSelector map[string]string `json:"endpointSelector"`
	IngressRules     []PolicyRule      `json:"ingressRules"`
	EgressRules      []PolicyRule      `json:"egressRules"`
}

// PolicyRule defines a single ingress or egress rule.
type PolicyRule struct {
	PeerType string            `json:"peerType"` // "endpoints", "entities", "cidr"
	Labels   map[string]string `json:"labels,omitempty"`
	Entities []string          `json:"entities,omitempty"`
	CIDRs    []string          `json:"cidrs,omitempty"`
	Ports    []PortRule        `json:"ports,omitempty"`
	Action   string            `json:"action"` // "allow", "deny"
}

// PortRule defines a port and protocol for a policy rule.
type PortRule struct {
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

// HandleListCiliumPolicies lists CiliumNetworkPolicies via dynamic client.
func (h *Handler) HandleListCiliumPolicies(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	ns := params.Namespace
	if ns != "" {
		if !h.checkAccess(w, r, user, "list", kindCiliumNetworkPolicy, ns) {
			return
		}
	} else {
		if !h.checkAccess(w, r, user, "list", kindCiliumNetworkPolicy, "") {
			return
		}
	}

	dc, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	var list *unstructured.UnstructuredList
	if ns != "" {
		list, err = dc.Resource(ciliumPolicyGVR).Namespace(ns).List(r.Context(), metav1.ListOptions{
			LabelSelector: params.LabelSelector,
		})
	} else {
		list, err = dc.Resource(ciliumPolicyGVR).List(r.Context(), metav1.ListOptions{
			LabelSelector: params.LabelSelector,
		})
	}
	if err != nil {
		mapK8sError(w, err, "list", "CiliumNetworkPolicy", ns, "")
		return
	}

	// Convert to pointer slice for paginate
	items := make([]*unstructured.Unstructured, len(list.Items))
	for i := range list.Items {
		items[i] = &list.Items[i]
	}
	paged, cont := paginate(items, params.Limit, params.Continue)
	writeList(w, paged, len(items), cont)
}

// HandleGetCiliumPolicy gets a single CiliumNetworkPolicy.
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

	dc, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	obj, err := dc.Resource(ciliumPolicyGVR).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
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

	dc, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := dc.Resource(ciliumPolicyGVR).Namespace(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "CiliumNetworkPolicy", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "CiliumNetworkPolicy", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "CiliumNetworkPolicy", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
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

	dc, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	created, err := dc.Resource(ciliumPolicyGVR).Namespace(ns).Create(r.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "CiliumNetworkPolicy", ns, req.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "CiliumNetworkPolicy", ns, req.Name)
		return
	}

	// Audit log with policy summary
	detail := policySummary(&req)
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         timeNow(),
		ClusterID:         h.ClusterID,
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            audit.ActionCreate,
		ResourceKind:      "CiliumNetworkPolicy",
		ResourceNamespace: ns,
		ResourceName:      created.GetName(),
		Result:            audit.ResultSuccess,
		Detail:            detail,
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"data":     created,
		"warnings": warnings,
	})
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

func validateRule(rule *PolicyRule) []string {
	var errs []string

	// Action
	if rule.Action != "allow" && rule.Action != "deny" {
		errs = append(errs, "action must be 'allow' or 'deny'")
	}

	// PeerType
	switch rule.PeerType {
	case "endpoints":
		if err := validateLabels(rule.Labels); err != "" {
			errs = append(errs, "labels: "+err)
		}
	case "entities":
		for _, e := range rule.Entities {
			if !validEntities[e] {
				errs = append(errs, fmt.Sprintf("invalid entity %q", e))
			}
		}
	case "cidr":
		for _, c := range rule.CIDRs {
			if err := validateCIDR(c); err != "" {
				errs = append(errs, err)
			}
		}
	default:
		errs = append(errs, fmt.Sprintf("invalid peerType %q, must be endpoints/entities/cidr", rule.PeerType))
	}

	// Ports
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
	for _, rules := range [][]PolicyRule{req.IngressRules, req.EgressRules} {
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
func buildDirectionalRules(rules []PolicyRule, direction string) (allow []any, deny []any) {
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

// policySummary returns a human-readable summary for audit logging.
func policySummary(req *CiliumPolicyRequest) string {
	parts := []string{fmt.Sprintf("policy=%s/%s", req.Namespace, req.Name)}
	if len(req.EndpointSelector) > 0 {
		labels := make([]string, 0, len(req.EndpointSelector))
		for k, v := range req.EndpointSelector {
			labels = append(labels, k+"="+v)
		}
		parts = append(parts, "selector="+strings.Join(labels, ","))
	}
	parts = append(parts, fmt.Sprintf("ingress=%d", len(req.IngressRules)))
	parts = append(parts, fmt.Sprintf("egress=%d", len(req.EgressRules)))
	return strings.Join(parts, " ")
}
