package wizard

import (
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// dnsLabelRegex validates RFC 1123 DNS labels (used for k8s names).
var dnsLabelRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// envVarNameRegex validates k8s environment variable names.
var envVarNameRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// cronRegex validates basic 5-field cron expressions (min hour dom month dow).
// Allows *, digits, ranges (1-5), steps (*/2), lists (1,3,5), and common shortcuts.
var cronRegex = regexp.MustCompile(`^(\S+\s+){4}\S+$`)

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

// VolumeMountInput represents a volume mount for a container.
type VolumeMountInput struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// ContainerInput represents the shared container configuration used by workload wizards.
type ContainerInput struct {
	Image        string           `json:"image"`
	Command      []string         `json:"command,omitempty"`
	Args         []string         `json:"args,omitempty"`
	Ports        []PortInput      `json:"ports,omitempty"`
	EnvVars      []EnvVarInput    `json:"envVars,omitempty"`
	Resources    *ResourcesInput  `json:"resources,omitempty"`
	Probes       *ProbesInput     `json:"probes,omitempty"`
	VolumeMounts []VolumeMountInput `json:"volumeMounts,omitempty"`
}

// ValidateContainer checks the container fields and returns field-level errors.
// The prefix is prepended to field names (e.g. prefix "container" gives "container.image").
// Pass an empty string for no prefix.
func (c *ContainerInput) ValidateContainer(prefix string) []FieldError {
	var errs []FieldError
	p := prefix
	if p != "" {
		p += "."
	}

	if c.Image == "" {
		errs = append(errs, FieldError{Field: p + "image", Message: "is required"})
	} else if len(c.Image) > 512 {
		errs = append(errs, FieldError{Field: p + "image", Message: "must be 512 characters or less"})
	}

	if len(c.Ports) > 20 {
		errs = append(errs, FieldError{Field: p + "ports", Message: "must have 20 or fewer entries"})
	}
	if len(c.EnvVars) > 100 {
		errs = append(errs, FieldError{Field: p + "envVars", Message: "must have 100 or fewer entries"})
	}

	// Validate ports
	seenPorts := make(map[int32]bool)
	for i, port := range c.Ports {
		if port.ContainerPort < 1 || port.ContainerPort > 65535 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%sports[%d].containerPort", p, i),
				Message: "must be between 1 and 65535",
			})
		}
		if seenPorts[port.ContainerPort] {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%sports[%d].containerPort", p, i),
				Message: fmt.Sprintf("duplicate port %d", port.ContainerPort),
			})
		}
		seenPorts[port.ContainerPort] = true

		if port.Protocol != "" && port.Protocol != "TCP" && port.Protocol != "UDP" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%sports[%d].protocol", p, i),
				Message: "must be TCP or UDP",
			})
		}
	}

	// Validate env vars
	for i, e := range c.EnvVars {
		if !envVarNameRegex.MatchString(e.Name) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%senvVars[%d].name", p, i),
				Message: "must start with a letter or underscore and contain only alphanumeric characters and underscores",
			})
		}
		if e.ConfigMapRef != "" && e.SecretRef != "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%senvVars[%d]", p, i),
				Message: "cannot have both configMapRef and secretRef",
			})
		}
		hasRef := e.ConfigMapRef != "" || e.SecretRef != ""
		if !hasRef && e.Value == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%senvVars[%d]", p, i),
				Message: "must have a value, configMapRef, or secretRef",
			})
		}
		if hasRef && e.Key == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%senvVars[%d].key", p, i),
				Message: "is required when using configMapRef or secretRef",
			})
		}
	}

	// Validate resources
	if c.Resources != nil {
		errs = append(errs, validateQuantity(p+"resources.requestCpu", c.Resources.RequestCPU)...)
		errs = append(errs, validateQuantity(p+"resources.requestMemory", c.Resources.RequestMemory)...)
		errs = append(errs, validateQuantity(p+"resources.limitCpu", c.Resources.LimitCPU)...)
		errs = append(errs, validateQuantity(p+"resources.limitMemory", c.Resources.LimitMemory)...)
	}

	// Validate probes
	if c.Probes != nil {
		if c.Probes.Liveness != nil {
			errs = append(errs, validateProbe(p+"probes.liveness", c.Probes.Liveness)...)
		}
		if c.Probes.Readiness != nil {
			errs = append(errs, validateProbe(p+"probes.readiness", c.Probes.Readiness)...)
		}
	}

	// Validate volume mounts
	seenMounts := make(map[string]bool)
	for i, vm := range c.VolumeMounts {
		if vm.Name == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%svolumeMounts[%d].name", p, i),
				Message: "is required",
			})
		}
		if vm.MountPath == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%svolumeMounts[%d].mountPath", p, i),
				Message: "is required",
			})
		} else if vm.MountPath[0] != '/' {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%svolumeMounts[%d].mountPath", p, i),
				Message: "must be an absolute path",
			})
		}
		if seenMounts[vm.MountPath] {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("%svolumeMounts[%d].mountPath", p, i),
				Message: fmt.Sprintf("duplicate mount path %q", vm.MountPath),
			})
		}
		if vm.MountPath != "" {
			seenMounts[vm.MountPath] = true
		}
	}

	return errs
}

// BuildContainer converts the ContainerInput into a corev1.Container.
// The name parameter is used as the container name.
func (c *ContainerInput) BuildContainer(name string) (corev1.Container, error) {
	container := corev1.Container{
		Name:    name,
		Image:   c.Image,
		Command: c.Command,
		Args:    c.Args,
	}

	// Ports
	for _, p := range c.Ports {
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
	for _, e := range c.EnvVars {
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
	if c.Resources != nil {
		reqs := corev1.ResourceList{}
		lims := corev1.ResourceList{}
		if c.Resources.RequestCPU != "" {
			q, err := resource.ParseQuantity(c.Resources.RequestCPU)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("invalid CPU request %q: %w", c.Resources.RequestCPU, err)
			}
			reqs[corev1.ResourceCPU] = q
		}
		if c.Resources.RequestMemory != "" {
			q, err := resource.ParseQuantity(c.Resources.RequestMemory)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("invalid memory request %q: %w", c.Resources.RequestMemory, err)
			}
			reqs[corev1.ResourceMemory] = q
		}
		if c.Resources.LimitCPU != "" {
			q, err := resource.ParseQuantity(c.Resources.LimitCPU)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("invalid CPU limit %q: %w", c.Resources.LimitCPU, err)
			}
			lims[corev1.ResourceCPU] = q
		}
		if c.Resources.LimitMemory != "" {
			q, err := resource.ParseQuantity(c.Resources.LimitMemory)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("invalid memory limit %q: %w", c.Resources.LimitMemory, err)
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
	if c.Probes != nil {
		if c.Probes.Liveness != nil {
			container.LivenessProbe = buildProbe(c.Probes.Liveness)
		}
		if c.Probes.Readiness != nil {
			container.ReadinessProbe = buildProbe(c.Probes.Readiness)
		}
	}

	// Volume mounts
	for _, vm := range c.VolumeMounts {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      vm.Name,
			MountPath: vm.MountPath,
			ReadOnly:  vm.ReadOnly,
		})
	}

	return container, nil
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
