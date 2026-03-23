package wizard

import (
	sigsyaml "sigs.k8s.io/yaml"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RoleBindingInput represents the wizard form data for creating a RoleBinding or ClusterRoleBinding.
type RoleBindingInput struct {
	Name         string         `json:"name"`
	Namespace    string         `json:"namespace,omitempty"` // empty for ClusterRoleBinding
	ClusterScope bool           `json:"clusterScope"`
	RoleRef      RoleRefInput   `json:"roleRef"`
	Subjects     []SubjectInput `json:"subjects"`
}

// RoleRefInput represents the role reference in a binding.
type RoleRefInput struct {
	Kind string `json:"kind"` // "Role" or "ClusterRole"
	Name string `json:"name"`
}

// SubjectInput represents a subject in a binding.
type SubjectInput struct {
	Kind      string `json:"kind"`      // "User", "Group", or "ServiceAccount"
	Name      string `json:"name"`
	Namespace string `json:"namespace"` // only for ServiceAccount
}

// Validate checks the RoleBindingInput and returns field-level errors.
func (rb *RoleBindingInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(rb.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if !rb.ClusterScope {
		if rb.Namespace == "" {
			errs = append(errs, FieldError{Field: "namespace", Message: "is required for RoleBinding"})
		} else if !dnsLabelRegex.MatchString(rb.Namespace) {
			errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
		}
	}

	// Validate roleRef
	if rb.RoleRef.Kind != "Role" && rb.RoleRef.Kind != "ClusterRole" {
		errs = append(errs, FieldError{Field: "roleRef.kind", Message: "must be Role or ClusterRole"})
	}
	if rb.ClusterScope && rb.RoleRef.Kind == "Role" {
		errs = append(errs, FieldError{Field: "roleRef.kind", Message: "ClusterRoleBinding can only reference a ClusterRole"})
	}
	if rb.RoleRef.Name == "" {
		errs = append(errs, FieldError{Field: "roleRef.name", Message: "is required"})
	}

	// Validate subjects
	if len(rb.Subjects) == 0 {
		errs = append(errs, FieldError{Field: "subjects", Message: "at least one subject is required"})
	}
	if len(rb.Subjects) > 50 {
		errs = append(errs, FieldError{Field: "subjects", Message: "must have 50 or fewer subjects"})
	}
	validKinds := map[string]bool{"User": true, "Group": true, "ServiceAccount": true}
	for i, s := range rb.Subjects {
		if !validKinds[s.Kind] {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("subjects[%d].kind", i),
				Message: "must be User, Group, or ServiceAccount",
			})
		}
		if s.Name == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("subjects[%d].name", i),
				Message: "is required",
			})
		}
		if s.Kind == "ServiceAccount" && s.Namespace == "" {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("subjects[%d].namespace", i),
				Message: "is required for ServiceAccount subjects",
			})
		}
	}

	return errs
}

// ToRoleBinding converts the input to a typed Kubernetes RoleBinding.
func (rb *RoleBindingInput) ToRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "RoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rb.Name,
			Namespace: rb.Namespace,
		},
		RoleRef:  rb.buildRoleRef(),
		Subjects: rb.buildSubjects(),
	}
}

// ToClusterRoleBinding converts the input to a typed Kubernetes ClusterRoleBinding.
func (rb *RoleBindingInput) ToClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: rb.Name,
		},
		RoleRef:  rb.buildRoleRef(),
		Subjects: rb.buildSubjects(),
	}
}

func (rb *RoleBindingInput) buildRoleRef() rbacv1.RoleRef {
	return rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     rb.RoleRef.Kind,
		Name:     rb.RoleRef.Name,
	}
}

func (rb *RoleBindingInput) buildSubjects() []rbacv1.Subject {
	subjects := make([]rbacv1.Subject, len(rb.Subjects))
	for i, s := range rb.Subjects {
		sub := rbacv1.Subject{
			Kind: s.Kind,
			Name: s.Name,
		}
		if s.Kind == "ServiceAccount" {
			sub.APIGroup = ""
			sub.Namespace = s.Namespace
		} else {
			sub.APIGroup = "rbac.authorization.k8s.io"
		}
		subjects[i] = sub
	}
	return subjects
}

// ToYAML implements WizardInput by converting to a RoleBinding or ClusterRoleBinding.
func (rb *RoleBindingInput) ToYAML() (string, error) {
	var obj any
	if rb.ClusterScope {
		obj = rb.ToClusterRoleBinding()
	} else {
		obj = rb.ToRoleBinding()
	}
	yamlBytes, err := sigsyaml.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
