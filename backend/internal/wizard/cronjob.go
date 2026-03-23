package wizard

import (
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CronJobInput represents the wizard form data for creating a CronJob.
type CronJobInput struct {
	Name                       string         `json:"name"`
	Namespace                  string         `json:"namespace"`
	Schedule                   string         `json:"schedule"`
	Container                  ContainerInput `json:"container"`
	RestartPolicy              string         `json:"restartPolicy"`
	ConcurrencyPolicy          string         `json:"concurrencyPolicy"`
	SuccessfulJobsHistoryLimit *int32         `json:"successfulJobsHistoryLimit,omitempty"`
	FailedJobsHistoryLimit     *int32         `json:"failedJobsHistoryLimit,omitempty"`
	Suspend                    bool           `json:"suspend,omitempty"`
}

// Validate checks the CronJobInput and returns field-level errors.
func (c *CronJobInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(c.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if c.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(c.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	schedule := strings.TrimSpace(c.Schedule)
	if schedule == "" {
		errs = append(errs, FieldError{Field: "schedule", Message: "is required"})
	} else if !cronRegex.MatchString(schedule) {
		errs = append(errs, FieldError{Field: "schedule", Message: "must be a valid 5-field cron expression (e.g. '0 * * * *')"})
	}

	// Validate container fields
	errs = append(errs, c.Container.ValidateContainer("container")...)

	// RestartPolicy
	switch c.RestartPolicy {
	case "Never", "OnFailure":
		// valid
	case "":
		errs = append(errs, FieldError{Field: "restartPolicy", Message: "is required (Never or OnFailure)"})
	default:
		errs = append(errs, FieldError{Field: "restartPolicy", Message: "must be Never or OnFailure"})
	}

	// ConcurrencyPolicy
	switch c.ConcurrencyPolicy {
	case "Allow", "Forbid", "Replace":
		// valid
	case "":
		errs = append(errs, FieldError{Field: "concurrencyPolicy", Message: "is required (Allow, Forbid, or Replace)"})
	default:
		errs = append(errs, FieldError{Field: "concurrencyPolicy", Message: "must be Allow, Forbid, or Replace"})
	}

	// History limits
	if c.SuccessfulJobsHistoryLimit != nil && *c.SuccessfulJobsHistoryLimit < 0 {
		errs = append(errs, FieldError{Field: "successfulJobsHistoryLimit", Message: "must be >= 0"})
	}
	if c.FailedJobsHistoryLimit != nil && *c.FailedJobsHistoryLimit < 0 {
		errs = append(errs, FieldError{Field: "failedJobsHistoryLimit", Message: "must be >= 0"})
	}

	return errs
}

// ToCronJob converts the wizard input to a typed Kubernetes CronJob.
func (c *CronJobInput) ToCronJob() (*batchv1.CronJob, error) {
	container, err := c.Container.BuildContainer(c.Name)
	if err != nil {
		return nil, err
	}

	lbls := map[string]string{
		"app": c.Name,
	}

	cronJob := &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name,
			Namespace: c.Namespace,
			Labels:    lbls,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:          strings.TrimSpace(c.Schedule),
			ConcurrencyPolicy: batchv1.ConcurrencyPolicy(c.ConcurrencyPolicy),
			Suspend:           &c.Suspend,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: lbls,
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: lbls,
						},
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{container},
							RestartPolicy: corev1.RestartPolicy(c.RestartPolicy),
						},
					},
				},
			},
		},
	}

	if c.SuccessfulJobsHistoryLimit != nil {
		cronJob.Spec.SuccessfulJobsHistoryLimit = c.SuccessfulJobsHistoryLimit
	}
	if c.FailedJobsHistoryLimit != nil {
		cronJob.Spec.FailedJobsHistoryLimit = c.FailedJobsHistoryLimit
	}

	return cronJob, nil
}

// ToYAML implements WizardInput by converting to a CronJob and marshaling to YAML.
func (c *CronJobInput) ToYAML() (string, error) {
	cronJob, err := c.ToCronJob()
	if err != nil {
		return "", err
	}
	yamlBytes, err := sigsyaml.Marshal(cronJob)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}
