package wizard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// PolicyTemplate defines metadata for a pre-built policy template.
type PolicyTemplate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Severity    string   `json:"severity"`
	TargetKinds []string `json:"targetKinds"`
	Engines     []string `json:"engines"`
}

// policyTemplates is the registry of available policy templates.
var policyTemplates = map[string]PolicyTemplate{
	"disallow-privileged": {
		ID:          "disallow-privileged",
		Name:        "Disallow Privileged Containers",
		Category:    "Pod Security",
		Description: "Privileged mode disables most security mechanisms and must not be allowed.",
		Severity:    "high",
		TargetKinds: []string{"Pod"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
	"disallow-root": {
		ID:          "disallow-root",
		Name:        "Disallow Root User",
		Category:    "Pod Security",
		Description: "Containers must run as a non-root user.",
		Severity:    "high",
		TargetKinds: []string{"Pod"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
	"disallow-privilege-escalation": {
		ID:          "disallow-privilege-escalation",
		Name:        "Disallow Privilege Escalation",
		Category:    "Pod Security",
		Description: "Containers must not allow privilege escalation via setuid or setgid.",
		Severity:    "high",
		TargetKinds: []string{"Pod"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
	"restrict-capabilities": {
		ID:          "restrict-capabilities",
		Name:        "Restrict Capabilities",
		Category:    "Pod Security",
		Description: "Drop all capabilities and allow only specific additions.",
		Severity:    "medium",
		TargetKinds: []string{"Pod"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
	"allowed-registries": {
		ID:          "allowed-registries",
		Name:        "Restrict Image Registries",
		Category:    "Image Policies",
		Description: "Images must come from approved container registries.",
		Severity:    "high",
		TargetKinds: []string{"Pod"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
	"disallow-latest-tag": {
		ID:          "disallow-latest-tag",
		Name:        "Disallow Latest Tag",
		Category:    "Image Policies",
		Description: "Images must have an explicit tag; ':latest' is not allowed.",
		Severity:    "medium",
		TargetKinds: []string{"Pod"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
	"require-resource-limits": {
		ID:          "require-resource-limits",
		Name:        "Require Resource Limits",
		Category:    "Resource Management",
		Description: "All containers must define CPU and memory resource limits.",
		Severity:    "medium",
		TargetKinds: []string{"Pod"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
	"require-labels": {
		ID:          "require-labels",
		Name:        "Require Labels",
		Category:    "Labeling",
		Description: "Resources must have specified labels for ownership and lifecycle tracking.",
		Severity:    "medium",
		TargetKinds: []string{"Pod", "Deployment", "StatefulSet", "DaemonSet"},
		Engines:     []string{"kyverno", "gatekeeper"},
	},
}

// validTargetKinds restricts the resource kinds that can be targeted by policy templates.
var validTargetKinds = map[string]bool{
	"Pod": true, "Deployment": true, "StatefulSet": true, "DaemonSet": true,
	"ReplicaSet": true, "Job": true, "CronJob": true,
}

// registryPatternRegex validates container registry prefixes (hostname/path format).
var registryPatternRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

// labelKeyRegex validates Kubernetes label keys (optional prefix/name).
var labelKeyRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?/)?[a-zA-Z0-9]([a-zA-Z0-9._-]{0,61}[a-zA-Z0-9])?$`)

// maxDescription is the maximum length for a policy description annotation.
const maxDescription = 1024

// maxExcludedNamespaces is the maximum number of excluded namespaces.
const maxExcludedNamespaces = 100

// validPolicyEngines is the set of supported policy engines.
var validPolicyEngines = map[string]bool{
	"kyverno":    true,
	"gatekeeper": true,
}

// validKyvernoActions are allowed Kyverno enforcement actions.
var validKyvernoActions = map[string]bool{
	"Audit":   true,
	"Enforce": true,
}

// validGatekeeperActions are allowed Gatekeeper enforcement actions.
var validGatekeeperActions = map[string]bool{
	"deny":   true,
	"dryrun": true,
	"warn":   true,
}

// Per-template parameter structs.

// CapabilityParams configures the restrict-capabilities template.
type CapabilityParams struct {
	DropAll    bool     `json:"dropAll"`
	AllowedAdd []string `json:"allowedAdd"`
}

// RegistryParams configures the allowed-registries template.
type RegistryParams struct {
	Registries []string `json:"registries"`
}

// ResourceLimitParams configures the require-resource-limits template.
type ResourceLimitParams struct {
	RequireCPU    bool `json:"requireCpu"`
	RequireMemory bool `json:"requireMemory"`
}

// RequireLabelsParams configures the require-labels template.
type RequireLabelsParams struct {
	Labels []string `json:"labels"`
}

// PolicyWizardInput represents the wizard form data for creating a policy.
type PolicyWizardInput struct {
	TemplateID         string          `json:"templateId"`
	Engine             string          `json:"engine"`
	Name               string          `json:"name"`
	Action             string          `json:"action"`
	TargetKinds        []string        `json:"targetKinds"`
	ExcludedNamespaces []string        `json:"excludedNamespaces"`
	Description        string          `json:"description"`
	Params             json.RawMessage `json:"params"`
}

// Validate checks the PolicyWizardInput and returns field-level errors.
func (p *PolicyWizardInput) Validate() []FieldError {
	var errs []FieldError

	// Template ID
	tmpl, ok := policyTemplates[p.TemplateID]
	if !ok {
		errs = append(errs, FieldError{Field: "templateId", Message: "unknown policy template"})
		return errs // can't validate further without a valid template
	}

	// Engine
	if !validPolicyEngines[p.Engine] {
		errs = append(errs, FieldError{Field: "engine", Message: "must be 'kyverno' or 'gatekeeper'"})
	} else {
		// Verify the template supports this engine
		supported := false
		for _, e := range tmpl.Engines {
			if e == p.Engine {
				supported = true
				break
			}
		}
		if !supported {
			errs = append(errs, FieldError{Field: "engine", Message: fmt.Sprintf("template '%s' does not support engine '%s'", p.TemplateID, p.Engine)})
		}
	}

	// Name
	if p.Name == "" {
		errs = append(errs, FieldError{Field: "name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(p.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	// Action
	if p.Engine == "kyverno" && !validKyvernoActions[p.Action] {
		errs = append(errs, FieldError{Field: "action", Message: "must be 'Audit' or 'Enforce'"})
	}
	if p.Engine == "gatekeeper" && !validGatekeeperActions[p.Action] {
		errs = append(errs, FieldError{Field: "action", Message: "must be 'deny', 'dryrun', or 'warn'"})
	}

	// Target kinds
	if len(p.TargetKinds) == 0 {
		errs = append(errs, FieldError{Field: "targetKinds", Message: "at least one target kind is required"})
	} else {
		for i, kind := range p.TargetKinds {
			if !validTargetKinds[kind] {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("targetKinds[%d]", i),
					Message: fmt.Sprintf("unsupported target kind %q", kind),
				})
			}
		}
	}

	// Description length
	if len(p.Description) > maxDescription {
		errs = append(errs, FieldError{Field: "description", Message: fmt.Sprintf("must be %d characters or fewer", maxDescription)})
	}

	// Excluded namespaces (optional, but validate format and count)
	if len(p.ExcludedNamespaces) > maxExcludedNamespaces {
		errs = append(errs, FieldError{Field: "excludedNamespaces", Message: fmt.Sprintf("must have %d or fewer entries", maxExcludedNamespaces)})
	}
	for i, ns := range p.ExcludedNamespaces {
		if ns != "" && !dnsLabelRegex.MatchString(ns) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("excludedNamespaces[%d]", i),
				Message: "must be a valid DNS label",
			})
		}
	}

	// Template-specific parameter validation
	errs = append(errs, p.validateParams()...)

	return errs
}

// validateParams validates template-specific parameters.
func (p *PolicyWizardInput) validateParams() []FieldError {
	switch p.TemplateID {
	case "disallow-privileged", "disallow-root", "disallow-privilege-escalation", "disallow-latest-tag":
		// No template-specific params
		return nil

	case "restrict-capabilities":
		var params CapabilityParams
		if len(p.Params) > 0 {
			if err := json.Unmarshal(p.Params, &params); err != nil {
				return []FieldError{{Field: "params", Message: "invalid capability parameters"}}
			}
		}
		// Validate capability names (uppercase Linux capability names, not "ALL")
		for i, cap := range params.AllowedAdd {
			if cap == "ALL" {
				return []FieldError{{
					Field:   fmt.Sprintf("params.allowedAdd[%d]", i),
					Message: "'ALL' cannot be added — it would negate dropping all capabilities",
				}}
			}
			if cap != strings.ToUpper(cap) || cap == "" {
				return []FieldError{{
					Field:   fmt.Sprintf("params.allowedAdd[%d]", i),
					Message: "must be an uppercase Linux capability name (e.g. NET_BIND_SERVICE)",
				}}
			}
		}
		return nil

	case "allowed-registries":
		var params RegistryParams
		if len(p.Params) == 0 {
			return []FieldError{{Field: "params.registries", Message: "at least one registry is required"}}
		}
		if err := json.Unmarshal(p.Params, &params); err != nil {
			return []FieldError{{Field: "params", Message: "invalid registry parameters"}}
		}
		if len(params.Registries) == 0 {
			return []FieldError{{Field: "params.registries", Message: "at least one registry is required"}}
		}
		for i, reg := range params.Registries {
			if reg == "" {
				return []FieldError{{
					Field:   fmt.Sprintf("params.registries[%d]", i),
					Message: "registry pattern must not be empty",
				}}
			}
			if !registryPatternRegex.MatchString(reg) {
				return []FieldError{{
					Field:   fmt.Sprintf("params.registries[%d]", i),
					Message: "must be a valid registry prefix (e.g. ghcr.io/ or registry.k8s.io/myorg)",
				}}
			}
		}
		return nil

	case "require-resource-limits":
		var params ResourceLimitParams
		if len(p.Params) > 0 {
			if err := json.Unmarshal(p.Params, &params); err != nil {
				return []FieldError{{Field: "params", Message: "invalid resource limit parameters"}}
			}
		}
		if !params.RequireCPU && !params.RequireMemory {
			return []FieldError{{Field: "params", Message: "at least one of requireCpu or requireMemory must be true"}}
		}
		return nil

	case "require-labels":
		var params RequireLabelsParams
		if len(p.Params) == 0 {
			return []FieldError{{Field: "params.labels", Message: "at least one label is required"}}
		}
		if err := json.Unmarshal(p.Params, &params); err != nil {
			return []FieldError{{Field: "params", Message: "invalid label parameters"}}
		}
		if len(params.Labels) == 0 {
			return []FieldError{{Field: "params.labels", Message: "at least one label is required"}}
		}
		for i, label := range params.Labels {
			if label == "" {
				return []FieldError{{
					Field:   fmt.Sprintf("params.labels[%d]", i),
					Message: "label key must not be empty",
				}}
			}
			if !labelKeyRegex.MatchString(label) {
				return []FieldError{{
					Field:   fmt.Sprintf("params.labels[%d]", i),
					Message: "must be a valid Kubernetes label key (e.g. app.kubernetes.io/name)",
				}}
			}
		}
		return nil

	default:
		return []FieldError{{Field: "templateId", Message: "unknown policy template"}}
	}
}

// parseParams unmarshals the raw Params field into the given default value.
// Validate() must be called before this to ensure the JSON is well-formed.
func parseParams[T any](raw json.RawMessage, defaults T) T {
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &defaults)
	}
	return defaults
}

func (p *PolicyWizardInput) parseCapabilityParams() CapabilityParams {
	return parseParams(p.Params, CapabilityParams{DropAll: true})
}

func (p *PolicyWizardInput) parseRegistryParams() RegistryParams {
	return parseParams(p.Params, RegistryParams{})
}

func (p *PolicyWizardInput) parseResourceLimitParams() ResourceLimitParams {
	return parseParams(p.Params, ResourceLimitParams{RequireCPU: true, RequireMemory: true})
}

func (p *PolicyWizardInput) parseRequireLabelsParams() RequireLabelsParams {
	return parseParams(p.Params, RequireLabelsParams{})
}

// ToYAML generates policy YAML for the selected engine.
func (p *PolicyWizardInput) ToYAML() (string, error) {
	switch p.Engine {
	case "kyverno":
		return p.toKyvernoYAML()
	case "gatekeeper":
		return p.toGatekeeperYAML()
	default:
		return "", fmt.Errorf("unsupported engine: %s", p.Engine)
	}
}
