package wizard

import (
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// dnsSubdomainRegex validates RFC 1123 DNS subdomains (up to 253 chars, used for StorageClass names).
var dnsSubdomainRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// validReclaimPolicies lists the allowed StorageClass reclaim policies.
var validReclaimPolicies = map[string]bool{
	"Delete": true,
	"Retain": true,
}

// validBindingModes lists the allowed volume binding modes.
var validBindingModes = map[string]bool{
	"Immediate":            true,
	"WaitForFirstConsumer": true,
}

// StorageClassInput represents the wizard form data for creating a StorageClass.
type StorageClassInput struct {
	Name                 string            `json:"name"`
	Provisioner          string            `json:"provisioner"`
	ReclaimPolicy        string            `json:"reclaimPolicy,omitempty"`
	VolumeBindingMode    string            `json:"volumeBindingMode,omitempty"`
	AllowVolumeExpansion bool              `json:"allowVolumeExpansion,omitempty"`
	IsDefault            bool              `json:"isDefault,omitempty"`
	Parameters           map[string]string `json:"parameters,omitempty"`
	MountOptions         []string          `json:"mountOptions,omitempty"`
}

// Validate checks the StorageClassInput and returns field-level errors.
func (s *StorageClassInput) Validate() []FieldError {
	var errs []FieldError

	if s.Name == "" {
		errs = append(errs, FieldError{Field: "name", Message: "is required"})
	} else if len(s.Name) > 253 {
		errs = append(errs, FieldError{Field: "name", Message: "must be 253 characters or less"})
	} else if !dnsSubdomainRegex.MatchString(s.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS subdomain (lowercase alphanumeric, hyphens, and dots)"})
	}

	if s.Provisioner == "" {
		errs = append(errs, FieldError{Field: "provisioner", Message: "is required"})
	} else if len(s.Provisioner) > 253 {
		errs = append(errs, FieldError{Field: "provisioner", Message: "must be 253 characters or less"})
	}

	if s.ReclaimPolicy != "" && !validReclaimPolicies[s.ReclaimPolicy] {
		errs = append(errs, FieldError{
			Field:   "reclaimPolicy",
			Message: fmt.Sprintf("must be one of: Delete, Retain"),
		})
	}

	if s.VolumeBindingMode != "" && !validBindingModes[s.VolumeBindingMode] {
		errs = append(errs, FieldError{
			Field:   "volumeBindingMode",
			Message: fmt.Sprintf("must be one of: Immediate, WaitForFirstConsumer"),
		})
	}

	if len(s.Parameters) > 50 {
		errs = append(errs, FieldError{Field: "parameters", Message: "must have 50 or fewer entries"})
	}
	for k := range s.Parameters {
		if len(k) > 253 {
			errs = append(errs, FieldError{Field: "parameters", Message: fmt.Sprintf("key %q exceeds 253 characters", k)})
		}
	}

	if len(s.MountOptions) > 20 {
		errs = append(errs, FieldError{Field: "mountOptions", Message: "must have 20 or fewer entries"})
	}

	return errs
}

// ToStorageClass converts the wizard input to a typed Kubernetes StorageClass.
// Validate() should be called before ToStorageClass() to ensure inputs are well-formed.
func (s *StorageClassInput) ToStorageClass() *storagev1.StorageClass {
	sc := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "storage.k8s.io/v1",
			Kind:       "StorageClass",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name,
		},
		Provisioner:          s.Provisioner,
		AllowVolumeExpansion: &s.AllowVolumeExpansion,
	}

	if len(s.Parameters) > 0 {
		sc.Parameters = s.Parameters
	}

	if len(s.MountOptions) > 0 {
		sc.MountOptions = s.MountOptions
	}

	if s.ReclaimPolicy != "" {
		policy := corev1.PersistentVolumeReclaimPolicy(s.ReclaimPolicy)
		sc.ReclaimPolicy = &policy
	}

	if s.VolumeBindingMode != "" {
		mode := storagev1.VolumeBindingMode(s.VolumeBindingMode)
		sc.VolumeBindingMode = &mode
	}

	if s.IsDefault {
		sc.Annotations = map[string]string{
			"storageclass.kubernetes.io/is-default-class": "true",
		}
	}

	return sc
}
