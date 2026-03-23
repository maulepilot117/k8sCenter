# Phase 4D Batch 3: NetworkPolicy, HPA, PDB Wizards

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add creation wizards for NetworkPolicy, HorizontalPodAutoscaler, and PodDisruptionBudget — completing the Phase 4D wizard suite.

**Architecture:** Each resource gets a backend wizard input type (Validate + ToYAML) registered in routes.go, a frontend wizard island with form steps + YAML review, a `/new` route page, and a `createHref` on the existing resource table page. Follows the identical pattern used by all 14 existing wizards.

**Tech Stack:** Go (client-go typed structs, sigs.k8s.io/yaml), Preact/Fresh 2.x (signals, islands), Tailwind CSS v4

---

## File Structure

### Backend (new files)
- `backend/internal/wizard/networkpolicy.go` — NetworkPolicyInput struct + Validate + ToYAML
- `backend/internal/wizard/networkpolicy_test.go` — Validation + YAML generation tests
- `backend/internal/wizard/hpa.go` — HPAInput struct + Validate + ToYAML
- `backend/internal/wizard/hpa_test.go` — Validation + YAML generation tests
- `backend/internal/wizard/pdb.go` — PDBInput struct + Validate + ToYAML
- `backend/internal/wizard/pdb_test.go` — Validation + YAML generation tests

### Backend (modify)
- `backend/internal/server/routes.go` — Register 3 new wizard preview endpoints (~3 lines)

### Frontend (new files)
- `frontend/islands/NetworkPolicyWizard.tsx` — 3-step wizard (Basics, Rules, Review)
- `frontend/islands/HPAWizard.tsx` — 2-step wizard (Configure, Review)
- `frontend/islands/PDBWizard.tsx` — 2-step wizard (Configure, Review)
- `frontend/routes/networking/networkpolicies/new.tsx` — Route page for NetworkPolicy wizard
- `frontend/routes/scaling/hpas/new.tsx` — Route page for HPA wizard
- `frontend/routes/scaling/pdbs/new.tsx` — Route page for PDB wizard

### Frontend (modify)
- `frontend/routes/networking/networkpolicies.tsx` — Add `createHref`
- `frontend/routes/scaling/hpas.tsx` — Add `createHref`
- `frontend/routes/scaling/pdbs.tsx` — Add `createHref`

---

## Task 1: NetworkPolicy Backend Wizard Input

**Files:**
- Create: `backend/internal/wizard/networkpolicy.go`
- Create: `backend/internal/wizard/networkpolicy_test.go`

- [ ] **Step 1: Write the test file**

```go
// backend/internal/wizard/networkpolicy_test.go
package wizard

import (
	"strings"
	"testing"
)

func TestNetworkPolicyInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      NetworkPolicyInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid ingress only",
			input: NetworkPolicyInput{
				Name: "allow-web", Namespace: "default",
				PodSelector: map[string]string{"app": "web"},
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{{
					Ports: []NetworkPolicyPortInput{{Port: 80, Protocol: "TCP"}},
				}},
			},
			wantErrors: 0,
		},
		{
			name: "valid egress only",
			input: NetworkPolicyInput{
				Name: "deny-egress", Namespace: "prod",
				PodSelector: map[string]string{},
				PolicyTypes: []string{"Egress"},
			},
			wantErrors: 0,
		},
		{
			name: "valid ingress+egress",
			input: NetworkPolicyInput{
				Name: "full-policy", Namespace: "default",
				PodSelector: map[string]string{"tier": "backend"},
				PolicyTypes: []string{"Ingress", "Egress"},
				Ingress: []NetworkPolicyRuleInput{{
					From: []NetworkPolicyPeerInput{{
						NamespaceSelector: map[string]string{"env": "prod"},
					}},
					Ports: []NetworkPolicyPortInput{{Port: 443, Protocol: "TCP"}},
				}},
				Egress: []NetworkPolicyRuleInput{{
					To: []NetworkPolicyPeerInput{{
						IPBlock: &IPBlockInput{CIDR: "10.0.0.0/8"},
					}},
					Ports: []NetworkPolicyPortInput{{Port: 5432, Protocol: "TCP"}},
				}},
			},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      NetworkPolicyInput{Namespace: "default", PolicyTypes: []string{"Ingress"}},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      NetworkPolicyInput{Name: "pol", PolicyTypes: []string{"Ingress"}},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "missing policyTypes",
			input:      NetworkPolicyInput{Name: "pol", Namespace: "default"},
			wantErrors: 1, wantFields: []string{"policyTypes"},
		},
		{
			name:       "invalid policyType",
			input:      NetworkPolicyInput{Name: "pol", Namespace: "default", PolicyTypes: []string{"Invalid"}},
			wantErrors: 1, wantFields: []string{"policyTypes[0]"},
		},
		{
			name: "invalid port number",
			input: NetworkPolicyInput{
				Name: "pol", Namespace: "default", PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{{
					Ports: []NetworkPolicyPortInput{{Port: 99999, Protocol: "TCP"}},
				}},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].ports[0].port"},
		},
		{
			name: "invalid protocol",
			input: NetworkPolicyInput{
				Name: "pol", Namespace: "default", PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{{
					Ports: []NetworkPolicyPortInput{{Port: 80, Protocol: "HTTP"}},
				}},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].ports[0].protocol"},
		},
		{
			name: "invalid CIDR",
			input: NetworkPolicyInput{
				Name: "pol", Namespace: "default", PolicyTypes: []string{"Egress"},
				Egress: []NetworkPolicyRuleInput{{
					To: []NetworkPolicyPeerInput{{
						IPBlock: &IPBlockInput{CIDR: "not-a-cidr"},
					}},
				}},
			},
			wantErrors: 1, wantFields: []string{"egress[0].to[0].ipBlock.cidr"},
		},
		{
			name: "invalid except CIDR",
			input: NetworkPolicyInput{
				Name: "pol", Namespace: "default", PolicyTypes: []string{"Egress"},
				Egress: []NetworkPolicyRuleInput{{
					To: []NetworkPolicyPeerInput{{
						IPBlock: &IPBlockInput{CIDR: "10.0.0.0/8", Except: []string{"bad"}},
					}},
				}},
			},
			wantErrors: 1, wantFields: []string{"egress[0].to[0].ipBlock.except[0]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if len(errs) != tt.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrors, len(errs), errs)
			}
			for _, wf := range tt.wantFields {
				found := false
				for _, e := range errs {
					if e.Field == wf {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, not found in %v", wf, errs)
				}
			}
		})
	}
}

func TestNetworkPolicyInputToYAML(t *testing.T) {
	input := NetworkPolicyInput{
		Name: "allow-web", Namespace: "default",
		PodSelector: map[string]string{"app": "web"},
		PolicyTypes: []string{"Ingress"},
		Ingress: []NetworkPolicyRuleInput{{
			From: []NetworkPolicyPeerInput{{
				PodSelector: map[string]string{"role": "frontend"},
			}},
			Ports: []NetworkPolicyPortInput{{Port: 80, Protocol: "TCP"}},
		}},
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: NetworkPolicy") {
		t.Error("expected kind: NetworkPolicy")
	}
	if !strings.Contains(yaml, "name: allow-web") {
		t.Error("expected name: allow-web")
	}
	if !strings.Contains(yaml, "app: web") {
		t.Error("expected podSelector label")
	}
	if !strings.Contains(yaml, "Ingress") {
		t.Error("expected policyTypes to contain Ingress")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/wizard/ -run TestNetworkPolicy -v`
Expected: FAIL — `NetworkPolicyInput` undefined

- [ ] **Step 3: Write the implementation**

```go
// backend/internal/wizard/networkpolicy.go
package wizard

import (
	"fmt"
	"net"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	sigsyaml "sigs.k8s.io/yaml"
)

// corev1 Protocol constants as strings for comparison.
var validProtocols = map[string]bool{"TCP": true, "UDP": true, "SCTP": true}

// NetworkPolicyInput represents the wizard form data for creating a NetworkPolicy.
type NetworkPolicyInput struct {
	Name        string                   `json:"name"`
	Namespace   string                   `json:"namespace"`
	PodSelector map[string]string        `json:"podSelector"`
	PolicyTypes []string                 `json:"policyTypes"`
	Ingress     []NetworkPolicyRuleInput `json:"ingress,omitempty"`
	Egress      []NetworkPolicyRuleInput `json:"egress,omitempty"`
}

// NetworkPolicyRuleInput represents one ingress or egress rule.
type NetworkPolicyRuleInput struct {
	From  []NetworkPolicyPeerInput `json:"from,omitempty"`
	To    []NetworkPolicyPeerInput `json:"to,omitempty"`
	Ports []NetworkPolicyPortInput `json:"ports,omitempty"`
}

// NetworkPolicyPeerInput represents a peer selector (pod, namespace, or IP block).
type NetworkPolicyPeerInput struct {
	PodSelector       map[string]string `json:"podSelector,omitempty"`
	NamespaceSelector map[string]string `json:"namespaceSelector,omitempty"`
	IPBlock           *IPBlockInput     `json:"ipBlock,omitempty"`
}

// IPBlockInput represents an IP CIDR block with optional exceptions.
type IPBlockInput struct {
	CIDR   string   `json:"cidr"`
	Except []string `json:"except,omitempty"`
}

// NetworkPolicyPortInput represents a port + protocol.
type NetworkPolicyPortInput struct {
	Port     int32  `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// Validate checks the NetworkPolicyInput and returns field-level errors.
func (n *NetworkPolicyInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(n.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if n.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(n.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if len(n.PolicyTypes) == 0 {
		errs = append(errs, FieldError{Field: "policyTypes", Message: "at least one policy type (Ingress or Egress) is required"})
	}
	for i, pt := range n.PolicyTypes {
		if pt != "Ingress" && pt != "Egress" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("policyTypes[%d]", i),
				Message: "must be Ingress or Egress",
			})
		}
	}

	for i, rule := range n.Ingress {
		errs = append(errs, validateNetworkPolicyRule(fmt.Sprintf("ingress[%d]", i), rule)...)
	}
	for i, rule := range n.Egress {
		errs = append(errs, validateNetworkPolicyRule(fmt.Sprintf("egress[%d]", i), rule)...)
	}

	return errs
}

func validateNetworkPolicyRule(prefix string, rule NetworkPolicyRuleInput) []FieldError {
	var errs []FieldError

	for i, port := range rule.Ports {
		pf := fmt.Sprintf("%s.ports[%d]", prefix, i)
		if port.Port < 0 || port.Port > 65535 {
			errs = append(errs, FieldError{Field: pf + ".port", Message: "must be between 0 and 65535"})
		}
		if port.Protocol != "" && !validProtocols[port.Protocol] {
			errs = append(errs, FieldError{Field: pf + ".protocol", Message: "must be TCP, UDP, or SCTP"})
		}
	}

	// Validate peers (from or to)
	peers := rule.From
	peerPrefix := prefix + ".from"
	if len(peers) == 0 {
		peers = rule.To
		peerPrefix = prefix + ".to"
	}
	for i, peer := range peers {
		if peer.IPBlock != nil {
			ipf := fmt.Sprintf("%s[%d].ipBlock", peerPrefix, i)
			if _, _, err := net.ParseCIDR(peer.IPBlock.CIDR); err != nil {
				errs = append(errs, FieldError{Field: ipf + ".cidr", Message: "must be a valid CIDR (e.g. 10.0.0.0/8)"})
			}
			for j, exc := range peer.IPBlock.Except {
				if _, _, err := net.ParseCIDR(exc); err != nil {
					errs = append(errs, FieldError{
						Field:   fmt.Sprintf("%s.except[%d]", ipf, j),
						Message: "must be a valid CIDR",
					})
				}
			}
		}
	}

	return errs
}

// ToYAML implements WizardInput.
func (n *NetworkPolicyInput) ToYAML() (string, error) {
	np := n.toNetworkPolicy()
	yamlBytes, err := sigsyaml.Marshal(np)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

func (n *NetworkPolicyInput) toNetworkPolicy() *networkingv1.NetworkPolicy {
	np := &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "NetworkPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      n.Name,
			Namespace: n.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: n.PodSelector,
			},
		},
	}

	for _, pt := range n.PolicyTypes {
		np.Spec.PolicyTypes = append(np.Spec.PolicyTypes, networkingv1.PolicyType(pt))
	}

	for _, rule := range n.Ingress {
		np.Spec.Ingress = append(np.Spec.Ingress, buildIngressRule(rule))
	}
	for _, rule := range n.Egress {
		np.Spec.Egress = append(np.Spec.Egress, buildEgressRule(rule))
	}

	return np
}

func buildIngressRule(r NetworkPolicyRuleInput) networkingv1.NetworkPolicyIngressRule {
	rule := networkingv1.NetworkPolicyIngressRule{
		Ports: buildPorts(r.Ports),
	}
	for _, peer := range r.From {
		rule.From = append(rule.From, buildPeer(peer))
	}
	return rule
}

func buildEgressRule(r NetworkPolicyRuleInput) networkingv1.NetworkPolicyEgressRule {
	rule := networkingv1.NetworkPolicyEgressRule{
		Ports: buildPorts(r.Ports),
	}
	for _, peer := range r.To {
		rule.To = append(rule.To, buildPeer(peer))
	}
	return rule
}

func buildPorts(ports []NetworkPolicyPortInput) []networkingv1.NetworkPolicyPort {
	var result []networkingv1.NetworkPolicyPort
	for _, p := range ports {
		npp := networkingv1.NetworkPolicyPort{}
		if p.Port > 0 {
			port := intstr.FromInt32(p.Port)
			npp.Port = &port
		}
		if p.Protocol != "" {
			proto := corev1Protocol(p.Protocol)
			npp.Protocol = &proto
		}
		result = append(result, npp)
	}
	return result
}

func buildPeer(peer NetworkPolicyPeerInput) networkingv1.NetworkPolicyPeer {
	npp := networkingv1.NetworkPolicyPeer{}
	if peer.PodSelector != nil {
		npp.PodSelector = &metav1.LabelSelector{MatchLabels: peer.PodSelector}
	}
	if peer.NamespaceSelector != nil {
		npp.NamespaceSelector = &metav1.LabelSelector{MatchLabels: peer.NamespaceSelector}
	}
	if peer.IPBlock != nil {
		npp.IPBlock = &networkingv1.IPBlock{
			CIDR:   peer.IPBlock.CIDR,
			Except: peer.IPBlock.Except,
		}
	}
	return npp
}

func corev1Protocol(s string) corev1.Protocol {
	switch s {
	case "UDP":
		return corev1.ProtocolUDP
	case "SCTP":
		return corev1.ProtocolSCTP
	default:
		return corev1.ProtocolTCP
	}
}
```

Note: The `corev1Protocol` function needs `corev1 "k8s.io/api/core/v1"` added to the imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/wizard/ -run TestNetworkPolicy -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/wizard/networkpolicy.go backend/internal/wizard/networkpolicy_test.go
git commit -m "feat(wizard): add NetworkPolicy input type with validation and YAML generation"
```

---

## Task 2: HPA Backend Wizard Input

**Files:**
- Create: `backend/internal/wizard/hpa.go`
- Create: `backend/internal/wizard/hpa_test.go`

- [ ] **Step 1: Write the test file**

```go
// backend/internal/wizard/hpa_test.go
package wizard

import (
	"strings"
	"testing"
)

func TestHPAInputValidate(t *testing.T) {
	minReplicas := int32(1)
	tests := []struct {
		name       string
		input      HPAInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid CPU metric",
			input: HPAInput{
				Name: "web-hpa", Namespace: "default",
				TargetKind: "Deployment", TargetName: "web",
				MinReplicas: &minReplicas, MaxReplicas: 10,
				Metrics: []HPAMetricInput{{
					Type:               "Resource",
					ResourceName:       "cpu",
					TargetType:         "Utilization",
					TargetAverageValue: 80,
				}},
			},
			wantErrors: 0,
		},
		{
			name: "valid memory metric",
			input: HPAInput{
				Name: "mem-hpa", Namespace: "prod",
				TargetKind: "StatefulSet", TargetName: "db",
				MinReplicas: &minReplicas, MaxReplicas: 5,
				Metrics: []HPAMetricInput{{
					Type:               "Resource",
					ResourceName:       "memory",
					TargetType:         "Utilization",
					TargetAverageValue: 70,
				}},
			},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      HPAInput{Namespace: "default", TargetKind: "Deployment", TargetName: "web", MaxReplicas: 10},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      HPAInput{Name: "hpa", TargetKind: "Deployment", TargetName: "web", MaxReplicas: 10},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "missing targetName",
			input:      HPAInput{Name: "hpa", Namespace: "default", TargetKind: "Deployment", MaxReplicas: 10},
			wantErrors: 1, wantFields: []string{"targetName"},
		},
		{
			name:       "invalid targetKind",
			input:      HPAInput{Name: "hpa", Namespace: "default", TargetKind: "Pod", TargetName: "web", MaxReplicas: 10},
			wantErrors: 1, wantFields: []string{"targetKind"},
		},
		{
			name:       "maxReplicas zero",
			input:      HPAInput{Name: "hpa", Namespace: "default", TargetKind: "Deployment", TargetName: "web", MaxReplicas: 0},
			wantErrors: 1, wantFields: []string{"maxReplicas"},
		},
		{
			name: "minReplicas > maxReplicas",
			input: func() HPAInput {
				min := int32(20)
				return HPAInput{
					Name: "hpa", Namespace: "default", TargetKind: "Deployment", TargetName: "web",
					MinReplicas: &min, MaxReplicas: 10,
				}
			}(),
			wantErrors: 1, wantFields: []string{"minReplicas"},
		},
		{
			name: "no metrics",
			input: HPAInput{
				Name: "hpa", Namespace: "default", TargetKind: "Deployment", TargetName: "web",
				MaxReplicas: 10, Metrics: []HPAMetricInput{},
			},
			wantErrors: 1, wantFields: []string{"metrics"},
		},
		{
			name: "invalid metric type",
			input: HPAInput{
				Name: "hpa", Namespace: "default", TargetKind: "Deployment", TargetName: "web",
				MaxReplicas: 10,
				Metrics: []HPAMetricInput{{Type: "Invalid", ResourceName: "cpu", TargetType: "Utilization", TargetAverageValue: 80}},
			},
			wantErrors: 1, wantFields: []string{"metrics[0].type"},
		},
		{
			name: "invalid targetAverageValue",
			input: HPAInput{
				Name: "hpa", Namespace: "default", TargetKind: "Deployment", TargetName: "web",
				MaxReplicas: 10,
				Metrics: []HPAMetricInput{{Type: "Resource", ResourceName: "cpu", TargetType: "Utilization", TargetAverageValue: 0}},
			},
			wantErrors: 1, wantFields: []string{"metrics[0].targetAverageValue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if len(errs) != tt.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrors, len(errs), errs)
			}
			for _, wf := range tt.wantFields {
				found := false
				for _, e := range errs {
					if e.Field == wf {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, not found in %v", wf, errs)
				}
			}
		})
	}
}

func TestHPAInputToYAML(t *testing.T) {
	minReplicas := int32(2)
	input := HPAInput{
		Name: "web-hpa", Namespace: "prod",
		TargetKind: "Deployment", TargetName: "web-app",
		MinReplicas: &minReplicas, MaxReplicas: 10,
		Metrics: []HPAMetricInput{{
			Type:               "Resource",
			ResourceName:       "cpu",
			TargetType:         "Utilization",
			TargetAverageValue: 80,
		}},
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: HorizontalPodAutoscaler") {
		t.Error("expected kind: HorizontalPodAutoscaler")
	}
	if !strings.Contains(yaml, "name: web-hpa") {
		t.Error("expected name: web-hpa")
	}
	if !strings.Contains(yaml, "name: web-app") {
		t.Error("expected scaleTargetRef name")
	}
	if !strings.Contains(yaml, "cpu") {
		t.Error("expected cpu in metrics")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/wizard/ -run TestHPA -v`
Expected: FAIL — `HPAInput` undefined

- [ ] **Step 3: Write the implementation**

```go
// backend/internal/wizard/hpa.go
package wizard

import (
	"fmt"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// HPAInput represents the wizard form data for creating a HorizontalPodAutoscaler.
type HPAInput struct {
	Name        string           `json:"name"`
	Namespace   string           `json:"namespace"`
	TargetKind  string           `json:"targetKind"`
	TargetName  string           `json:"targetName"`
	MinReplicas *int32           `json:"minReplicas,omitempty"`
	MaxReplicas int32            `json:"maxReplicas"`
	Metrics     []HPAMetricInput `json:"metrics"`
}

// HPAMetricInput represents a single metric for the HPA.
type HPAMetricInput struct {
	Type               string `json:"type"`
	ResourceName       string `json:"resourceName,omitempty"`
	TargetType         string `json:"targetType"`
	TargetAverageValue int32  `json:"targetAverageValue"`
}

// validHPATargetKinds are the k8s kinds that can be scaled by an HPA.
var validHPATargetKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"ReplicaSet":  true,
}

// Validate checks the HPAInput and returns field-level errors.
func (h *HPAInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(h.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if h.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(h.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if !validHPATargetKinds[h.TargetKind] {
		errs = append(errs, FieldError{Field: "targetKind", Message: "must be Deployment, StatefulSet, or ReplicaSet"})
	}
	if h.TargetName == "" {
		errs = append(errs, FieldError{Field: "targetName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(h.TargetName) {
		errs = append(errs, FieldError{Field: "targetName", Message: "must be a valid DNS label"})
	}

	if h.MaxReplicas < 1 {
		errs = append(errs, FieldError{Field: "maxReplicas", Message: "must be at least 1"})
	}
	if h.MinReplicas != nil && *h.MinReplicas > h.MaxReplicas {
		errs = append(errs, FieldError{Field: "minReplicas", Message: "must not exceed maxReplicas"})
	}

	if len(h.Metrics) == 0 {
		errs = append(errs, FieldError{Field: "metrics", Message: "at least one metric is required"})
	}
	for i, m := range h.Metrics {
		mf := fmt.Sprintf("metrics[%d]", i)
		if m.Type != "Resource" {
			errs = append(errs, FieldError{Field: mf + ".type", Message: "must be Resource"})
		}
		if m.Type == "Resource" && m.ResourceName != "cpu" && m.ResourceName != "memory" {
			errs = append(errs, FieldError{Field: mf + ".resourceName", Message: "must be cpu or memory"})
		}
		if m.TargetType != "Utilization" && m.TargetType != "AverageValue" {
			errs = append(errs, FieldError{Field: mf + ".targetType", Message: "must be Utilization or AverageValue"})
		}
		if m.TargetAverageValue < 1 {
			errs = append(errs, FieldError{Field: mf + ".targetAverageValue", Message: "must be at least 1"})
		}
	}

	return errs
}

// ToYAML implements WizardInput.
func (h *HPAInput) ToYAML() (string, error) {
	hpa := h.toHPA()
	yamlBytes, err := sigsyaml.Marshal(hpa)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

// apiVersionForKind returns the autoscaling API group version for the target kind.
func apiVersionForKind(kind string) string {
	switch kind {
	case "Deployment", "StatefulSet", "ReplicaSet":
		return "apps/v1"
	default:
		return "apps/v1"
	}
}

func (h *HPAInput) toHPA() *autoscalingv2.HorizontalPodAutoscaler {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "autoscaling/v2",
			Kind:       "HorizontalPodAutoscaler",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: apiVersionForKind(h.TargetKind),
				Kind:       h.TargetKind,
				Name:       h.TargetName,
			},
			MaxReplicas: h.MaxReplicas,
		},
	}

	if h.MinReplicas != nil {
		hpa.Spec.MinReplicas = h.MinReplicas
	}

	for _, m := range h.Metrics {
		metric := autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1ResourceName(m.ResourceName),
				Target: autoscalingv2.MetricTarget{},
			},
		}
		switch m.TargetType {
		case "Utilization":
			avg := m.TargetAverageValue
			metric.Resource.Target.Type = autoscalingv2.UtilizationMetricType
			metric.Resource.Target.AverageUtilization = &avg
		case "AverageValue":
			// For AverageValue, we use resource.Quantity
			metric.Resource.Target.Type = autoscalingv2.AverageValueMetricType
		}
		hpa.Spec.Metrics = append(hpa.Spec.Metrics, metric)
	}

	return hpa
}

func corev1ResourceName(name string) corev1.ResourceName {
	switch name {
	case "memory":
		return corev1.ResourceMemory
	default:
		return corev1.ResourceCPU
	}
}
```

Note: Needs `corev1 "k8s.io/api/core/v1"` import — same as networkpolicy.go.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/wizard/ -run TestHPA -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/wizard/hpa.go backend/internal/wizard/hpa_test.go
git commit -m "feat(wizard): add HPA input type with validation and YAML generation"
```

---

## Task 3: PDB Backend Wizard Input

**Files:**
- Create: `backend/internal/wizard/pdb.go`
- Create: `backend/internal/wizard/pdb_test.go`

- [ ] **Step 1: Write the test file**

```go
// backend/internal/wizard/pdb_test.go
package wizard

import (
	"strings"
	"testing"
)

func TestPDBInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      PDBInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid minAvailable number",
			input: PDBInput{
				Name: "web-pdb", Namespace: "default",
				Selector:     map[string]string{"app": "web"},
				MinAvailable: strPtr("2"),
			},
			wantErrors: 0,
		},
		{
			name: "valid minAvailable percentage",
			input: PDBInput{
				Name: "web-pdb", Namespace: "default",
				Selector:     map[string]string{"app": "web"},
				MinAvailable: strPtr("50%"),
			},
			wantErrors: 0,
		},
		{
			name: "valid maxUnavailable",
			input: PDBInput{
				Name: "web-pdb", Namespace: "default",
				Selector:       map[string]string{"app": "web"},
				MaxUnavailable: strPtr("1"),
			},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      PDBInput{Namespace: "default", Selector: map[string]string{"app": "web"}, MinAvailable: strPtr("1")},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      PDBInput{Name: "pdb", Selector: map[string]string{"app": "web"}, MinAvailable: strPtr("1")},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "empty selector",
			input:      PDBInput{Name: "pdb", Namespace: "default", MinAvailable: strPtr("1")},
			wantErrors: 1, wantFields: []string{"selector"},
		},
		{
			name: "both minAvailable and maxUnavailable",
			input: PDBInput{
				Name: "pdb", Namespace: "default",
				Selector: map[string]string{"app": "web"},
				MinAvailable: strPtr("1"), MaxUnavailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"maxUnavailable"},
		},
		{
			name: "neither minAvailable nor maxUnavailable",
			input: PDBInput{
				Name: "pdb", Namespace: "default",
				Selector: map[string]string{"app": "web"},
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
		{
			name: "invalid minAvailable value",
			input: PDBInput{
				Name: "pdb", Namespace: "default",
				Selector:     map[string]string{"app": "web"},
				MinAvailable: strPtr("abc"),
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
		{
			name: "negative minAvailable",
			input: PDBInput{
				Name: "pdb", Namespace: "default",
				Selector:     map[string]string{"app": "web"},
				MinAvailable: strPtr("-1"),
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if len(errs) != tt.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrors, len(errs), errs)
			}
			for _, wf := range tt.wantFields {
				found := false
				for _, e := range errs {
					if e.Field == wf {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, not found in %v", wf, errs)
				}
			}
		})
	}
}

func TestPDBInputToYAML(t *testing.T) {
	input := PDBInput{
		Name: "web-pdb", Namespace: "prod",
		Selector:     map[string]string{"app": "web"},
		MinAvailable: strPtr("2"),
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: PodDisruptionBudget") {
		t.Error("expected kind: PodDisruptionBudget")
	}
	if !strings.Contains(yaml, "name: web-pdb") {
		t.Error("expected name: web-pdb")
	}
	if !strings.Contains(yaml, "app: web") {
		t.Error("expected selector label")
	}
	if !strings.Contains(yaml, "minAvailable") {
		t.Error("expected minAvailable in spec")
	}
}

func strPtr(s string) *string { return &s }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/wizard/ -run TestPDB -v`
Expected: FAIL — `PDBInput` undefined

- [ ] **Step 3: Write the implementation**

```go
// backend/internal/wizard/pdb.go
package wizard

import (
	"regexp"
	"strconv"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	sigsyaml "sigs.k8s.io/yaml"
)

// intOrPercentRegex matches a non-negative integer or a percentage (e.g. "2", "50%").
var intOrPercentRegex = regexp.MustCompile(`^(\d+%?)$`)

// PDBInput represents the wizard form data for creating a PodDisruptionBudget.
type PDBInput struct {
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	Selector       map[string]string `json:"selector"`
	MinAvailable   *string           `json:"minAvailable,omitempty"`
	MaxUnavailable *string           `json:"maxUnavailable,omitempty"`
}

// Validate checks the PDBInput and returns field-level errors.
func (p *PDBInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(p.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if p.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(p.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if len(p.Selector) == 0 {
		errs = append(errs, FieldError{Field: "selector", Message: "at least one label is required"})
	}

	hasMin := p.MinAvailable != nil && *p.MinAvailable != ""
	hasMax := p.MaxUnavailable != nil && *p.MaxUnavailable != ""

	if hasMin && hasMax {
		errs = append(errs, FieldError{Field: "maxUnavailable", Message: "cannot set both minAvailable and maxUnavailable"})
	} else if !hasMin && !hasMax {
		errs = append(errs, FieldError{Field: "minAvailable", Message: "either minAvailable or maxUnavailable is required"})
	}

	if hasMin {
		errs = append(errs, validateIntOrPercent("minAvailable", *p.MinAvailable)...)
	}
	if hasMax {
		errs = append(errs, validateIntOrPercent("maxUnavailable", *p.MaxUnavailable)...)
	}

	return errs
}

func validateIntOrPercent(field, value string) []FieldError {
	if !intOrPercentRegex.MatchString(value) {
		return []FieldError{{Field: field, Message: "must be a non-negative integer or percentage (e.g. 2, 50%)"}}
	}
	// If not a percentage, check it's a valid non-negative number
	if value[len(value)-1] != '%' {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return []FieldError{{Field: field, Message: "must be a non-negative integer or percentage"}}
		}
	}
	return nil
}

// ToYAML implements WizardInput.
func (p *PDBInput) ToYAML() (string, error) {
	pdb := p.toPDB()
	yamlBytes, err := sigsyaml.Marshal(pdb)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

func (p *PDBInput) toPDB() *policyv1.PodDisruptionBudget {
	pdb := &policyv1.PodDisruptionBudget{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "policy/v1",
			Kind:       "PodDisruptionBudget",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: p.Selector,
			},
		},
	}

	if p.MinAvailable != nil && *p.MinAvailable != "" {
		val := intstr.Parse(*p.MinAvailable)
		pdb.Spec.MinAvailable = &val
	}
	if p.MaxUnavailable != nil && *p.MaxUnavailable != "" {
		val := intstr.Parse(*p.MaxUnavailable)
		pdb.Spec.MaxUnavailable = &val
	}

	return pdb
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/wizard/ -run TestPDB -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/wizard/pdb.go backend/internal/wizard/pdb_test.go
git commit -m "feat(wizard): add PDB input type with validation and YAML generation"
```

---

## Task 4: Register Backend Wizard Routes

**Files:**
- Modify: `backend/internal/server/routes.go:207` — Add 3 new wizard preview routes

- [ ] **Step 1: Add route registrations**

After line 207 (the statefulset route), add:

```go
wr.Post("/networkpolicy/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.NetworkPolicyInput{} }))
wr.Post("/hpa/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.HPAInput{} }))
wr.Post("/pdb/preview", h.HandlePreview(func() wizard.WizardInput { return &wizard.PDBInput{} }))
```

- [ ] **Step 2: Run all wizard tests to verify no regressions**

Run: `cd backend && go test ./internal/wizard/ -v -count=1`
Expected: all PASS

- [ ] **Step 3: Run go vet**

Run: `cd backend && go vet ./...`
Expected: clean

- [ ] **Step 4: Commit**

```bash
git add backend/internal/server/routes.go
git commit -m "feat(wizard): register NetworkPolicy, HPA, PDB preview routes"
```

---

## Task 5: NetworkPolicy Frontend Wizard

**Files:**
- Create: `frontend/islands/NetworkPolicyWizard.tsx`
- Create: `frontend/routes/networking/networkpolicies/new.tsx`
- Modify: `frontend/routes/networking/networkpolicies.tsx` — Add `createHref`

The NetworkPolicy wizard is 3 steps: Basics (name, namespace, podSelector, policyTypes), Rules (ingress/egress rule builder), Review (YAML preview).

- [ ] **Step 1: Create the wizard island**

Create `frontend/islands/NetworkPolicyWizard.tsx` following the IngressWizard pattern:

**Key form state:**
```typescript
interface NetworkPolicyFormState {
  name: string;
  namespace: string;
  podSelectorLabels: LabelEntry[];
  policyTypes: string[];   // "Ingress" | "Egress"
  ingressRules: NPRuleState[];
  egressRules: NPRuleState[];
}

interface NPRuleState {
  peers: NPPeerState[];
  ports: NPPortState[];
}

interface NPPeerState {
  type: "podSelector" | "namespaceSelector" | "ipBlock";
  labels: LabelEntry[];          // for pod/namespace selector
  cidr: string;                   // for ipBlock
  except: string[];               // for ipBlock
}

interface NPPortState {
  port: number;
  protocol: "TCP" | "UDP" | "SCTP";
}

interface LabelEntry { key: string; value: string }
```

**Step 1 (Basics):** Name, namespace dropdown, pod selector label editor (add/remove key-value rows), policy type checkboxes (Ingress, Egress — at least one required).

**Step 2 (Rules):** Conditional sections based on policyTypes. If Ingress checked: ingress rules list (add/remove rules, each with peers + ports). If Egress checked: egress rules list. Each peer has a type dropdown (podSelector, namespaceSelector, ipBlock) and corresponding fields. Each port has port number + protocol dropdown.

**Step 3 (Review):** WizardReviewStep with YAML preview.

The preview payload maps form state to the backend `NetworkPolicyInput` structure.

- [ ] **Step 2: Create the route page**

```tsx
// frontend/routes/networking/networkpolicies/new.tsx
import { define } from "@/utils.ts";
import NetworkPolicyWizard from "@/islands/NetworkPolicyWizard.tsx";

export default define.page(function NewNetworkPolicyPage() {
  return <NetworkPolicyWizard />;
});
```

- [ ] **Step 3: Add createHref to the resource table page**

Modify `frontend/routes/networking/networkpolicies.tsx`:

```tsx
import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function NetworkPoliciesPage() {
  return (
    <ResourceTable
      kind="networkpolicies"
      title="Network Policies"
      createHref="/networking/networkpolicies/new"
    />
  );
});
```

- [ ] **Step 4: Verify frontend builds**

Run: `cd frontend && deno task build`
Expected: clean build, no type errors

- [ ] **Step 5: Commit**

```bash
git add frontend/islands/NetworkPolicyWizard.tsx frontend/routes/networking/networkpolicies/new.tsx frontend/routes/networking/networkpolicies.tsx
git commit -m "feat(wizard): add NetworkPolicy creation wizard with full rule builder"
```

---

## Task 6: HPA Frontend Wizard

**Files:**
- Create: `frontend/islands/HPAWizard.tsx`
- Create: `frontend/routes/scaling/hpas/new.tsx`
- Modify: `frontend/routes/scaling/hpas.tsx` — Add `createHref`

The HPA wizard is 2 steps: Configure (target ref, replicas, metrics), Review.

- [ ] **Step 1: Create the wizard island**

Create `frontend/islands/HPAWizard.tsx`:

**Key form state:**
```typescript
interface HPAFormState {
  name: string;
  namespace: string;
  targetKind: "Deployment" | "StatefulSet" | "ReplicaSet";
  targetName: string;
  minReplicas: number;
  maxReplicas: number;
  metrics: HPAMetricState[];
}

interface HPAMetricState {
  type: "Resource";
  resourceName: "cpu" | "memory";
  targetType: "Utilization" | "AverageValue";
  targetAverageValue: number;
}
```

**Configure step:** Name, namespace, target kind dropdown (Deployment/StatefulSet/ReplicaSet), target name input, min/max replicas, metrics list (add/remove) with resource name dropdown + target utilization percentage.

- [ ] **Step 2: Create the route page**

```tsx
// frontend/routes/scaling/hpas/new.tsx
import { define } from "@/utils.ts";
import HPAWizard from "@/islands/HPAWizard.tsx";

export default define.page(function NewHPAPage() {
  return <HPAWizard />;
});
```

- [ ] **Step 3: Add createHref to the resource table page**

Modify `frontend/routes/scaling/hpas.tsx`:

```tsx
import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function HPAsPage() {
  return (
    <ResourceTable
      kind="hpas"
      title="HorizontalPodAutoscalers"
      createHref="/scaling/hpas/new"
    />
  );
});
```

- [ ] **Step 4: Verify frontend builds**

Run: `cd frontend && deno task build`
Expected: clean build

- [ ] **Step 5: Commit**

```bash
git add frontend/islands/HPAWizard.tsx frontend/routes/scaling/hpas/new.tsx frontend/routes/scaling/hpas.tsx
git commit -m "feat(wizard): add HPA creation wizard with metric configuration"
```

---

## Task 7: PDB Frontend Wizard

**Files:**
- Create: `frontend/islands/PDBWizard.tsx`
- Create: `frontend/routes/scaling/pdbs/new.tsx`
- Modify: `frontend/routes/scaling/pdbs.tsx` — Add `createHref`

The PDB wizard is 2 steps: Configure (selector, min/max), Review.

- [ ] **Step 1: Create the wizard island**

Create `frontend/islands/PDBWizard.tsx`:

**Key form state:**
```typescript
interface PDBFormState {
  name: string;
  namespace: string;
  selectorLabels: LabelEntry[];
  budgetType: "minAvailable" | "maxUnavailable";
  budgetValue: string;  // number or percentage like "50%"
}
```

**Configure step:** Name, namespace, pod selector label editor (add/remove key-value rows), radio toggle between minAvailable/maxUnavailable, value input (number or percentage).

- [ ] **Step 2: Create the route page**

```tsx
// frontend/routes/scaling/pdbs/new.tsx
import { define } from "@/utils.ts";
import PDBWizard from "@/islands/PDBWizard.tsx";

export default define.page(function NewPDBPage() {
  return <PDBWizard />;
});
```

- [ ] **Step 3: Add createHref to the resource table page**

Modify `frontend/routes/scaling/pdbs.tsx`:

```tsx
import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function PDBsPage() {
  return (
    <ResourceTable
      kind="poddisruptionbudgets"
      title="PodDisruptionBudgets"
      createHref="/scaling/pdbs/new"
    />
  );
});
```

- [ ] **Step 4: Verify frontend builds**

Run: `cd frontend && deno task build`
Expected: clean build

- [ ] **Step 5: Commit**

```bash
git add frontend/islands/PDBWizard.tsx frontend/routes/scaling/pdbs/new.tsx frontend/routes/scaling/pdbs.tsx
git commit -m "feat(wizard): add PDB creation wizard with minAvailable/maxUnavailable toggle"
```

---

## Task 8: Integration Verification

- [ ] **Step 1: Run all backend tests**

Run: `cd backend && go test ./... -race -count=1`
Expected: all PASS

- [ ] **Step 2: Run backend linting**

Run: `cd backend && go vet ./...`
Expected: clean

- [ ] **Step 3: Run frontend build**

Run: `cd frontend && deno task build`
Expected: clean build

- [ ] **Step 4: Run frontend lint**

Run: `cd frontend && deno lint && deno fmt --check`
Expected: clean

- [ ] **Step 5: Update CLAUDE.md**

Add the 3 new wizard preview endpoints to the API endpoints section, update file tree, and mark Phase 4D Batch 3 as complete.

- [ ] **Step 6: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for Phase 4D Batch 3 completion"
```
