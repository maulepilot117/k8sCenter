package wizard

import (
	"fmt"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// toKyvernoYAML generates a Kyverno ClusterPolicy YAML document.
func (p *PolicyWizardInput) toKyvernoYAML() (string, error) {
	tmpl := policyTemplates[p.TemplateID]

	description := p.Description
	if description == "" {
		description = tmpl.Description
	}

	policy := map[string]any{
		"apiVersion": "kyverno.io/v1",
		"kind":       "ClusterPolicy",
		"metadata": map[string]any{
			"name": p.Name,
			"annotations": map[string]any{
				"policies.kyverno.io/title":       tmpl.Name,
				"policies.kyverno.io/category":    tmpl.Category,
				"policies.kyverno.io/severity":    tmpl.Severity,
				"policies.kyverno.io/subject":     strings.Join(p.TargetKinds, ", "),
				"policies.kyverno.io/description": description,
			},
		},
		"spec": map[string]any{
			"validationFailureAction": p.Action,
			"background":              true,
			"rules":                   p.kyvernoRules(),
		},
	}

	yamlBytes, err := sigsyaml.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("marshal kyverno policy: %w", err)
	}
	return string(yamlBytes), nil
}

// kyvernoRules builds the spec.rules array based on the template.
func (p *PolicyWizardInput) kyvernoRules() []map[string]any {
	switch p.TemplateID {
	case "disallow-privileged":
		return []map[string]any{
			p.kyvernoValidateRule("deny-privileged",
				"Privileged containers are not allowed.",
				p.kyvernoContainerPattern(map[string]any{"privileged": "!true"})),
		}

	case "disallow-root":
		return []map[string]any{
			p.kyvernoValidateRule("require-run-as-non-root",
				"Containers must run as non-root. Set runAsNonRoot to true.",
				p.kyvernoContainerPattern(map[string]any{"runAsNonRoot": true})),
		}

	case "disallow-privilege-escalation":
		return []map[string]any{
			p.kyvernoValidateRule("deny-privilege-escalation",
				"Privilege escalation is not allowed. Set allowPrivilegeEscalation to false.",
				p.kyvernoContainerPattern(map[string]any{"allowPrivilegeEscalation": "!true"})),
		}

	case "restrict-capabilities":
		params := p.parseCapabilityParams()
		caps := map[string]any{}
		if params.DropAll {
			caps["drop"] = []string{"ALL"}
		}
		if len(params.AllowedAdd) > 0 {
			caps["add"] = params.AllowedAdd
		}
		msg := "Capabilities must be restricted."
		if len(params.AllowedAdd) > 0 {
			msg = fmt.Sprintf("Capabilities must be restricted. Only allowed additions: %s.", strings.Join(params.AllowedAdd, ", "))
		}
		return []map[string]any{
			p.kyvernoValidateRule("restrict-capabilities", msg,
				p.kyvernoContainerPattern(map[string]any{"capabilities": caps})),
		}

	case "allowed-registries":
		params := p.parseRegistryParams()
		patterns := make([]string, len(params.Registries))
		for i, reg := range params.Registries {
			if strings.HasSuffix(reg, "/") {
				patterns[i] = reg + "*"
			} else {
				patterns[i] = reg + "/*"
			}
		}
		imagePattern := strings.Join(patterns, " | ")
		containerEntry := map[string]any{"image": imagePattern}
		return []map[string]any{
			p.kyvernoValidateRule("validate-image-registries",
				"Images must come from an allowed registry.",
				map[string]any{
					"spec": map[string]any{
						"containers":             []map[string]any{containerEntry},
						"=(initContainers)":      []map[string]any{containerEntry},
						"=(ephemeralContainers)": []map[string]any{containerEntry},
					},
				}),
		}

	case "disallow-latest-tag":
		containerEntry := map[string]any{"image": "!*:latest & *:*"}
		return []map[string]any{
			p.kyvernoValidateRule("validate-image-tag",
				"Images must have an explicit tag; ':latest' is not allowed.",
				map[string]any{
					"spec": map[string]any{
						"containers":             []map[string]any{containerEntry},
						"=(initContainers)":      []map[string]any{containerEntry},
						"=(ephemeralContainers)": []map[string]any{containerEntry},
					},
				}),
		}

	case "require-resource-limits":
		params := p.parseResourceLimitParams()
		limits := map[string]any{}
		if params.RequireCPU {
			limits["cpu"] = "?*"
		}
		if params.RequireMemory {
			limits["memory"] = "?*"
		}
		return []map[string]any{
			p.kyvernoValidateRule("validate-resource-limits",
				"CPU and memory resource limits are required.",
				map[string]any{
					"spec": map[string]any{
						"containers": []map[string]any{{
							"resources": map[string]any{
								"limits": limits,
							},
						}},
					},
				}),
		}

	case "require-labels":
		params := p.parseRequireLabelsParams()
		labels := map[string]any{}
		for _, key := range params.Labels {
			labels[key] = "?*"
		}
		return []map[string]any{
			p.kyvernoValidateRule("check-for-labels",
				fmt.Sprintf("Resources must have the following labels: %s.", strings.Join(params.Labels, ", ")),
				map[string]any{
					"metadata": map[string]any{
						"labels": labels,
					},
				}),
		}

	default:
		return nil
	}
}

// kyvernoValidateRule builds a complete rule with match, exclude, and validate blocks.
func (p *PolicyWizardInput) kyvernoValidateRule(name, message string, pattern map[string]any) map[string]any {
	rule := map[string]any{
		"name": name,
		"match": map[string]any{
			"any": []map[string]any{{
				"resources": map[string]any{
					"kinds": p.TargetKinds,
				},
			}},
		},
		"validate": map[string]any{
			"message": message,
			"pattern": pattern,
		},
	}

	if len(p.ExcludedNamespaces) > 0 {
		rule["exclude"] = map[string]any{
			"any": []map[string]any{{
				"resources": map[string]any{
					"namespaces": p.ExcludedNamespaces,
				},
			}},
		}
	}

	return rule
}

// kyvernoContainerPattern builds a pattern that checks all container types
// for a given securityContext field.
func (p *PolicyWizardInput) kyvernoContainerPattern(securityContext map[string]any) map[string]any {
	entry := map[string]any{"securityContext": securityContext}
	return map[string]any{
		"spec": map[string]any{
			"containers":             []map[string]any{entry},
			"=(initContainers)":      []map[string]any{entry},
			"=(ephemeralContainers)": []map[string]any{entry},
		},
	}
}
