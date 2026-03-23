package wizard

import (
	"strings"
	"testing"
)

func TestNetworkPolicyInputValidate(t *testing.T) {
	validIngress := []NetworkPolicyRuleInput{
		{
			From: []NetworkPolicyPeerInput{
				{PodSelector: map[string]string{"app": "frontend"}},
			},
			Ports: []NetworkPolicyPortInput{
				{Port: 8080, Protocol: "TCP"},
			},
		},
	}
	validEgress := []NetworkPolicyRuleInput{
		{
			To: []NetworkPolicyPeerInput{
				{NamespaceSelector: map[string]string{"env": "prod"}},
			},
			Ports: []NetworkPolicyPortInput{
				{Port: 443, Protocol: "TCP"},
			},
		},
	}

	tests := []struct {
		name       string
		input      NetworkPolicyInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid ingress only",
			input: NetworkPolicyInput{
				Name: "allow-frontend", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress:     validIngress,
			},
			wantErrors: 0,
		},
		{
			name: "valid egress only",
			input: NetworkPolicyInput{
				Name: "allow-egress", Namespace: "production",
				PolicyTypes: []string{"Egress"},
				Egress:      validEgress,
			},
			wantErrors: 0,
		},
		{
			name: "valid ingress and egress",
			input: NetworkPolicyInput{
				Name: "full-policy", Namespace: "staging",
				PolicyTypes: []string{"Ingress", "Egress"},
				Ingress:     validIngress,
				Egress:      validEgress,
			},
			wantErrors: 0,
		},
		{
			name: "valid empty pod selector (matches all pods)",
			input: NetworkPolicyInput{
				Name: "allow-all", Namespace: "default",
				PodSelector: map[string]string{},
				PolicyTypes: []string{"Ingress"},
			},
			wantErrors: 0,
		},
		{
			name: "valid ipblock peer",
			input: NetworkPolicyInput{
				Name: "allow-cidr", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{
						From: []NetworkPolicyPeerInput{
							{IPBlock: &IPBlockInput{CIDR: "10.0.0.0/8", Except: []string{"10.1.0.0/16"}}},
						},
					},
				},
			},
			wantErrors: 0,
		},
		{
			name: "valid sctp protocol",
			input: NetworkPolicyInput{
				Name: "sctp-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{
						Ports: []NetworkPolicyPortInput{
							{Port: 9000, Protocol: "SCTP"},
						},
					},
				},
			},
			wantErrors: 0,
		},
		// --- name errors ---
		{
			name: "missing name",
			input: NetworkPolicyInput{
				Namespace: "default", PolicyTypes: []string{"Ingress"},
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "invalid name uppercase",
			input: NetworkPolicyInput{
				Name: "MyPolicy", Namespace: "default", PolicyTypes: []string{"Ingress"},
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "invalid name starts with hyphen",
			input: NetworkPolicyInput{
				Name: "-bad", Namespace: "default", PolicyTypes: []string{"Ingress"},
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		// --- namespace errors ---
		{
			name: "missing namespace",
			input: NetworkPolicyInput{
				Name: "my-policy", PolicyTypes: []string{"Ingress"},
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name: "invalid namespace",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "INVALID", PolicyTypes: []string{"Ingress"},
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		// --- policyTypes errors ---
		{
			name: "missing policyTypes",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
			},
			wantErrors: 1, wantFields: []string{"policyTypes"},
		},
		{
			name: "invalid policyType value",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default", PolicyTypes: []string{"ingress"},
			},
			wantErrors: 1, wantFields: []string{"policyTypes[0]"},
		},
		{
			name: "invalid policyType unknown value",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default", PolicyTypes: []string{"Both"},
			},
			wantErrors: 1, wantFields: []string{"policyTypes[0]"},
		},
		// --- port errors ---
		{
			name: "invalid port zero",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{Ports: []NetworkPolicyPortInput{{Port: 0, Protocol: "TCP"}}},
				},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].ports[0].port"},
		},
		{
			name: "invalid port too high",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{Ports: []NetworkPolicyPortInput{{Port: 70000, Protocol: "TCP"}}},
				},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].ports[0].port"},
		},
		{
			name: "invalid protocol",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{Ports: []NetworkPolicyPortInput{{Port: 80, Protocol: "HTTP"}}},
				},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].ports[0].protocol"},
		},
		{
			name: "invalid protocol udp lowercase",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Egress"},
				Egress: []NetworkPolicyRuleInput{
					{Ports: []NetworkPolicyPortInput{{Port: 53, Protocol: "udp"}}},
				},
			},
			wantErrors: 1, wantFields: []string{"egress[0].ports[0].protocol"},
		},
		// --- CIDR errors ---
		{
			name: "invalid ipblock cidr",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{
						From: []NetworkPolicyPeerInput{
							{IPBlock: &IPBlockInput{CIDR: "not-a-cidr"}},
						},
					},
				},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].from[0].ipBlock.cidr"},
		},
		{
			name: "invalid ipblock cidr host address instead of network",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{
						From: []NetworkPolicyPeerInput{
							{IPBlock: &IPBlockInput{CIDR: "256.0.0.1/24"}},
						},
					},
				},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].from[0].ipBlock.cidr"},
		},
		{
			name: "invalid ipblock cidr has host bits set",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{
						From: []NetworkPolicyPeerInput{
							{IPBlock: &IPBlockInput{CIDR: "10.0.0.1/24"}},
						},
					},
				},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].from[0].ipBlock.cidr"},
		},
		{
			name: "invalid except cidr",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInput{
					{
						From: []NetworkPolicyPeerInput{
							{IPBlock: &IPBlockInput{CIDR: "10.0.0.0/8", Except: []string{"not-a-cidr"}}},
						},
					},
				},
			},
			wantErrors: 1, wantFields: []string{"ingress[0].from[0].ipBlock.except[0]"},
		},
		// --- egress peer errors ---
		{
			name: "invalid egress port",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Egress"},
				Egress: []NetworkPolicyRuleInput{
					{Ports: []NetworkPolicyPortInput{{Port: 99999, Protocol: "UDP"}}},
				},
			},
			wantErrors: 1, wantFields: []string{"egress[0].ports[0].port"},
		},
		{
			name: "invalid egress cidr",
			input: NetworkPolicyInput{
				Name: "my-policy", Namespace: "default",
				PolicyTypes: []string{"Egress"},
				Egress: []NetworkPolicyRuleInput{
					{
						To: []NetworkPolicyPeerInput{
							{IPBlock: &IPBlockInput{CIDR: "bad-cidr"}},
						},
					},
				},
			},
			wantErrors: 1, wantFields: []string{"egress[0].to[0].ipBlock.cidr"},
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
	t.Run("ingress only produces correct YAML", func(t *testing.T) {
		input := NetworkPolicyInput{
			Name:      "allow-web",
			Namespace: "production",
			PodSelector: map[string]string{
				"app": "backend",
			},
			PolicyTypes: []string{"Ingress"},
			Ingress: []NetworkPolicyRuleInput{
				{
					From: []NetworkPolicyPeerInput{
						{PodSelector: map[string]string{"role": "frontend"}},
					},
					Ports: []NetworkPolicyPortInput{
						{Port: 8080, Protocol: "TCP"},
					},
				},
			},
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "kind: NetworkPolicy") {
			t.Error("expected kind: NetworkPolicy")
		}
		if !strings.Contains(yaml, "apiVersion: networking.k8s.io/v1") {
			t.Error("expected apiVersion: networking.k8s.io/v1")
		}
		if !strings.Contains(yaml, "name: allow-web") {
			t.Error("expected name: allow-web")
		}
		if !strings.Contains(yaml, "namespace: production") {
			t.Error("expected namespace: production")
		}
		if !strings.Contains(yaml, "app: backend") {
			t.Error("expected app: backend in podSelector")
		}
		if !strings.Contains(yaml, "Ingress") {
			t.Error("expected Ingress in policyTypes")
		}
		if !strings.Contains(yaml, "8080") {
			t.Error("expected port 8080")
		}
	})

	t.Run("egress only produces correct YAML", func(t *testing.T) {
		input := NetworkPolicyInput{
			Name:        "deny-egress",
			Namespace:   "restricted",
			PolicyTypes: []string{"Egress"},
			Egress: []NetworkPolicyRuleInput{
				{
					To: []NetworkPolicyPeerInput{
						{NamespaceSelector: map[string]string{"kubernetes.io/metadata.name": "kube-system"}},
					},
					Ports: []NetworkPolicyPortInput{
						{Port: 53, Protocol: "UDP"},
					},
				},
			},
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "kind: NetworkPolicy") {
			t.Error("expected kind: NetworkPolicy")
		}
		if !strings.Contains(yaml, "Egress") {
			t.Error("expected Egress in policyTypes")
		}
		if !strings.Contains(yaml, "53") {
			t.Error("expected port 53")
		}
		if !strings.Contains(yaml, "UDP") {
			t.Error("expected protocol UDP")
		}
	})

	t.Run("ipblock produces correct YAML", func(t *testing.T) {
		input := NetworkPolicyInput{
			Name:        "allow-cidr",
			Namespace:   "default",
			PolicyTypes: []string{"Ingress"},
			Ingress: []NetworkPolicyRuleInput{
				{
					From: []NetworkPolicyPeerInput{
						{IPBlock: &IPBlockInput{
							CIDR:   "192.168.0.0/16",
							Except: []string{"192.168.1.0/24"},
						}},
					},
				},
			},
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "192.168.0.0/16") {
			t.Error("expected CIDR 192.168.0.0/16")
		}
		if !strings.Contains(yaml, "192.168.1.0/24") {
			t.Error("expected except CIDR 192.168.1.0/24")
		}
	})

	t.Run("empty pod selector matches all pods", func(t *testing.T) {
		input := NetworkPolicyInput{
			Name:        "match-all",
			Namespace:   "default",
			PolicyTypes: []string{"Ingress", "Egress"},
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "kind: NetworkPolicy") {
			t.Error("expected kind: NetworkPolicy")
		}
		// Empty matchLabels selector should be present
		if !strings.Contains(yaml, "podSelector:") {
			t.Error("expected podSelector in YAML")
		}
	})
}
