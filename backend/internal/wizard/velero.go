package wizard

import (
	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"
)

// VeleroBackupInput represents the wizard form data for creating a Velero Backup.
type VeleroBackupInput struct {
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	IncludedNamespaces []string          `json:"includedNamespaces,omitempty"`
	ExcludedNamespaces []string          `json:"excludedNamespaces,omitempty"`
	StorageLocation    string            `json:"storageLocation,omitempty"`
	TTL                string            `json:"ttl,omitempty"`
	SnapshotVolumes    *bool             `json:"snapshotVolumes,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
}

// Validate checks the VeleroBackupInput and returns field-level errors.
func (v *VeleroBackupInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(v.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if v.Namespace == "" {
		v.Namespace = "velero" // Default to velero namespace
	} else if !dnsLabelRegex.MatchString(v.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	for i, ns := range v.IncludedNamespaces {
		if ns != "*" && !dnsLabelRegex.MatchString(ns) {
			errs = append(errs, FieldError{Field: "includedNamespaces", Message: "each namespace must be a valid DNS label or '*'"})
			break
		}
		_ = i
	}

	for i, ns := range v.ExcludedNamespaces {
		if !dnsLabelRegex.MatchString(ns) {
			errs = append(errs, FieldError{Field: "excludedNamespaces", Message: "each namespace must be a valid DNS label"})
			break
		}
		_ = i
	}

	return errs
}

// ToBackup converts the input to an unstructured Velero Backup resource.
func (v *VeleroBackupInput) ToBackup() *unstructured.Unstructured {
	spec := map[string]any{}

	if len(v.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = v.IncludedNamespaces
	}
	if len(v.ExcludedNamespaces) > 0 {
		spec["excludedNamespaces"] = v.ExcludedNamespaces
	}
	if v.StorageLocation != "" {
		spec["storageLocation"] = v.StorageLocation
	}
	if v.TTL != "" {
		spec["ttl"] = v.TTL
	}
	if v.SnapshotVolumes != nil {
		spec["snapshotVolumes"] = *v.SnapshotVolumes
	}

	metadata := map[string]any{
		"name":      v.Name,
		"namespace": v.Namespace,
	}
	if len(v.Labels) > 0 {
		metadata["labels"] = v.Labels
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "Backup",
			"metadata":   metadata,
			"spec":       spec,
		},
	}
}

// ToYAML implements WizardInput by marshaling the unstructured Backup.
func (v *VeleroBackupInput) ToYAML() (string, error) {
	obj := v.ToBackup()
	yamlBytes, err := sigsyaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

// VeleroRestoreInput represents the wizard form data for creating a Velero Restore.
type VeleroRestoreInput struct {
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	BackupName         string            `json:"backupName"`
	ScheduleName       string            `json:"scheduleName,omitempty"`
	IncludedNamespaces []string          `json:"includedNamespaces,omitempty"`
	ExcludedNamespaces []string          `json:"excludedNamespaces,omitempty"`
	NamespaceMapping   map[string]string `json:"namespaceMapping,omitempty"`
	RestorePVs         *bool             `json:"restorePVs,omitempty"`
}

// Validate checks the VeleroRestoreInput and returns field-level errors.
func (v *VeleroRestoreInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(v.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if v.Namespace == "" {
		v.Namespace = "velero"
	} else if !dnsLabelRegex.MatchString(v.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	// Must have either backupName or scheduleName
	if v.BackupName == "" && v.ScheduleName == "" {
		errs = append(errs, FieldError{Field: "backupName", Message: "backupName or scheduleName is required"})
	}

	if v.BackupName != "" && v.ScheduleName != "" {
		errs = append(errs, FieldError{Field: "backupName", Message: "cannot specify both backupName and scheduleName"})
	}

	return errs
}

// ToRestore converts the input to an unstructured Velero Restore resource.
func (v *VeleroRestoreInput) ToRestore() *unstructured.Unstructured {
	spec := map[string]any{}

	if v.BackupName != "" {
		spec["backupName"] = v.BackupName
	}
	if v.ScheduleName != "" {
		spec["scheduleName"] = v.ScheduleName
	}
	if len(v.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = v.IncludedNamespaces
	}
	if len(v.ExcludedNamespaces) > 0 {
		spec["excludedNamespaces"] = v.ExcludedNamespaces
	}
	if len(v.NamespaceMapping) > 0 {
		spec["namespaceMapping"] = v.NamespaceMapping
	}
	if v.RestorePVs != nil {
		spec["restorePVs"] = *v.RestorePVs
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "Restore",
			"metadata": map[string]any{
				"name":      v.Name,
				"namespace": v.Namespace,
			},
			"spec": spec,
		},
	}
}

// ToYAML implements WizardInput by marshaling the unstructured Restore.
func (v *VeleroRestoreInput) ToYAML() (string, error) {
	obj := v.ToRestore()
	yamlBytes, err := sigsyaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

// VeleroScheduleInput represents the wizard form data for creating a Velero Schedule.
type VeleroScheduleInput struct {
	Name               string   `json:"name"`
	Namespace          string   `json:"namespace"`
	Schedule           string   `json:"schedule"` // Cron expression
	Paused             bool     `json:"paused,omitempty"`
	IncludedNamespaces []string `json:"includedNamespaces,omitempty"`
	ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`
	StorageLocation    string   `json:"storageLocation,omitempty"`
	TTL                string   `json:"ttl,omitempty"`
	SnapshotVolumes    *bool    `json:"snapshotVolumes,omitempty"`
}

// Validate checks the VeleroScheduleInput and returns field-level errors.
func (v *VeleroScheduleInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(v.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if v.Namespace == "" {
		v.Namespace = "velero"
	} else if !dnsLabelRegex.MatchString(v.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if v.Schedule == "" {
		errs = append(errs, FieldError{Field: "schedule", Message: "cron schedule is required"})
	} else if _, err := cron.ParseStandard(v.Schedule); err != nil {
		errs = append(errs, FieldError{Field: "schedule", Message: "must be a valid cron expression (e.g., '0 * * * *' for hourly)"})
	}

	return errs
}

// ToSchedule converts the input to an unstructured Velero Schedule resource.
func (v *VeleroScheduleInput) ToSchedule() *unstructured.Unstructured {
	template := map[string]any{}
	if len(v.IncludedNamespaces) > 0 {
		template["includedNamespaces"] = v.IncludedNamespaces
	}
	if len(v.ExcludedNamespaces) > 0 {
		template["excludedNamespaces"] = v.ExcludedNamespaces
	}
	if v.StorageLocation != "" {
		template["storageLocation"] = v.StorageLocation
	}
	if v.TTL != "" {
		template["ttl"] = v.TTL
	}
	if v.SnapshotVolumes != nil {
		template["snapshotVolumes"] = *v.SnapshotVolumes
	}

	spec := map[string]any{
		"schedule": v.Schedule,
	}
	if v.Paused {
		spec["paused"] = true
	}
	if len(template) > 0 {
		spec["template"] = template
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "Schedule",
			"metadata": map[string]any{
				"name":      v.Name,
				"namespace": v.Namespace,
			},
			"spec": spec,
		},
	}
}

// ToYAML implements WizardInput by marshaling the unstructured Schedule.
func (v *VeleroScheduleInput) ToYAML() (string, error) {
	obj := v.ToSchedule()
	yamlBytes, err := sigsyaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
