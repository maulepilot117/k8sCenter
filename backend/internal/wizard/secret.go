package wizard

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// SecretInput represents the wizard form data for creating a Secret.
type SecretInput struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Type      string            `json:"type"`
	Data      map[string]string `json:"data"`
}

// validSecretTypes lists the allowed secret types for the wizard.
var validSecretTypes = map[string]corev1.SecretType{
	"Opaque":                              corev1.SecretTypeOpaque,
	"kubernetes.io/tls":                   corev1.SecretTypeTLS,
	"kubernetes.io/basic-auth":            corev1.SecretTypeBasicAuth,
	"kubernetes.io/dockerconfigjson":      corev1.SecretTypeDockerConfigJson,
}

// Validate checks the SecretInput and returns field-level errors.
func (s *SecretInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(s.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if s.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	secretType, ok := validSecretTypes[s.Type]
	if !ok {
		errs = append(errs, FieldError{Field: "type", Message: "must be Opaque, kubernetes.io/tls, kubernetes.io/basic-auth, or kubernetes.io/dockerconfigjson"})
	} else {
		// Type-specific key validation
		switch secretType {
		case corev1.SecretTypeTLS:
			if _, hasCrt := s.Data["tls.crt"]; !hasCrt {
				errs = append(errs, FieldError{Field: "data.tls.crt", Message: "is required for TLS secrets"})
			}
			if _, hasKey := s.Data["tls.key"]; !hasKey {
				errs = append(errs, FieldError{Field: "data.tls.key", Message: "is required for TLS secrets"})
			}
		case corev1.SecretTypeBasicAuth:
			if _, hasUser := s.Data["username"]; !hasUser {
				errs = append(errs, FieldError{Field: "data.username", Message: "is required for basic-auth secrets"})
			}
		case corev1.SecretTypeDockerConfigJson:
			if _, hasCfg := s.Data[".dockerconfigjson"]; !hasCfg {
				errs = append(errs, FieldError{Field: "data..dockerconfigjson", Message: "is required for docker-registry secrets"})
			}
		}
	}

	return errs
}

// ToSecret converts the wizard input to a typed Secret.
func (s *SecretInput) ToSecret() *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
		},
		StringData: s.Data,
		Type:       validSecretTypes[s.Type],
	}
}

// ToYAML implements WizardInput by converting to a Secret and marshaling to YAML.
func (s *SecretInput) ToYAML() (string, error) {
	secret := s.ToSecret()
	yamlBytes, err := sigsyaml.Marshal(secret)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
