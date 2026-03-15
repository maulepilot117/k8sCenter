package wizard

import (
	"fmt"
	"regexp"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DeploymentInput represents the wizard form data for creating a Deployment.
type DeploymentInput struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Image     string            `json:"image"`
	Replicas  int32             `json:"replicas"`
	Labels    map[string]string `json:"labels,omitempty"`
	Ports     []PortInput       `json:"ports,omitempty"`
	EnvVars   []EnvVarInput     `json:"envVars,omitempty"`
	Resources *ResourcesInput   `json:"resources,omitempty"`
	Probes    *ProbesInput      `json:"probes,omitempty"`
	Strategy  *StrategyInput    `json:"strategy,omitempty"`
}

// PortInput represents a container port.
type PortInput struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int32  `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// EnvVarInput represents an environment variable (literal, configmap ref, or secret ref).
type EnvVarInput struct {
	Name         string `json:"name"`
	Value        string `json:"value,omitempty"`
	ConfigMapRef string `json:"configMapRef,omitempty"`
	SecretRef    string `json:"secretRef,omitempty"`
	Key          string `json:"key,omitempty"`
}

// ResourcesInput represents CPU/memory requests and limits.
type ResourcesInput struct {
	RequestCPU    string `json:"requestCpu,omitempty"`
	RequestMemory string `json:"requestMemory,omitempty"`
	LimitCPU      string `json:"limitCpu,omitempty"`
	LimitMemory   string `json:"limitMemory,omitempty"`
}

// ProbesInput holds liveness and readiness probe configurations.
type ProbesInput struct {
	Liveness  *ProbeInput `json:"liveness,omitempty"`
	Readiness *ProbeInput `json:"readiness,omitempty"`
}

// ProbeInput represents a health probe (HTTP GET or TCP socket).
type ProbeInput struct {
	Type                string `json:"type"`
	Path                string `json:"path,omitempty"`
	Port                int32  `json:"port"`
	InitialDelaySeconds int32  `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int32  `json:"periodSeconds,omitempty"`
}

// StrategyInput represents the deployment update strategy.
type StrategyInput struct {
	Type           string `json:"type,omitempty"`
	MaxSurge       string `json:"maxSurge,omitempty"`
	MaxUnavailable string `json:"maxUnavailable,omitempty"`
}

// dnsLabelRegex validates RFC 1123 DNS labels (used for k8s names).
var dnsLabelRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// envVarNameRegex validates k8s environment variable names.
var envVarNameRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate checks the DeploymentInput and returns field-level errors.
func (d *DeploymentInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(d.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if d.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(d.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}
	if d.Image == "" {
		errs = append(errs, FieldError{Field: "image", Message: "is required"})
	} else if len(d.Image) > 512 {
		errs = append(errs, FieldError{Field: "image", Message: "must be 512 characters or less"})
	}
	if d.Replicas < 0 || d.Replicas > 1000 {
		errs = append(errs, FieldError{Field: "replicas", Message: "must be between 0 and 1000"})
	}

	// Validate label/map sizes
	if len(d.Labels) > 50 {
		errs = append(errs, FieldError{Field: "labels", Message: "must have 50 or fewer entries"})
	}
	for k, v := range d.Labels {
		if len(k) > 253 {
			errs = append(errs, FieldError{Field: "labels", Message: fmt.Sprintf("key %q exceeds 253 characters", k)})
		}
		if len(v) > 63 {
			errs = append(errs, FieldError{Field: "labels", Message: fmt.Sprintf("value for key %q exceeds 63 characters", k)})
		}
	}
	if len(d.Ports) > 20 {
		errs = append(errs, FieldError{Field: "ports", Message: "must have 20 or fewer entries"})
	}
	if len(d.EnvVars) > 100 {
		errs = append(errs, FieldError{Field: "envVars", Message: "must have 100 or fewer entries"})
	}

	// Validate ports
	seenPorts := make(map[int32]bool)
	for i, p := range d.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].containerPort", i),
				Message: "must be between 1 and 65535",
			})
		}
		if seenPorts[p.ContainerPort] {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].containerPort", i),
				Message: fmt.Sprintf("duplicate port %d", p.ContainerPort),
			})
		}
		seenPorts[p.ContainerPort] = true

		if p.Protocol != "" && p.Protocol != "TCP" && p.Protocol != "UDP" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("ports[%d].protocol", i),
				Message: "must be TCP or UDP",
			})
		}
	}

	// Validate env vars
	for i, e := range d.EnvVars {
		if !envVarNameRegex.MatchString(e.Name) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("envVars[%d].name", i),
				Message: "must start with a letter or underscore and contain only alphanumeric characters and underscores",
			})
		}
		if e.ConfigMapRef != "" && e.SecretRef != "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("envVars[%d]", i),
				Message: "cannot have both configMapRef and secretRef",
			})
		}
		hasRef := e.ConfigMapRef != "" || e.SecretRef != ""
		if !hasRef && e.Value == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("envVars[%d]", i),
				Message: "must have a value, configMapRef, or secretRef",
			})
		}
		if hasRef && e.Key == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("envVars[%d].key", i),
				Message: "is required when using configMapRef or secretRef",
			})
		}
	}

	// Validate resources
	if d.Resources != nil {
		errs = append(errs, validateQuantity("resources.requestCpu", d.Resources.RequestCPU)...)
		errs = append(errs, validateQuantity("resources.requestMemory", d.Resources.RequestMemory)...)
		errs = append(errs, validateQuantity("resources.limitCpu", d.Resources.LimitCPU)...)
		errs = append(errs, validateQuantity("resources.limitMemory", d.Resources.LimitMemory)...)
	}

	// Validate probes
	if d.Probes != nil {
		if d.Probes.Liveness != nil {
			errs = append(errs, validateProbe("probes.liveness", d.Probes.Liveness)...)
		}
		if d.Probes.Readiness != nil {
			errs = append(errs, validateProbe("probes.readiness", d.Probes.Readiness)...)
		}
	}

	// Validate strategy
	if d.Strategy != nil && d.Strategy.Type != "" {
		if d.Strategy.Type != "RollingUpdate" && d.Strategy.Type != "Recreate" {
			errs = append(errs, FieldError{
				Field:   "strategy.type",
				Message: "must be RollingUpdate or Recreate",
			})
		}
	}

	return errs
}

// ToDeployment converts the wizard input to a typed Kubernetes Deployment.
// Validate() should be called before ToDeployment() to ensure inputs are well-formed.
func (d *DeploymentInput) ToDeployment() (*appsv1.Deployment, error) {
	lbls := d.Labels
	if lbls == nil {
		lbls = make(map[string]string)
	}
	if _, ok := lbls["app"]; !ok {
		lbls["app"] = d.Name
	}

	container := corev1.Container{
		Name:  d.Name,
		Image: d.Image,
	}

	// Ports
	for _, p := range d.Ports {
		proto := corev1.ProtocolTCP
		if p.Protocol == "UDP" {
			proto = corev1.ProtocolUDP
		}
		cp := corev1.ContainerPort{
			ContainerPort: p.ContainerPort,
			Protocol:      proto,
		}
		if p.Name != "" {
			cp.Name = p.Name
		}
		container.Ports = append(container.Ports, cp)
	}

	// Env vars
	for _, e := range d.EnvVars {
		ev := corev1.EnvVar{Name: e.Name}
		switch {
		case e.ConfigMapRef != "":
			ev.ValueFrom = &corev1.EnvVarSource{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: e.ConfigMapRef},
					Key:                  e.Key,
				},
			}
		case e.SecretRef != "":
			ev.ValueFrom = &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: e.SecretRef},
					Key:                  e.Key,
				},
			}
		default:
			ev.Value = e.Value
		}
		container.Env = append(container.Env, ev)
	}

	// Resources
	if d.Resources != nil {
		reqs := corev1.ResourceList{}
		lims := corev1.ResourceList{}
		if d.Resources.RequestCPU != "" {
			q, err := resource.ParseQuantity(d.Resources.RequestCPU)
			if err != nil {
				return nil, fmt.Errorf("invalid CPU request %q: %w", d.Resources.RequestCPU, err)
			}
			reqs[corev1.ResourceCPU] = q
		}
		if d.Resources.RequestMemory != "" {
			q, err := resource.ParseQuantity(d.Resources.RequestMemory)
			if err != nil {
				return nil, fmt.Errorf("invalid memory request %q: %w", d.Resources.RequestMemory, err)
			}
			reqs[corev1.ResourceMemory] = q
		}
		if d.Resources.LimitCPU != "" {
			q, err := resource.ParseQuantity(d.Resources.LimitCPU)
			if err != nil {
				return nil, fmt.Errorf("invalid CPU limit %q: %w", d.Resources.LimitCPU, err)
			}
			lims[corev1.ResourceCPU] = q
		}
		if d.Resources.LimitMemory != "" {
			q, err := resource.ParseQuantity(d.Resources.LimitMemory)
			if err != nil {
				return nil, fmt.Errorf("invalid memory limit %q: %w", d.Resources.LimitMemory, err)
			}
			lims[corev1.ResourceMemory] = q
		}
		if len(reqs) > 0 || len(lims) > 0 {
			container.Resources = corev1.ResourceRequirements{}
			if len(reqs) > 0 {
				container.Resources.Requests = reqs
			}
			if len(lims) > 0 {
				container.Resources.Limits = lims
			}
		}
	}

	// Probes
	if d.Probes != nil {
		if d.Probes.Liveness != nil {
			container.LivenessProbe = buildProbe(d.Probes.Liveness)
		}
		if d.Probes.Readiness != nil {
			container.ReadinessProbe = buildProbe(d.Probes.Readiness)
		}
	}

	replicas := d.Replicas
	if replicas == 0 {
		replicas = 1 // Default to 1 replica for creation wizards
	}
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.Namespace,
			Labels:    lbls,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: lbls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: lbls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}

	// Strategy
	if d.Strategy != nil && d.Strategy.Type != "" {
		dep.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.DeploymentStrategyType(d.Strategy.Type),
		}
		if d.Strategy.Type == "RollingUpdate" {
			ru := &appsv1.RollingUpdateDeployment{}
			if d.Strategy.MaxSurge != "" {
				v := intstr.Parse(d.Strategy.MaxSurge)
				ru.MaxSurge = &v
			}
			if d.Strategy.MaxUnavailable != "" {
				v := intstr.Parse(d.Strategy.MaxUnavailable)
				ru.MaxUnavailable = &v
			}
			dep.Spec.Strategy.RollingUpdate = ru
		}
	}

	return dep, nil
}

func buildProbe(p *ProbeInput) *corev1.Probe {
	probe := &corev1.Probe{
		InitialDelaySeconds: p.InitialDelaySeconds,
		PeriodSeconds:       p.PeriodSeconds,
	}
	switch p.Type {
	case "http":
		probe.ProbeHandler = corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: p.Path,
				Port: intstr.FromInt32(p.Port),
			},
		}
	case "tcp":
		probe.ProbeHandler = corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt32(p.Port),
			},
		}
	}
	return probe
}

func validateProbe(prefix string, p *ProbeInput) []FieldError {
	var errs []FieldError
	if p.Type != "http" && p.Type != "tcp" {
		errs = append(errs, FieldError{
			Field:   prefix + ".type",
			Message: "must be http or tcp",
		})
	}
	if p.Port < 1 || p.Port > 65535 {
		errs = append(errs, FieldError{
			Field:   prefix + ".port",
			Message: "must be between 1 and 65535",
		})
	}
	if p.Type == "http" && p.Path == "" {
		errs = append(errs, FieldError{
			Field:   prefix + ".path",
			Message: "is required for HTTP probes",
		})
	}
	if p.Type == "http" && p.Path != "" && p.Path[0] != '/' {
		errs = append(errs, FieldError{
			Field:   prefix + ".path",
			Message: "must start with /",
		})
	}
	if p.Type == "http" && len(p.Path) > 1024 {
		errs = append(errs, FieldError{
			Field:   prefix + ".path",
			Message: "must be 1024 characters or fewer",
		})
	}
	return errs
}

func validateQuantity(field, value string) []FieldError {
	if value == "" {
		return nil
	}
	if _, err := resource.ParseQuantity(value); err != nil {
		return []FieldError{{
			Field:   field,
			Message: "invalid resource quantity (e.g. use 100m, 128Mi, 1Gi)",
		}}
	}
	return nil
}
