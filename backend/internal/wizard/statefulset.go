package wizard

import (
	"fmt"

	sigsyaml "sigs.k8s.io/yaml"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VolumeClaimTemplate represents a PVC template for a StatefulSet.
type VolumeClaimTemplate struct {
	Name             string `json:"name"`
	StorageClassName string `json:"storageClassName"`
	Size             string `json:"size"`       // e.g. "10Gi"
	AccessMode       string `json:"accessMode"` // ReadWriteOnce, ReadWriteMany, ReadOnlyMany, ReadWriteOncePod
}

// StatefulSetInput represents the wizard form data for creating a StatefulSet.
type StatefulSetInput struct {
	Name                 string                `json:"name"`
	Namespace            string                `json:"namespace"`
	ServiceName          string                `json:"serviceName"`
	Replicas             int32                 `json:"replicas"`
	Container            ContainerInput        `json:"container"`
	VolumeClaimTemplates []VolumeClaimTemplate `json:"volumeClaimTemplates,omitempty"`
	PodManagementPolicy  string                `json:"podManagementPolicy,omitempty"`
}

// Validate checks the StatefulSetInput and returns field-level errors.
func (s *StatefulSetInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(s.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if s.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if s.ServiceName == "" {
		errs = append(errs, FieldError{Field: "serviceName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.ServiceName) {
		errs = append(errs, FieldError{Field: "serviceName", Message: "must be a valid DNS label"})
	}

	if s.Replicas < 0 || s.Replicas > 1000 {
		errs = append(errs, FieldError{Field: "replicas", Message: "must be between 0 and 1000"})
	}

	// Validate container fields
	errs = append(errs, s.Container.ValidateContainer("container")...)

	// Validate volume claim templates
	if len(s.VolumeClaimTemplates) > 20 {
		errs = append(errs, FieldError{Field: "volumeClaimTemplates", Message: "must have 20 or fewer entries"})
	}
	seenVCTNames := make(map[string]bool)
	for i, vct := range s.VolumeClaimTemplates {
		prefix := fmt.Sprintf("volumeClaimTemplates[%d]", i)
		if !dnsLabelRegex.MatchString(vct.Name) {
			errs = append(errs, FieldError{Field: prefix + ".name", Message: "must be a valid DNS label"})
		}
		if seenVCTNames[vct.Name] {
			errs = append(errs, FieldError{Field: prefix + ".name", Message: fmt.Sprintf("duplicate name %q", vct.Name)})
		}
		if vct.Name != "" {
			seenVCTNames[vct.Name] = true
		}

		if vct.Size == "" {
			errs = append(errs, FieldError{Field: prefix + ".size", Message: "is required"})
		} else {
			qty, err := resource.ParseQuantity(vct.Size)
			if err != nil {
				errs = append(errs, FieldError{Field: prefix + ".size", Message: "must be a valid quantity (e.g. 10Gi)"})
			} else if qty.IsZero() || qty.Cmp(resource.MustParse("0")) <= 0 {
				errs = append(errs, FieldError{Field: prefix + ".size", Message: "must be greater than 0"})
			}
		}

		if _, ok := validAccessModes[vct.AccessMode]; !ok {
			errs = append(errs, FieldError{Field: prefix + ".accessMode", Message: "must be ReadWriteOnce, ReadWriteMany, ReadOnlyMany, or ReadWriteOncePod"})
		}
	}

	// Validate pod management policy
	if s.PodManagementPolicy != "" && s.PodManagementPolicy != "OrderedReady" && s.PodManagementPolicy != "Parallel" {
		errs = append(errs, FieldError{Field: "podManagementPolicy", Message: "must be OrderedReady or Parallel"})
	}

	return errs
}

// ToStatefulSet converts the wizard input to a typed Kubernetes StatefulSet.
// Validate() should be called before ToStatefulSet() to ensure inputs are well-formed.
func (s *StatefulSetInput) ToStatefulSet() (*appsv1.StatefulSet, error) {
	lbls := map[string]string{"app": s.Name}

	container, err := s.Container.BuildContainer(s.Name)
	if err != nil {
		return nil, err
	}

	replicas := s.Replicas
	if replicas == 0 {
		replicas = 1 // Default to 1 replica for creation wizards
	}

	sts := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels:    lbls,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: s.ServiceName,
			Replicas:    &replicas,
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

	// Pod management policy
	if s.PodManagementPolicy != "" {
		sts.Spec.PodManagementPolicy = appsv1.PodManagementPolicyType(s.PodManagementPolicy)
	}

	// Volume claim templates
	for _, vct := range s.VolumeClaimTemplates {
		scName := vct.StorageClassName
		pvc := corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: vct.Name,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{validAccessModes[vct.AccessMode]},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(vct.Size),
					},
				},
			},
		}
		if scName != "" {
			pvc.Spec.StorageClassName = &scName
		}
		sts.Spec.VolumeClaimTemplates = append(sts.Spec.VolumeClaimTemplates, pvc)
	}

	return sts, nil
}

// ToYAML implements WizardInput by converting to a StatefulSet and marshaling to YAML.
func (s *StatefulSetInput) ToYAML() (string, error) {
	sts, err := s.ToStatefulSet()
	if err != nil {
		return "", err
	}
	yamlBytes, err := sigsyaml.Marshal(sts)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
