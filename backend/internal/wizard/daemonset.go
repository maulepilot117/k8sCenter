package wizard

import (
	"fmt"

	sigsyaml "sigs.k8s.io/yaml"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DaemonSetInput represents the wizard form data for creating a DaemonSet.
type DaemonSetInput struct {
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	Container      ContainerInput    `json:"container"`
	NodeSelector   map[string]string `json:"nodeSelector,omitempty"`
	MaxUnavailable string            `json:"maxUnavailable,omitempty"`
}

// Validate checks the DaemonSetInput and returns field-level errors.
func (d *DaemonSetInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(d.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if d.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(d.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	// Validate container fields
	errs = append(errs, d.Container.ValidateContainer("container")...)

	// Validate nodeSelector key/value lengths
	if len(d.NodeSelector) > 50 {
		errs = append(errs, FieldError{Field: "nodeSelector", Message: "must have 50 or fewer entries"})
	}
	for k, v := range d.NodeSelector {
		if len(k) > 253 {
			errs = append(errs, FieldError{Field: "nodeSelector", Message: fmt.Sprintf("key %q exceeds 253 characters", k)})
		}
		if len(v) > 63 {
			errs = append(errs, FieldError{Field: "nodeSelector", Message: fmt.Sprintf("value for key %q exceeds 63 characters", k)})
		}
	}

	return errs
}

// ToDaemonSet converts the wizard input to a typed Kubernetes DaemonSet.
// Validate() should be called before ToDaemonSet() to ensure inputs are well-formed.
func (d *DaemonSetInput) ToDaemonSet() (*appsv1.DaemonSet, error) {
	lbls := map[string]string{"app": d.Name}

	container, err := d.Container.BuildContainer(d.Name)
	if err != nil {
		return nil, err
	}

	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.Namespace,
			Labels:    lbls,
		},
		Spec: appsv1.DaemonSetSpec{
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

	// Node selector
	if len(d.NodeSelector) > 0 {
		ds.Spec.Template.Spec.NodeSelector = d.NodeSelector
	}

	// Update strategy with maxUnavailable
	if d.MaxUnavailable != "" {
		v := intstr.Parse(d.MaxUnavailable)
		ds.Spec.UpdateStrategy = appsv1.DaemonSetUpdateStrategy{
			Type: appsv1.RollingUpdateDaemonSetStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDaemonSet{
				MaxUnavailable: &v,
			},
		}
	}

	return ds, nil
}

// ToYAML implements WizardInput by converting to a DaemonSet and marshaling to YAML.
func (d *DaemonSetInput) ToYAML() (string, error) {
	ds, err := d.ToDaemonSet()
	if err != nil {
		return "", err
	}
	yamlBytes, err := sigsyaml.Marshal(ds)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
