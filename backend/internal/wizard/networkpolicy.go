package wizard

import (
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	sigsyaml "sigs.k8s.io/yaml"
)

// NetworkPolicyInput represents the wizard form data for creating a NetworkPolicy.
type NetworkPolicyInput struct {
	Name        string                   `json:"name"`
	Namespace   string                   `json:"namespace"`
	PodSelector map[string]string        `json:"podSelector"`
	PolicyTypes []string                 `json:"policyTypes"`
	Ingress     []NetworkPolicyRuleInput `json:"ingress,omitempty"`
	Egress      []NetworkPolicyRuleInput `json:"egress,omitempty"`
}

// NetworkPolicyRuleInput represents a single ingress or egress rule.
type NetworkPolicyRuleInput struct {
	From  []NetworkPolicyPeerInput `json:"from,omitempty"`
	To    []NetworkPolicyPeerInput `json:"to,omitempty"`
	Ports []NetworkPolicyPortInput `json:"ports,omitempty"`
}

// NetworkPolicyPeerInput represents a network peer (pod selector, namespace selector, or IP block).
type NetworkPolicyPeerInput struct {
	PodSelector       map[string]string `json:"podSelector,omitempty"`
	NamespaceSelector map[string]string `json:"namespaceSelector,omitempty"`
	IPBlock           *IPBlockInput     `json:"ipBlock,omitempty"`
}

// IPBlockInput represents an IP block with an optional exception list.
type IPBlockInput struct {
	CIDR   string   `json:"cidr"`
	Except []string `json:"except,omitempty"`
}

// NetworkPolicyPortInput represents a port/protocol pair for a network policy rule.
type NetworkPolicyPortInput struct {
	Port     int32  `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// validPolicyTypes is the set of allowed NetworkPolicy policy types.
var validPolicyTypes = map[string]bool{
	"Ingress": true,
	"Egress":  true,
}

// validNetworkProtocols is the set of allowed protocols for NetworkPolicy ports.
var validNetworkProtocols = map[string]bool{
	"TCP":  true,
	"UDP":  true,
	"SCTP": true,
}

// Validate checks the NetworkPolicyInput and returns field-level errors.
func (np *NetworkPolicyInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(np.Name) {
		errs = append(errs, FieldError{
			Field:   "name",
			Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)",
		})
	}
	if np.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(np.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if len(np.PolicyTypes) == 0 {
		errs = append(errs, FieldError{Field: "policyTypes", Message: "at least one policy type is required"})
	}
	for i, pt := range np.PolicyTypes {
		if !validPolicyTypes[pt] {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("policyTypes[%d]", i),
				Message: "must be Ingress or Egress",
			})
		}
	}

	for i, rule := range np.Ingress {
		errs = append(errs, validateNetworkPolicyRule(fmt.Sprintf("ingress[%d]", i), rule, true)...)
	}
	for i, rule := range np.Egress {
		errs = append(errs, validateNetworkPolicyRule(fmt.Sprintf("egress[%d]", i), rule, false)...)
	}

	return errs
}

// validateNetworkPolicyRule validates a single ingress or egress rule.
// isIngress controls whether to check "from" (ingress) or "to" (egress) peers.
func validateNetworkPolicyRule(prefix string, rule NetworkPolicyRuleInput, isIngress bool) []FieldError {
	var errs []FieldError

	if isIngress {
		for i, peer := range rule.From {
			errs = append(errs, validateNetworkPolicyPeer(fmt.Sprintf("%s.from[%d]", prefix, i), peer)...)
		}
	} else {
		for i, peer := range rule.To {
			errs = append(errs, validateNetworkPolicyPeer(fmt.Sprintf("%s.to[%d]", prefix, i), peer)...)
		}
	}

	for i, port := range rule.Ports {
		errs = append(errs, validateNetworkPolicyPort(fmt.Sprintf("%s.ports[%d]", prefix, i), port)...)
	}

	return errs
}

// validateNetworkPolicyPeer validates a single peer entry.
func validateNetworkPolicyPeer(prefix string, peer NetworkPolicyPeerInput) []FieldError {
	var errs []FieldError

	if peer.IPBlock != nil {
		if _, _, err := net.ParseCIDR(peer.IPBlock.CIDR); err != nil {
			errs = append(errs, FieldError{
				Field:   prefix + ".ipBlock.cidr",
				Message: "must be a valid CIDR (e.g. 10.0.0.0/8)",
			})
		}
		for j, except := range peer.IPBlock.Except {
			if _, _, err := net.ParseCIDR(except); err != nil {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("%s.ipBlock.except[%d]", prefix, j),
					Message: "must be a valid CIDR",
				})
			}
		}
	}

	return errs
}

// validateNetworkPolicyPort validates a single port/protocol entry.
func validateNetworkPolicyPort(prefix string, port NetworkPolicyPortInput) []FieldError {
	var errs []FieldError

	if port.Port < 1 || port.Port > 65535 {
		errs = append(errs, FieldError{
			Field:   prefix + ".port",
			Message: "must be between 1 and 65535",
		})
	}

	if port.Protocol != "" && !validNetworkProtocols[port.Protocol] {
		errs = append(errs, FieldError{
			Field:   prefix + ".protocol",
			Message: "must be TCP, UDP, or SCTP",
		})
	}

	return errs
}

// ToNetworkPolicy converts the wizard input to a typed Kubernetes NetworkPolicy.
func (np *NetworkPolicyInput) ToNetworkPolicy() *networkingv1.NetworkPolicy {
	policy := &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "NetworkPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      np.Name,
			Namespace: np.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: np.PodSelector,
			},
		},
	}

	// Policy types
	for _, pt := range np.PolicyTypes {
		policy.Spec.PolicyTypes = append(policy.Spec.PolicyTypes, networkingv1.PolicyType(pt))
	}

	// Ingress rules
	for _, rule := range np.Ingress {
		ingressRule := networkingv1.NetworkPolicyIngressRule{}
		for _, peer := range rule.From {
			ingressRule.From = append(ingressRule.From, buildNetworkPolicyPeer(peer))
		}
		for _, port := range rule.Ports {
			ingressRule.Ports = append(ingressRule.Ports, buildNetworkPolicyPort(port))
		}
		policy.Spec.Ingress = append(policy.Spec.Ingress, ingressRule)
	}

	// Egress rules
	for _, rule := range np.Egress {
		egressRule := networkingv1.NetworkPolicyEgressRule{}
		for _, peer := range rule.To {
			egressRule.To = append(egressRule.To, buildNetworkPolicyPeer(peer))
		}
		for _, port := range rule.Ports {
			egressRule.Ports = append(egressRule.Ports, buildNetworkPolicyPort(port))
		}
		policy.Spec.Egress = append(policy.Spec.Egress, egressRule)
	}

	return policy
}

// buildNetworkPolicyPeer converts a NetworkPolicyPeerInput to a typed NetworkPolicyPeer.
func buildNetworkPolicyPeer(peer NetworkPolicyPeerInput) networkingv1.NetworkPolicyPeer {
	result := networkingv1.NetworkPolicyPeer{}

	if peer.PodSelector != nil {
		result.PodSelector = &metav1.LabelSelector{
			MatchLabels: peer.PodSelector,
		}
	}
	if peer.NamespaceSelector != nil {
		result.NamespaceSelector = &metav1.LabelSelector{
			MatchLabels: peer.NamespaceSelector,
		}
	}
	if peer.IPBlock != nil {
		result.IPBlock = &networkingv1.IPBlock{
			CIDR:   peer.IPBlock.CIDR,
			Except: peer.IPBlock.Except,
		}
	}

	return result
}

// buildNetworkPolicyPort converts a NetworkPolicyPortInput to a typed NetworkPolicyPort.
func buildNetworkPolicyPort(port NetworkPolicyPortInput) networkingv1.NetworkPolicyPort {
	result := networkingv1.NetworkPolicyPort{}

	if port.Port != 0 {
		portVal := intstr.FromInt32(port.Port)
		result.Port = &portVal
	}

	if port.Protocol != "" {
		var proto corev1.Protocol
		switch port.Protocol {
		case "UDP":
			proto = corev1.ProtocolUDP
		case "SCTP":
			proto = corev1.ProtocolSCTP
		default:
			proto = corev1.ProtocolTCP
		}
		result.Protocol = &proto
	}

	return result
}

// ToYAML implements WizardInput by converting to a NetworkPolicy and marshaling to YAML.
func (np *NetworkPolicyInput) ToYAML() (string, error) {
	policy := np.ToNetworkPolicy()
	yamlBytes, err := sigsyaml.Marshal(policy)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
