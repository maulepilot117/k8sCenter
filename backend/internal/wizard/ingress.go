package wizard

import (
	"fmt"
	"regexp"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// hostRegex validates an optional hostname (RFC 952 / RFC 1123 with dots).
// Empty host is allowed (matches all hosts).
var hostRegex = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validPathTypes is the set of allowed Ingress path types.
var validPathTypes = map[string]bool{
	"Prefix":                   true,
	"Exact":                    true,
	"ImplementationSpecific":   true,
}

// IngressInput represents the wizard form data for creating an Ingress.
type IngressInput struct {
	Name             string       `json:"name"`
	Namespace        string       `json:"namespace"`
	IngressClassName *string      `json:"ingressClassName,omitempty"`
	Rules            []IngressRule `json:"rules"`
	TLS              []IngressTLS `json:"tls,omitempty"`
}

// IngressRule represents one host rule with its paths.
type IngressRule struct {
	Host  string        `json:"host"`
	Paths []IngressPath `json:"paths"`
}

// IngressPath represents a single path entry in an Ingress rule.
type IngressPath struct {
	Path        string `json:"path"`
	PathType    string `json:"pathType"`
	ServiceName string `json:"serviceName"`
	ServicePort int32  `json:"servicePort"`
}

// IngressTLS represents TLS configuration for a set of hosts.
type IngressTLS struct {
	Hosts      []string `json:"hosts"`
	SecretName string   `json:"secretName"`
}

// Validate checks the IngressInput and returns field-level errors.
func (in *IngressInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(in.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if in.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(in.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if in.IngressClassName != nil && *in.IngressClassName != "" {
		if !dnsLabelRegex.MatchString(*in.IngressClassName) {
			errs = append(errs, FieldError{Field: "ingressClassName", Message: "must be a valid DNS label"})
		}
	}

	if len(in.Rules) == 0 {
		errs = append(errs, FieldError{Field: "rules", Message: "at least one rule is required"})
	}
	if len(in.Rules) > 50 {
		errs = append(errs, FieldError{Field: "rules", Message: "must have 50 or fewer rules"})
	}

	for i, rule := range in.Rules {
		if rule.Host != "" && !hostRegex.MatchString(strings.ToLower(rule.Host)) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("rules[%d].host", i),
				Message: "must be a valid hostname",
			})
		}

		if len(rule.Paths) == 0 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("rules[%d].paths", i),
				Message: "at least one path is required per rule",
			})
		}
		if len(rule.Paths) > 100 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("rules[%d].paths", i),
				Message: "must have 100 or fewer paths per rule",
			})
		}

		for j, p := range rule.Paths {
			if !strings.HasPrefix(p.Path, "/") {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("rules[%d].paths[%d].path", i, j),
					Message: "must start with /",
				})
			}
			if !validPathTypes[p.PathType] {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("rules[%d].paths[%d].pathType", i, j),
					Message: "must be Prefix, Exact, or ImplementationSpecific",
				})
			}
			if p.ServiceName == "" {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("rules[%d].paths[%d].serviceName", i, j),
					Message: "is required",
				})
			} else if !dnsLabelRegex.MatchString(p.ServiceName) {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("rules[%d].paths[%d].serviceName", i, j),
					Message: "must be a valid DNS label",
				})
			}
			if p.ServicePort < 1 || p.ServicePort > 65535 {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("rules[%d].paths[%d].servicePort", i, j),
					Message: "must be between 1 and 65535",
				})
			}
		}
	}

	for i, tls := range in.TLS {
		if len(tls.Hosts) == 0 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("tls[%d].hosts", i),
				Message: "at least one host is required",
			})
		}
		if tls.SecretName == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("tls[%d].secretName", i),
				Message: "is required",
			})
		} else if !dnsLabelRegex.MatchString(tls.SecretName) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("tls[%d].secretName", i),
				Message: "must be a valid DNS label",
			})
		}
	}

	return errs
}

// ToIngress converts the wizard input to a typed Kubernetes Ingress.
func (in *IngressInput) ToIngress() *networkingv1.Ingress {
	var rules []networkingv1.IngressRule
	for _, r := range in.Rules {
		var paths []networkingv1.HTTPIngressPath
		for _, p := range r.Paths {
			pathType := networkingv1.PathType(p.PathType)
			paths = append(paths, networkingv1.HTTPIngressPath{
				Path:     p.Path,
				PathType: &pathType,
				Backend: networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{
						Name: p.ServiceName,
						Port: networkingv1.ServiceBackendPort{
							Number: p.ServicePort,
						},
					},
				},
			})
		}
		rules = append(rules, networkingv1.IngressRule{
			Host: r.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: paths,
				},
			},
		})
	}

	var tlsSpec []networkingv1.IngressTLS
	for _, t := range in.TLS {
		tlsSpec = append(tlsSpec, networkingv1.IngressTLS{
			Hosts:      t.Hosts,
			SecretName: t.SecretName,
		})
	}

	ing := &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      in.Name,
			Namespace: in.Namespace,
		},
		Spec: networkingv1.IngressSpec{
			Rules: rules,
		},
	}

	if in.IngressClassName != nil && *in.IngressClassName != "" {
		ing.Spec.IngressClassName = in.IngressClassName
	}

	if len(tlsSpec) > 0 {
		ing.Spec.TLS = tlsSpec
	}

	return ing
}

// ToYAML implements WizardInput by converting to an Ingress and marshaling to YAML.
func (in *IngressInput) ToYAML() (string, error) {
	ing := in.ToIngress()
	yamlBytes, err := sigsyaml.Marshal(ing)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
