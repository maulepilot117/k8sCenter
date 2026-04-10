package wizard

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// PVCInput represents the wizard form data for creating a PersistentVolumeClaim.
type PVCInput struct {
	Name             string         `json:"name"`
	Namespace        string         `json:"namespace"`
	StorageClassName string         `json:"storageClassName"`
	Size             string         `json:"size"`       // e.g. "10Gi"
	AccessMode       string         `json:"accessMode"` // ReadWriteOnce, ReadWriteMany, ReadOnlyMany, ReadWriteOncePod
	DataSource       *PVCDataSource `json:"dataSource,omitempty"`
}

// PVCDataSource represents a data source for restoring from a VolumeSnapshot.
type PVCDataSource struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`     // "VolumeSnapshot"
	APIGroup string `json:"apiGroup"` // "snapshot.storage.k8s.io"
}

var validAccessModes = map[string]corev1.PersistentVolumeAccessMode{
	"ReadWriteOnce":    corev1.ReadWriteOnce,
	"ReadWriteMany":    corev1.ReadWriteMany,
	"ReadOnlyMany":     corev1.ReadOnlyMany,
	"ReadWriteOncePod": corev1.ReadWriteOncePod,
}

// Validate checks the PVCInput and returns field-level errors.
func (p *PVCInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(p.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if p.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(p.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if p.StorageClassName == "" {
		errs = append(errs, FieldError{Field: "storageClassName", Message: "is required"})
	}

	if p.Size == "" {
		errs = append(errs, FieldError{Field: "size", Message: "is required"})
	} else {
		qty, err := resource.ParseQuantity(p.Size)
		if err != nil {
			errs = append(errs, FieldError{Field: "size", Message: "must be a valid quantity (e.g. 10Gi)"})
		} else if qty.IsZero() || qty.Cmp(resource.MustParse("0")) <= 0 {
			errs = append(errs, FieldError{Field: "size", Message: "must be greater than 0"})
		}
	}

	if _, ok := validAccessModes[p.AccessMode]; !ok {
		errs = append(errs, FieldError{Field: "accessMode", Message: "must be ReadWriteOnce, ReadWriteMany, ReadOnlyMany, or ReadWriteOncePod"})
	}

	if p.DataSource != nil {
		if p.DataSource.Name == "" {
			errs = append(errs, FieldError{Field: "dataSource.name", Message: "is required"})
		}
		if p.DataSource.Kind == "" {
			errs = append(errs, FieldError{Field: "dataSource.kind", Message: "is required"})
		}
		if p.DataSource.APIGroup == "" {
			errs = append(errs, FieldError{Field: "dataSource.apiGroup", Message: "is required"})
		}
	}

	return errs
}

// ToPersistentVolumeClaim converts the wizard input to a typed PVC.
func (p *PVCInput) ToPersistentVolumeClaim() *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &p.StorageClassName,
			AccessModes:      []corev1.PersistentVolumeAccessMode{validAccessModes[p.AccessMode]},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(p.Size),
				},
			},
		},
	}

	if p.DataSource != nil {
		apiGroup := p.DataSource.APIGroup
		pvc.Spec.DataSource = &corev1.TypedLocalObjectReference{
			APIGroup: &apiGroup,
			Kind:     p.DataSource.Kind,
			Name:     p.DataSource.Name,
		}
	}

	return pvc
}

// ToYAML implements WizardInput by converting to a PVC and marshaling to YAML.
func (p *PVCInput) ToYAML() (string, error) {
	pvc := p.ToPersistentVolumeClaim()
	yamlBytes, err := sigsyaml.Marshal(pvc)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
