package wizard

import (
	"embed"
	"fmt"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

//go:embed rego/*.rego
var regoFS embed.FS

// gatekeeperTemplateInfo maps template IDs to their Gatekeeper CRD names and Rego file.
var gatekeeperTemplateInfo = map[string]struct {
	Kind     string // CRD Kind for the Constraint (e.g. K8sDisallowPrivileged)
	RegoFile string // filename in rego/ directory
}{
	"disallow-privileged":          {Kind: "K8sDisallowPrivileged", RegoFile: "disallow_privileged.rego"},
	"disallow-root":                {Kind: "K8sDisallowRoot", RegoFile: "disallow_root.rego"},
	"disallow-privilege-escalation": {Kind: "K8sDisallowPrivilegeEscalation", RegoFile: "disallow_privilege_escalation.rego"},
	"restrict-capabilities":        {Kind: "K8sRestrictCapabilities", RegoFile: "restrict_capabilities.rego"},
	"allowed-registries":           {Kind: "K8sAllowedRegistries", RegoFile: "allowed_registries.rego"},
	"disallow-latest-tag":          {Kind: "K8sDisallowLatestTag", RegoFile: "disallow_latest_tag.rego"},
	"require-resource-limits":      {Kind: "K8sRequireResourceLimits", RegoFile: "require_resource_limits.rego"},
	"require-labels":               {Kind: "K8sRequireLabels", RegoFile: "require_labels.rego"},
}

// toGatekeeperYAML generates a multi-document YAML with ConstraintTemplate + Constraint.
func (p *PolicyWizardInput) toGatekeeperYAML() (string, error) {
	info, ok := gatekeeperTemplateInfo[p.TemplateID]
	if !ok {
		return "", fmt.Errorf("no gatekeeper template for: %s", p.TemplateID)
	}

	rego, err := regoFS.ReadFile("rego/" + info.RegoFile)
	if err != nil {
		return "", fmt.Errorf("read rego file %s: %w", info.RegoFile, err)
	}

	// Build ConstraintTemplate
	ctName := strings.ToLower(info.Kind)
	ct := p.buildConstraintTemplate(ctName, info.Kind, string(rego))

	// Build Constraint
	constraint := p.buildConstraint(info.Kind)

	ctYAML, err := sigsyaml.Marshal(ct)
	if err != nil {
		return "", fmt.Errorf("marshal constraint template: %w", err)
	}

	constraintYAML, err := sigsyaml.Marshal(constraint)
	if err != nil {
		return "", fmt.Errorf("marshal constraint: %w", err)
	}

	return string(ctYAML) + "---\n" + string(constraintYAML), nil
}

// buildConstraintTemplate creates the ConstraintTemplate resource.
func (p *PolicyWizardInput) buildConstraintTemplate(name, kind, rego string) map[string]any {
	tmpl := policyTemplates[p.TemplateID]

	ct := map[string]any{
		"apiVersion": "templates.gatekeeper.sh/v1",
		"kind":       "ConstraintTemplate",
		"metadata": map[string]any{
			"name": name,
			"annotations": map[string]any{
				"description": tmpl.Description,
			},
		},
		"spec": map[string]any{
			"crd": map[string]any{
				"spec": map[string]any{
					"names": map[string]any{
						"kind": kind,
					},
				},
			},
			"targets": []map[string]any{{
				"target": "admission.k8s.gatekeeper.sh",
				"rego":   rego,
			}},
		},
	}

	// Add parameter schema if the template has parameters
	schema := p.gatekeeperParamSchema()
	if schema != nil {
		crdSpec := ct["spec"].(map[string]any)["crd"].(map[string]any)["spec"].(map[string]any)
		crdSpec["validation"] = map[string]any{
			"openAPIV3Schema": schema,
		}
	}

	return ct
}

// buildConstraint creates the Constraint resource that instantiates the template.
func (p *PolicyWizardInput) buildConstraint(kind string) map[string]any {
	constraint := map[string]any{
		"apiVersion": "constraints.gatekeeper.sh/v1beta1",
		"kind":       kind,
		"metadata": map[string]any{
			"name": p.Name,
		},
		"spec": map[string]any{
			"enforcementAction": p.Action,
			"match": map[string]any{
				"kinds": p.gatekeeperMatchKinds(),
			},
		},
	}

	if len(p.ExcludedNamespaces) > 0 {
		constraint["spec"].(map[string]any)["match"].(map[string]any)["excludedNamespaces"] = p.ExcludedNamespaces
	}

	params := p.gatekeeperParams()
	if params != nil {
		constraint["spec"].(map[string]any)["parameters"] = params
	}

	return constraint
}

// gatekeeperMatchKinds builds the match.kinds array for a Constraint.
func (p *PolicyWizardInput) gatekeeperMatchKinds() []map[string]any {
	// Determine the apiGroup based on the target kinds
	apiGroups := []string{""}
	for _, kind := range p.TargetKinds {
		switch kind {
		case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
			apiGroups = []string{"apps"}
		case "Job", "CronJob":
			apiGroups = []string{"batch"}
		}
	}

	return []map[string]any{{
		"apiGroups": apiGroups,
		"kinds":     p.TargetKinds,
	}}
}

// gatekeeperParamSchema returns the OpenAPI v3 schema for template-specific parameters.
func (p *PolicyWizardInput) gatekeeperParamSchema() map[string]any {
	switch p.TemplateID {
	case "restrict-capabilities":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"allowedCapabilities": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
		}
	case "allowed-registries":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"registries": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
		}
	case "require-resource-limits":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"requireCpu": map[string]any{
					"type": "boolean",
				},
				"requireMemory": map[string]any{
					"type": "boolean",
				},
			},
		}
	case "require-labels":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"labels": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
		}
	default:
		return nil
	}
}

// gatekeeperParams returns the Constraint spec.parameters for template-specific values.
func (p *PolicyWizardInput) gatekeeperParams() map[string]any {
	switch p.TemplateID {
	case "restrict-capabilities":
		params := p.parseCapabilityParams()
		return map[string]any{
			"allowedCapabilities": params.AllowedAdd,
		}
	case "allowed-registries":
		params := p.parseRegistryParams()
		return map[string]any{
			"registries": params.Registries,
		}
	case "require-resource-limits":
		params := p.parseResourceLimitParams()
		return map[string]any{
			"requireCpu":    params.RequireCPU,
			"requireMemory": params.RequireMemory,
		}
	case "require-labels":
		params := p.parseRequireLabelsParams()
		return map[string]any{
			"labels": params.Labels,
		}
	default:
		return nil
	}
}
