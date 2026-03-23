package wizard

import (
	sigsyaml "sigs.k8s.io/yaml"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobInput represents the wizard form data for creating a Job.
type JobInput struct {
	Name                  string         `json:"name"`
	Namespace             string         `json:"namespace"`
	Container             ContainerInput `json:"container"`
	RestartPolicy         string         `json:"restartPolicy,omitempty"`
	Completions           *int32         `json:"completions,omitempty"`
	Parallelism           *int32         `json:"parallelism,omitempty"`
	BackoffLimit          *int32         `json:"backoffLimit,omitempty"`
	ActiveDeadlineSeconds *int64         `json:"activeDeadlineSeconds,omitempty"`
}

// Validate checks the JobInput and returns field-level errors.
func (j *JobInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(j.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if j.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(j.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	// Validate restart policy
	rp := j.RestartPolicy
	if rp == "" {
		rp = "Never"
	}
	if rp != "Never" && rp != "OnFailure" {
		errs = append(errs, FieldError{Field: "restartPolicy", Message: "must be Never or OnFailure"})
	}

	// Validate optional numeric fields
	if j.Completions != nil && *j.Completions < 0 {
		errs = append(errs, FieldError{Field: "completions", Message: "must be non-negative"})
	}
	if j.Parallelism != nil && *j.Parallelism < 0 {
		errs = append(errs, FieldError{Field: "parallelism", Message: "must be non-negative"})
	}
	if j.BackoffLimit != nil && *j.BackoffLimit < 0 {
		errs = append(errs, FieldError{Field: "backoffLimit", Message: "must be non-negative"})
	}
	if j.ActiveDeadlineSeconds != nil && *j.ActiveDeadlineSeconds < 1 {
		errs = append(errs, FieldError{Field: "activeDeadlineSeconds", Message: "must be at least 1"})
	}

	// Delegate container validation
	errs = append(errs, j.Container.ValidateContainer("container")...)

	return errs
}

// ToYAML implements WizardInput by converting to a Job and marshaling to YAML.
func (j *JobInput) ToYAML() (string, error) {
	job, err := j.ToJob()
	if err != nil {
		return "", err
	}
	yamlBytes, err := sigsyaml.Marshal(job)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

// ToJob converts the wizard input to a typed Kubernetes Job.
// Validate() should be called before ToJob() to ensure inputs are well-formed.
func (j *JobInput) ToJob() (*batchv1.Job, error) {
	lbls := map[string]string{
		"app": j.Name,
	}

	container, err := j.Container.BuildContainer(j.Name)
	if err != nil {
		return nil, err
	}

	restartPolicy := corev1.RestartPolicyNever
	if j.RestartPolicy == "OnFailure" {
		restartPolicy = corev1.RestartPolicyOnFailure
	}

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      j.Name,
			Namespace: j.Namespace,
			Labels:    lbls,
		},
		Spec: batchv1.JobSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: lbls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: lbls,
				},
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{container},
					RestartPolicy: restartPolicy,
				},
			},
		},
	}

	if j.Completions != nil {
		job.Spec.Completions = j.Completions
	}
	if j.Parallelism != nil {
		job.Spec.Parallelism = j.Parallelism
	}
	if j.BackoffLimit != nil {
		job.Spec.BackoffLimit = j.BackoffLimit
	}
	if j.ActiveDeadlineSeconds != nil {
		job.Spec.ActiveDeadlineSeconds = j.ActiveDeadlineSeconds
	}

	return job, nil
}
