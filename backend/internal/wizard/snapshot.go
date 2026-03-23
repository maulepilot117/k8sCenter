package wizard

import (
	sigsyaml "sigs.k8s.io/yaml"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SnapshotInput represents the wizard form data for creating a VolumeSnapshot.
type SnapshotInput struct {
	Name                    string `json:"name"`
	Namespace               string `json:"namespace"`
	SourcePVC               string `json:"sourcePVC"`
	VolumeSnapshotClassName string `json:"volumeSnapshotClassName,omitempty"`
}

// Validate checks the SnapshotInput and returns field-level errors.
func (s *SnapshotInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(s.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if s.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if s.SourcePVC == "" {
		errs = append(errs, FieldError{Field: "sourcePVC", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.SourcePVC) {
		errs = append(errs, FieldError{Field: "sourcePVC", Message: "must be a valid DNS label"})
	}

	if s.VolumeSnapshotClassName != "" && !dnsLabelRegex.MatchString(s.VolumeSnapshotClassName) {
		errs = append(errs, FieldError{Field: "volumeSnapshotClassName", Message: "must be a valid DNS label"})
	}

	return errs
}

// ToVolumeSnapshot converts the input to an unstructured VolumeSnapshot resource.
// VolumeSnapshot is a CRD, so we use unstructured instead of typed structs.
func (s *SnapshotInput) ToVolumeSnapshot() *unstructured.Unstructured {
	spec := map[string]interface{}{
		"source": map[string]interface{}{
			"persistentVolumeClaimName": s.SourcePVC,
		},
	}

	if s.VolumeSnapshotClassName != "" {
		spec["volumeSnapshotClassName"] = s.VolumeSnapshotClassName
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "snapshot.storage.k8s.io/v1",
			"kind":       "VolumeSnapshot",
			"metadata": map[string]interface{}{
				"name":      s.Name,
				"namespace": s.Namespace,
			},
			"spec": spec,
		},
	}

	return obj
}

// ToYAML implements WizardInput by marshaling the unstructured VolumeSnapshot.
func (s *SnapshotInput) ToYAML() (string, error) {
	obj := s.ToVolumeSnapshot()
	yamlBytes, err := sigsyaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
