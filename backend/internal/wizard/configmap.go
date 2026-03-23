package wizard

import (
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// ConfigMapInput represents the wizard form data for creating a ConfigMap.
type ConfigMapInput struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Data      map[string]string `json:"data"`
}

// configMapKeyRegex validates ConfigMap data keys: alphanumeric, hyphens, underscores, dots.
// Must start and end with alphanumeric, max 253 characters (k8s subdomain limit for keys).
var configMapKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,251}[a-zA-Z0-9])?$`)

// maxConfigMapDataSize is the maximum total size of all data entries (1 MB).
const maxConfigMapDataSize = 1 << 20

// Validate checks the ConfigMapInput and returns field-level errors.
func (c *ConfigMapInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(c.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if c.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(c.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	totalSize := 0
	for key, val := range c.Data {
		if !configMapKeyRegex.MatchString(key) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("data[%s]", key),
				Message: "key must be alphanumeric with hyphens, underscores, or dots (1-253 chars)",
			})
		}
		totalSize += len(key) + len(val)
	}

	if totalSize > maxConfigMapDataSize {
		errs = append(errs, FieldError{Field: "data", Message: "total data size must be less than 1MB"})
	}

	return errs
}

// ToYAML implements WizardInput by converting to a ConfigMap and marshaling to YAML.
func (c *ConfigMapInput) ToYAML() (string, error) {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name,
			Namespace: c.Namespace,
		},
		Data: c.Data,
	}

	yamlBytes, err := sigsyaml.Marshal(cm)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
