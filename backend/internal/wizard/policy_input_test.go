package wizard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyWizardInput_Validate(t *testing.T) {
	tests := []struct {
		name      string
		input     PolicyWizardInput
		wantField string // expected error field (empty = no error expected)
	}{
		{
			name:      "unknown template",
			input:     PolicyWizardInput{TemplateID: "nonexistent", Engine: "kyverno", Name: "test", Action: "Audit"},
			wantField: "templateId",
		},
		{
			name:      "invalid engine",
			input:     PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "invalid", Name: "test", Action: "Audit", TargetKinds: []string{"Pod"}},
			wantField: "engine",
		},
		{
			name:      "empty name",
			input:     PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "kyverno", Name: "", Action: "Audit", TargetKinds: []string{"Pod"}},
			wantField: "name",
		},
		{
			name:      "invalid DNS name",
			input:     PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "kyverno", Name: "INVALID", Action: "Audit", TargetKinds: []string{"Pod"}},
			wantField: "name",
		},
		{
			name:      "invalid kyverno action",
			input:     PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "kyverno", Name: "test", Action: "deny", TargetKinds: []string{"Pod"}},
			wantField: "action",
		},
		{
			name:      "invalid gatekeeper action",
			input:     PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "gatekeeper", Name: "test", Action: "Enforce", TargetKinds: []string{"Pod"}},
			wantField: "action",
		},
		{
			name:      "empty target kinds",
			input:     PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "kyverno", Name: "test", Action: "Audit", TargetKinds: []string{}},
			wantField: "targetKinds",
		},
		{
			name:  "valid disallow-privileged kyverno",
			input: PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "kyverno", Name: "test-policy", Action: "Enforce", TargetKinds: []string{"Pod"}},
		},
		{
			name:  "valid disallow-privileged gatekeeper",
			input: PolicyWizardInput{TemplateID: "disallow-privileged", Engine: "gatekeeper", Name: "test-policy", Action: "deny", TargetKinds: []string{"Pod"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if tt.wantField == "" {
				if len(errs) > 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}
			found := false
			for _, e := range errs {
				if e.Field == tt.wantField {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected error on field %q, got errors: %v", tt.wantField, errs)
			}
		})
	}
}

func TestPolicyWizardInput_ValidateParams(t *testing.T) {
	tests := []struct {
		name      string
		input     PolicyWizardInput
		wantField string
	}{
		{
			name: "allowed-registries requires registries",
			input: PolicyWizardInput{
				TemplateID: "allowed-registries", Engine: "kyverno", Name: "test", Action: "Enforce",
				TargetKinds: []string{"Pod"},
			},
			wantField: "params.registries",
		},
		{
			name: "allowed-registries empty list",
			input: PolicyWizardInput{
				TemplateID: "allowed-registries", Engine: "kyverno", Name: "test", Action: "Enforce",
				TargetKinds: []string{"Pod"},
				Params:      mustJSON(RegistryParams{Registries: []string{}}),
			},
			wantField: "params.registries",
		},
		{
			name: "require-resource-limits needs at least one",
			input: PolicyWizardInput{
				TemplateID: "require-resource-limits", Engine: "kyverno", Name: "test", Action: "Audit",
				TargetKinds: []string{"Pod"},
				Params:      mustJSON(ResourceLimitParams{RequireCPU: false, RequireMemory: false}),
			},
			wantField: "params",
		},
		{
			name: "require-labels requires labels",
			input: PolicyWizardInput{
				TemplateID: "require-labels", Engine: "kyverno", Name: "test", Action: "Audit",
				TargetKinds: []string{"Pod"},
			},
			wantField: "params.labels",
		},
		{
			name: "restrict-capabilities invalid cap name",
			input: PolicyWizardInput{
				TemplateID: "restrict-capabilities", Engine: "kyverno", Name: "test", Action: "Audit",
				TargetKinds: []string{"Pod"},
				Params:      mustJSON(CapabilityParams{DropAll: true, AllowedAdd: []string{"lowercase"}}),
			},
			wantField: "params.allowedAdd[0]",
		},
		{
			name: "valid allowed-registries",
			input: PolicyWizardInput{
				TemplateID: "allowed-registries", Engine: "kyverno", Name: "test", Action: "Enforce",
				TargetKinds: []string{"Pod"},
				Params:      mustJSON(RegistryParams{Registries: []string{"ghcr.io/", "registry.k8s.io/"}}),
			},
		},
		{
			name: "valid require-labels",
			input: PolicyWizardInput{
				TemplateID: "require-labels", Engine: "kyverno", Name: "test", Action: "Audit",
				TargetKinds: []string{"Pod"},
				Params:      mustJSON(RequireLabelsParams{Labels: []string{"app", "env"}}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if tt.wantField == "" {
				if len(errs) > 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}
			found := false
			for _, e := range errs {
				if e.Field == tt.wantField {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected error on field %q, got errors: %v", tt.wantField, errs)
			}
		})
	}
}

func TestPolicyWizardInput_KyvernoYAML(t *testing.T) {
	templates := []struct {
		name   string
		input  PolicyWizardInput
		checks []string // substrings that must appear in the YAML
	}{
		{
			name: "disallow-privileged",
			input: PolicyWizardInput{
				TemplateID: "disallow-privileged", Engine: "kyverno", Name: "deny-priv",
				Action: "Enforce", TargetKinds: []string{"Pod"},
				ExcludedNamespaces: []string{"kube-system"},
			},
			checks: []string{
				"apiVersion: kyverno.io/v1",
				"kind: ClusterPolicy",
				"name: deny-priv",
				"validationFailureAction: Enforce",
				"privileged: '!true'",
				"policies.kyverno.io/severity: high",
				"kube-system",
			},
		},
		{
			name: "disallow-root",
			input: PolicyWizardInput{
				TemplateID: "disallow-root", Engine: "kyverno", Name: "no-root",
				Action: "Audit", TargetKinds: []string{"Pod"},
			},
			checks: []string{
				"kind: ClusterPolicy",
				"runAsNonRoot: true",
			},
		},
		{
			name: "disallow-privilege-escalation",
			input: PolicyWizardInput{
				TemplateID: "disallow-privilege-escalation", Engine: "kyverno", Name: "no-escalation",
				Action: "Enforce", TargetKinds: []string{"Pod"},
			},
			checks: []string{
				"kind: ClusterPolicy",
				"allowPrivilegeEscalation: '!true'",
			},
		},
		{
			name: "restrict-capabilities",
			input: PolicyWizardInput{
				TemplateID: "restrict-capabilities", Engine: "kyverno", Name: "restrict-caps",
				Action: "Audit", TargetKinds: []string{"Pod"},
				Params: mustJSON(CapabilityParams{DropAll: true, AllowedAdd: []string{"NET_BIND_SERVICE"}}),
			},
			checks: []string{
				"kind: ClusterPolicy",
				"- ALL",
				"- NET_BIND_SERVICE",
			},
		},
		{
			name: "allowed-registries",
			input: PolicyWizardInput{
				TemplateID: "allowed-registries", Engine: "kyverno", Name: "allowed-regs",
				Action: "Enforce", TargetKinds: []string{"Pod"},
				Params: mustJSON(RegistryParams{Registries: []string{"ghcr.io/", "registry.k8s.io/"}}),
			},
			checks: []string{
				"kind: ClusterPolicy",
				"ghcr.io/*",
				"registry.k8s.io/*",
			},
		},
		{
			name: "disallow-latest-tag",
			input: PolicyWizardInput{
				TemplateID: "disallow-latest-tag", Engine: "kyverno", Name: "no-latest",
				Action: "Audit", TargetKinds: []string{"Pod"},
			},
			checks: []string{
				"kind: ClusterPolicy",
				"!*:latest",
			},
		},
		{
			name: "require-resource-limits",
			input: PolicyWizardInput{
				TemplateID: "require-resource-limits", Engine: "kyverno", Name: "require-limits",
				Action: "Audit", TargetKinds: []string{"Pod"},
				Params: mustJSON(ResourceLimitParams{RequireCPU: true, RequireMemory: true}),
			},
			checks: []string{
				"kind: ClusterPolicy",
				"cpu: ?*",
				"memory: ?*",
			},
		},
		{
			name: "require-labels",
			input: PolicyWizardInput{
				TemplateID: "require-labels", Engine: "kyverno", Name: "require-app-labels",
				Action: "Audit", TargetKinds: []string{"Pod", "Deployment"},
				Params: mustJSON(RequireLabelsParams{Labels: []string{"app.kubernetes.io/name", "owner"}}),
			},
			checks: []string{
				"kind: ClusterPolicy",
				"app.kubernetes.io/name",
				"owner",
			},
		},
	}

	for _, tt := range templates {
		t.Run(tt.name, func(t *testing.T) {
			yaml, err := tt.input.ToYAML()
			if err != nil {
				t.Fatalf("ToYAML() error: %v", err)
			}
			for _, check := range tt.checks {
				if !strings.Contains(yaml, check) {
					t.Errorf("YAML missing %q:\n%s", check, yaml)
				}
			}
		})
	}
}

func TestPolicyWizardInput_GatekeeperYAML(t *testing.T) {
	templates := []struct {
		name   string
		input  PolicyWizardInput
		checks []string
	}{
		{
			name: "disallow-privileged",
			input: PolicyWizardInput{
				TemplateID: "disallow-privileged", Engine: "gatekeeper", Name: "deny-priv",
				Action: "deny", TargetKinds: []string{"Pod"},
				ExcludedNamespaces: []string{"kube-system"},
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sDisallowPrivileged",
				"enforcementAction: deny",
				"---",
				"rego:",
				"kube-system",
			},
		},
		{
			name: "disallow-root",
			input: PolicyWizardInput{
				TemplateID: "disallow-root", Engine: "gatekeeper", Name: "no-root",
				Action: "dryrun", TargetKinds: []string{"Pod"},
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sDisallowRoot",
				"enforcementAction: dryrun",
				"runAsNonRoot",
			},
		},
		{
			name: "restrict-capabilities with params",
			input: PolicyWizardInput{
				TemplateID: "restrict-capabilities", Engine: "gatekeeper", Name: "restrict-caps",
				Action: "deny", TargetKinds: []string{"Pod"},
				Params: mustJSON(CapabilityParams{DropAll: true, AllowedAdd: []string{"NET_BIND_SERVICE"}}),
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sRestrictCapabilities",
				"allowedCapabilities",
				"NET_BIND_SERVICE",
			},
		},
		{
			name: "allowed-registries with params",
			input: PolicyWizardInput{
				TemplateID: "allowed-registries", Engine: "gatekeeper", Name: "allowed-regs",
				Action: "deny", TargetKinds: []string{"Pod"},
				Params: mustJSON(RegistryParams{Registries: []string{"ghcr.io/", "registry.k8s.io/"}}),
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sAllowedRegistries",
				"registries",
				"ghcr.io/",
			},
		},
		{
			name: "disallow-latest-tag",
			input: PolicyWizardInput{
				TemplateID: "disallow-latest-tag", Engine: "gatekeeper", Name: "no-latest",
				Action: "warn", TargetKinds: []string{"Pod"},
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sDisallowLatestTag",
				"enforcementAction: warn",
			},
		},
		{
			name: "require-resource-limits",
			input: PolicyWizardInput{
				TemplateID: "require-resource-limits", Engine: "gatekeeper", Name: "req-limits",
				Action: "deny", TargetKinds: []string{"Pod"},
				Params: mustJSON(ResourceLimitParams{RequireCPU: true, RequireMemory: true}),
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sRequireResourceLimits",
				"requireCpu: true",
				"requireMemory: true",
			},
		},
		{
			name: "require-labels",
			input: PolicyWizardInput{
				TemplateID: "require-labels", Engine: "gatekeeper", Name: "req-labels",
				Action: "deny", TargetKinds: []string{"Pod", "Deployment"},
				Params: mustJSON(RequireLabelsParams{Labels: []string{"app", "env"}}),
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sRequireLabels",
				"- app",
				"- env",
			},
		},
		{
			name: "disallow-privilege-escalation",
			input: PolicyWizardInput{
				TemplateID: "disallow-privilege-escalation", Engine: "gatekeeper", Name: "no-escal",
				Action: "deny", TargetKinds: []string{"Pod"},
			},
			checks: []string{
				"kind: ConstraintTemplate",
				"kind: K8sDisallowPrivilegeEscalation",
				"allowPrivilegeEscalation",
			},
		},
	}

	for _, tt := range templates {
		t.Run(tt.name, func(t *testing.T) {
			yaml, err := tt.input.ToYAML()
			if err != nil {
				t.Fatalf("ToYAML() error: %v", err)
			}
			for _, check := range tt.checks {
				if !strings.Contains(yaml, check) {
					t.Errorf("YAML missing %q:\n%s", check, yaml)
				}
			}
		})
	}
}

// mustJSON marshals v to json.RawMessage, panicking on error.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
