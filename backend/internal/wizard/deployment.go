package wizard

import (
	"fmt"

	sigsyaml "sigs.k8s.io/yaml"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

// StrategyInput represents the deployment update strategy.
type StrategyInput struct {
	Type           string `json:"type,omitempty"`
	MaxSurge       string `json:"maxSurge,omitempty"`
	MaxUnavailable string `json:"maxUnavailable,omitempty"`
}

// toContainerInput builds a ContainerInput from the flat DeploymentInput fields.
func (d *DeploymentInput) toContainerInput() *ContainerInput {
	return &ContainerInput{
		Image:     d.Image,
		Ports:     d.Ports,
		EnvVars:   d.EnvVars,
		Resources: d.Resources,
		Probes:    d.Probes,
	}
}

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

	// Delegate container field validation (image, ports, envVars, resources, probes)
	ci := d.toContainerInput()
	errs = append(errs, ci.ValidateContainer("")...)

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

	ci := d.toContainerInput()
	container, err := ci.BuildContainer(d.Name)
	if err != nil {
		return nil, err
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

// ToYAML implements WizardInput by converting to a Deployment and marshaling to YAML.
func (d *DeploymentInput) ToYAML() (string, error) {
	dep, err := d.ToDeployment()
	if err != nil {
		return "", err
	}
	yamlBytes, err := sigsyaml.Marshal(dep)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
