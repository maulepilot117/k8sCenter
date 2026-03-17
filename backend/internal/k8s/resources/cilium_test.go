package resources

import (
	"testing"
)

func TestValidateCiliumPolicy_Valid(t *testing.T) {
	req := &CiliumPolicyRequest{
		Name:             "allow-web",
		Namespace:        "default",
		EndpointSelector: map[string]string{"app": "web"},
		IngressRules: []CiliumPolicyRule{
			{
				PeerType: "endpoints",
				Labels:   map[string]string{"app": "frontend"},
				Ports:    []CiliumPortRule{{Port: 80, Protocol: "TCP"}},
				Action:   "allow",
			},
		},
	}
	errs := validateCiliumPolicy(req)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateCiliumPolicy_EmptyName(t *testing.T) {
	req := &CiliumPolicyRequest{Name: ""}
	errs := validateCiliumPolicy(req)
	if len(errs) == 0 {
		t.Fatal("expected error for empty name")
	}
	if errs[0] != "name is required" {
		t.Errorf("unexpected error: %s", errs[0])
	}
}

func TestValidateCiliumPolicy_InvalidName(t *testing.T) {
	req := &CiliumPolicyRequest{Name: "UPPERCASE"}
	errs := validateCiliumPolicy(req)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid name")
	}
}

func TestValidateCiliumPolicy_TooManyRules(t *testing.T) {
	req := &CiliumPolicyRequest{Name: "test"}
	for i := 0; i < 101; i++ {
		req.IngressRules = append(req.IngressRules, CiliumPolicyRule{
			PeerType: "entities", Entities: []string{"world"}, Action: "allow",
		})
	}
	errs := validateCiliumPolicy(req)
	found := false
	for _, e := range errs {
		if e == "too many rules (max 100 total)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'too many rules' error, got %v", errs)
	}
}

func TestValidateRule_InvalidAction(t *testing.T) {
	rule := &CiliumPolicyRule{PeerType: "endpoints", Action: "block"}
	errs := validateRule(rule)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid action")
	}
}

func TestValidateRule_InvalidPeerType(t *testing.T) {
	rule := &CiliumPolicyRule{PeerType: "unknown", Action: "allow"}
	errs := validateRule(rule)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid peerType")
	}
}

func TestValidateRule_InvalidEntity(t *testing.T) {
	rule := &CiliumPolicyRule{
		PeerType: "entities",
		Entities: []string{"world", "invalid-entity"},
		Action:   "allow",
	}
	errs := validateRule(rule)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid entity")
	}
}

func TestValidateRule_PortOutOfRange(t *testing.T) {
	rule := &CiliumPolicyRule{
		PeerType: "endpoints",
		Action:   "allow",
		Ports:    []CiliumPortRule{{Port: 0, Protocol: "TCP"}, {Port: 70000, Protocol: "TCP"}},
	}
	errs := validateRule(rule)
	if len(errs) < 2 {
		t.Errorf("expected 2 port errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateRule_InvalidProtocol(t *testing.T) {
	rule := &CiliumPolicyRule{
		PeerType: "endpoints",
		Action:   "allow",
		Ports:    []CiliumPortRule{{Port: 80, Protocol: "ICMP"}},
	}
	errs := validateRule(rule)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid protocol")
	}
}

func TestValidateRule_TooManyCIDRs(t *testing.T) {
	rule := &CiliumPolicyRule{PeerType: "cidr", Action: "allow"}
	for i := 0; i < 51; i++ {
		rule.CIDRs = append(rule.CIDRs, "10.0.0.0/8")
	}
	errs := validateRule(rule)
	found := false
	for _, e := range errs {
		if e == "too many CIDRs per rule (max 50)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'too many CIDRs' error, got %v", errs)
	}
}

func TestValidateRule_TooManyPorts(t *testing.T) {
	rule := &CiliumPolicyRule{PeerType: "endpoints", Action: "allow"}
	for i := 0; i < 101; i++ {
		rule.Ports = append(rule.Ports, CiliumPortRule{Port: i + 1, Protocol: "TCP"})
	}
	errs := validateRule(rule)
	found := false
	for _, e := range errs {
		if e == "too many ports per rule (max 100)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'too many ports' error, got %v", errs)
	}
}

func TestValidateCIDR_Valid(t *testing.T) {
	if err := validateCIDR("10.0.0.0/8"); err != "" {
		t.Errorf("expected valid, got %s", err)
	}
	if err := validateCIDR("192.168.1.0/24"); err != "" {
		t.Errorf("expected valid, got %s", err)
	}
}

func TestValidateCIDR_Invalid(t *testing.T) {
	if err := validateCIDR("not-a-cidr"); err == "" {
		t.Error("expected error for invalid CIDR")
	}
}

func TestValidateCIDR_Loopback(t *testing.T) {
	if err := validateCIDR("127.0.0.0/8"); err == "" {
		t.Error("expected error for loopback CIDR")
	}
}

func TestBuildCiliumPolicy_IngressAndEgress(t *testing.T) {
	req := &CiliumPolicyRequest{
		Name:             "test-policy",
		Namespace:        "default",
		EndpointSelector: map[string]string{"app": "web"},
		IngressRules: []CiliumPolicyRule{
			{PeerType: "endpoints", Labels: map[string]string{"app": "frontend"}, Action: "allow",
				Ports: []CiliumPortRule{{Port: 80, Protocol: "TCP"}}},
		},
		EgressRules: []CiliumPolicyRule{
			{PeerType: "entities", Entities: []string{"world"}, Action: "allow"},
			{PeerType: "cidr", CIDRs: []string{"10.0.0.0/8"}, Action: "deny"},
		},
	}

	obj := buildCiliumPolicy(req)

	if obj.GetName() != "test-policy" {
		t.Errorf("expected name test-policy, got %s", obj.GetName())
	}
	if obj.GetNamespace() != "default" {
		t.Errorf("expected namespace default, got %s", obj.GetNamespace())
	}

	spec, ok := obj.Object["spec"].(map[string]any)
	if !ok {
		t.Fatal("expected spec to be map")
	}

	if _, ok := spec["ingress"]; !ok {
		t.Error("expected ingress rules")
	}
	if _, ok := spec["egress"]; !ok {
		t.Error("expected egress allow rules")
	}
	if _, ok := spec["egressDeny"]; !ok {
		t.Error("expected egressDeny rules")
	}
}

func TestBuildDirectionalRules_IngressKeys(t *testing.T) {
	rules := []CiliumPolicyRule{
		{PeerType: "endpoints", Labels: map[string]string{"app": "x"}, Action: "allow"},
		{PeerType: "entities", Entities: []string{"world"}, Action: "deny"},
	}
	allow, deny := buildDirectionalRules(rules, "ingress")
	if len(allow) != 1 {
		t.Errorf("expected 1 allow rule, got %d", len(allow))
	}
	if len(deny) != 1 {
		t.Errorf("expected 1 deny rule, got %d", len(deny))
	}

	// Verify ingress uses "from" prefix
	allowRule := allow[0].(map[string]any)
	if _, ok := allowRule["fromEndpoints"]; !ok {
		t.Error("expected fromEndpoints key for ingress")
	}
	denyRule := deny[0].(map[string]any)
	if _, ok := denyRule["fromEntities"]; !ok {
		t.Error("expected fromEntities key for ingress deny")
	}
}

func TestBuildDirectionalRules_EgressKeys(t *testing.T) {
	rules := []CiliumPolicyRule{
		{PeerType: "cidr", CIDRs: []string{"10.0.0.0/8"}, Action: "allow"},
	}
	allow, deny := buildDirectionalRules(rules, "egress")
	if len(allow) != 1 || len(deny) != 0 {
		t.Errorf("expected 1 allow, 0 deny, got %d, %d", len(allow), len(deny))
	}
	allowRule := allow[0].(map[string]any)
	if _, ok := allowRule["toCIDR"]; !ok {
		t.Error("expected toCIDR key for egress")
	}
}

func TestDetectDangerousPolicy_ProtectedNamespace(t *testing.T) {
	req := &CiliumPolicyRequest{Name: "test", Namespace: "kube-system"}
	warnings := detectDangerousPolicy(req)
	if len(warnings) == 0 {
		t.Error("expected warning for protected namespace")
	}
	if warnings[0].Code != "protected_namespace" {
		t.Errorf("expected protected_namespace, got %s", warnings[0].Code)
	}
}

func TestDetectDangerousPolicy_BroadDeny(t *testing.T) {
	req := &CiliumPolicyRequest{
		Name:      "test",
		Namespace: "default",
		IngressRules: []CiliumPolicyRule{
			{PeerType: "entities", Entities: []string{"world"}, Action: "deny"},
		},
	}
	warnings := detectDangerousPolicy(req)
	found := false
	for _, w := range warnings {
		if w.Code == "broad_deny" {
			found = true
		}
	}
	if !found {
		t.Error("expected broad_deny warning")
	}
}

func TestDetectDangerousPolicy_WideCIDR(t *testing.T) {
	req := &CiliumPolicyRequest{
		Name:             "test",
		Namespace:        "default",
		EndpointSelector: map[string]string{"app": "web"},
		EgressRules: []CiliumPolicyRule{
			{PeerType: "cidr", CIDRs: []string{"0.0.0.0/0"}, Action: "allow"},
		},
	}
	warnings := detectDangerousPolicy(req)
	found := false
	for _, w := range warnings {
		if w.Code == "wide_cidr" {
			found = true
		}
	}
	if !found {
		t.Error("expected wide_cidr warning")
	}
}
