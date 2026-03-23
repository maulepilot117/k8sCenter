package wizard

import (
	"fmt"

	sigsyaml "sigs.k8s.io/yaml"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HPAInput represents the wizard form data for creating a HorizontalPodAutoscaler.
type HPAInput struct {
	Name        string           `json:"name"`
	Namespace   string           `json:"namespace"`
	TargetKind  string           `json:"targetKind"`
	TargetName  string           `json:"targetName"`
	MinReplicas *int32           `json:"minReplicas,omitempty"`
	MaxReplicas int32            `json:"maxReplicas"`
	Metrics     []HPAMetricInput `json:"metrics"`
}

// HPAMetricInput represents a single metric source for the HPA.
type HPAMetricInput struct {
	Type               string `json:"type"`
	ResourceName       string `json:"resourceName,omitempty"`
	TargetType         string `json:"targetType"`
	TargetAverageValue int32  `json:"targetAverageValue"`
}

// Validate checks the HPAInput and returns field-level errors.
func (h *HPAInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(h.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if h.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(h.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	switch h.TargetKind {
	case "Deployment", "StatefulSet", "ReplicaSet":
		// valid
	default:
		errs = append(errs, FieldError{Field: "targetKind", Message: "must be Deployment, StatefulSet, or ReplicaSet"})
	}

	if h.TargetName == "" {
		errs = append(errs, FieldError{Field: "targetName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(h.TargetName) {
		errs = append(errs, FieldError{Field: "targetName", Message: "must be a valid DNS label"})
	}

	if h.MaxReplicas < 1 || h.MaxReplicas > 1000 {
		errs = append(errs, FieldError{Field: "maxReplicas", Message: "must be between 1 and 1000"})
	}

	if h.MinReplicas != nil {
		if *h.MinReplicas < 1 {
			errs = append(errs, FieldError{Field: "minReplicas", Message: "must be at least 1"})
		} else if *h.MinReplicas > h.MaxReplicas {
			errs = append(errs, FieldError{Field: "minReplicas", Message: "must not exceed maxReplicas"})
		}
	}

	if len(h.Metrics) == 0 {
		errs = append(errs, FieldError{Field: "metrics", Message: "at least one metric is required"})
	} else {
		for i, m := range h.Metrics {
			errs = append(errs, validateHPAMetric(i, m)...)
		}
	}

	return errs
}

// validateHPAMetric validates a single HPAMetricInput at index i.
func validateHPAMetric(i int, m HPAMetricInput) []FieldError {
	var errs []FieldError
	prefix := fmt.Sprintf("metrics[%d]", i)

	if m.Type != "Resource" {
		errs = append(errs, FieldError{Field: prefix + ".type", Message: "must be Resource"})
	}

	if m.ResourceName != "cpu" && m.ResourceName != "memory" {
		errs = append(errs, FieldError{Field: prefix + ".resourceName", Message: "must be cpu or memory"})
	}

	if m.TargetType != "Utilization" && m.TargetType != "AverageValue" {
		errs = append(errs, FieldError{Field: prefix + ".targetType", Message: "must be Utilization or AverageValue"})
	}

	if m.TargetType == "Utilization" {
		if m.TargetAverageValue < 1 || m.TargetAverageValue > 100 {
			errs = append(errs, FieldError{Field: prefix + ".targetAverageValue", Message: "must be between 1 and 100 for Utilization (percentage)"})
		}
	} else if m.TargetAverageValue < 1 {
		errs = append(errs, FieldError{Field: prefix + ".targetAverageValue", Message: "must be at least 1"})
	}

	return errs
}

// ToYAML implements WizardInput by converting to an HPA and marshaling to YAML.
func (h *HPAInput) ToYAML() (string, error) {
	hpa := h.ToHPA()
	yamlBytes, err := sigsyaml.Marshal(hpa)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

// ToHPA converts the wizard input to a typed Kubernetes HorizontalPodAutoscaler.
// Validate() should be called before ToHPA() to ensure inputs are well-formed.
func (h *HPAInput) ToHPA() *autoscalingv2.HorizontalPodAutoscaler {
	metrics := buildHPAMetrics(h.Metrics)

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "autoscaling/v2",
			Kind:       "HorizontalPodAutoscaler",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       h.TargetKind,
				Name:       h.TargetName,
			},
			MaxReplicas: h.MaxReplicas,
			Metrics:     metrics,
		},
	}

	if h.MinReplicas != nil {
		hpa.Spec.MinReplicas = h.MinReplicas
	}

	return hpa
}

// buildHPAMetrics converts HPAMetricInput slice to autoscalingv2.MetricSpec slice.
func buildHPAMetrics(inputs []HPAMetricInput) []autoscalingv2.MetricSpec {
	metrics := make([]autoscalingv2.MetricSpec, 0, len(inputs))

	for _, m := range inputs {
		resourceName := toK8sResourceName(m.ResourceName)
		metricTarget := buildMetricTarget(m)

		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name:   resourceName,
				Target: metricTarget,
			},
		})
	}

	return metrics
}

// toK8sResourceName maps a string resource name to a corev1.ResourceName.
// Panics on unsupported values — Validate() must be called first.
func toK8sResourceName(name string) corev1.ResourceName {
	switch name {
	case "cpu":
		return corev1.ResourceCPU
	case "memory":
		return corev1.ResourceMemory
	default:
		panic("toK8sResourceName: unsupported resource name (call Validate first): " + name)
	}
}

// buildMetricTarget constructs the MetricTarget based on the TargetType.
// Panics on unsupported values — Validate() must be called first.
func buildMetricTarget(m HPAMetricInput) autoscalingv2.MetricTarget {
	switch m.TargetType {
	case "Utilization":
		utilization := m.TargetAverageValue
		return autoscalingv2.MetricTarget{
			Type:               autoscalingv2.UtilizationMetricType,
			AverageUtilization: &utilization,
		}
	case "AverageValue":
		qty := resource.NewMilliQuantity(int64(m.TargetAverageValue)*1000, resource.DecimalSI)
		return autoscalingv2.MetricTarget{
			Type:         autoscalingv2.AverageValueMetricType,
			AverageValue: qty,
		}
	default:
		panic("buildMetricTarget: unsupported target type (call Validate first): " + m.TargetType)
	}
}
