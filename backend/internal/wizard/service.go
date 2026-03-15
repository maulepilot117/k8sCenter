package wizard

import (
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// portNameRegex validates IANA service names as required by k8s for named ports.
// Must be lowercase alphanumeric and hyphens, max 15 chars, at least one letter,
// cannot start or end with hyphen.
var portNameRegex = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,13}[a-z0-9])?$`)

// ServiceInput represents the wizard form data for creating a Service.
type ServiceInput struct {
	Name      string             `json:"name"`
	Namespace string             `json:"namespace"`
	Type      string             `json:"type"`
	Labels    map[string]string  `json:"labels,omitempty"`
	Selector  map[string]string  `json:"selector"`
	Ports     []ServicePortInput `json:"ports"`
}

// ServicePortInput represents a Service port mapping.
type ServicePortInput struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	TargetPort int32  `json:"targetPort"`
	Protocol   string `json:"protocol,omitempty"`
	NodePort   int32  `json:"nodePort,omitempty"`
}

// Validate checks the ServiceInput and returns field-level errors.
func (s *ServiceInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(s.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if s.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	// Validate label/map sizes
	if len(s.Labels) > 50 {
		errs = append(errs, FieldError{Field: "labels", Message: "must have 50 or fewer entries"})
	}
	for k, v := range s.Labels {
		if len(k) > 253 {
			errs = append(errs, FieldError{Field: "labels", Message: fmt.Sprintf("key %q exceeds 253 characters", k)})
		}
		if len(v) > 63 {
			errs = append(errs, FieldError{Field: "labels", Message: fmt.Sprintf("value for key %q exceeds 63 characters", k)})
		}
	}
	if len(s.Selector) > 20 {
		errs = append(errs, FieldError{Field: "selector", Message: "must have 20 or fewer entries"})
	}
	if len(s.Ports) > 100 {
		errs = append(errs, FieldError{Field: "ports", Message: "must have 100 or fewer entries"})
	}

	validTypes := map[string]bool{"ClusterIP": true, "NodePort": true, "LoadBalancer": true}
	if !validTypes[s.Type] {
		errs = append(errs, FieldError{Field: "type", Message: "must be ClusterIP, NodePort, or LoadBalancer"})
	}

	if len(s.Selector) == 0 {
		errs = append(errs, FieldError{Field: "selector", Message: "must have at least one key-value pair"})
	}

	if len(s.Ports) == 0 {
		errs = append(errs, FieldError{Field: "ports", Message: "at least one port is required"})
	}

	seenPorts := make(map[int32]bool)
	for i, p := range s.Ports {
		if p.Name != "" && !portNameRegex.MatchString(p.Name) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].name", i),
				Message: "must be a valid IANA service name (lowercase alphanumeric and hyphens, max 15 chars, at least one letter)",
			})
		}
		if seenPorts[p.Port] {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].port", i),
				Message: fmt.Sprintf("duplicate port %d", p.Port),
			})
		}
		seenPorts[p.Port] = true
		if p.Port < 1 || p.Port > 65535 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].port", i),
				Message: "must be between 1 and 65535",
			})
		}
		if p.TargetPort < 1 || p.TargetPort > 65535 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].targetPort", i),
				Message: "must be between 1 and 65535",
			})
		}
		if p.Protocol != "" && p.Protocol != "TCP" && p.Protocol != "UDP" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].protocol", i),
				Message: "must be TCP or UDP",
			})
		}
		if p.NodePort != 0 {
			if s.Type != "NodePort" && s.Type != "LoadBalancer" {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("ports[%d].nodePort", i),
					Message: "can only be set for NodePort or LoadBalancer services",
				})
			} else if p.NodePort < 30000 || p.NodePort > 32767 {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("ports[%d].nodePort", i),
					Message: "must be between 30000 and 32767",
				})
			}
		}
	}

	return errs
}

// ToService converts the wizard input to a typed Kubernetes Service.
func (s *ServiceInput) ToService() *corev1.Service {
	lbls := s.Labels
	if lbls == nil {
		lbls = make(map[string]string)
	}
	if _, ok := lbls["app"]; !ok {
		lbls["app"] = s.Name
	}

	var ports []corev1.ServicePort
	for _, p := range s.Ports {
		proto := corev1.ProtocolTCP
		if p.Protocol == "UDP" {
			proto = corev1.ProtocolUDP
		}
		sp := corev1.ServicePort{
			Port:       p.Port,
			TargetPort: intstr.FromInt32(p.TargetPort),
			Protocol:   proto,
		}
		if p.Name != "" {
			sp.Name = p.Name
		}
		if p.NodePort != 0 {
			sp.NodePort = p.NodePort
		}
		ports = append(ports, sp)
	}

	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels:    lbls,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceType(s.Type),
			Selector: s.Selector,
			Ports:    ports,
		},
	}
}
