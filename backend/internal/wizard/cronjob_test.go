package wizard

import (
	"strings"
	"testing"
)

func TestCronJobInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      CronJobInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid cronjob all fields",
			input: CronJobInput{
				Name:              "nightly-backup",
				Namespace:         "default",
				Schedule:          "0 2 * * *",
				Container:         ContainerInput{Image: "backup:latest"},
				RestartPolicy:     "Never",
				ConcurrencyPolicy: "Forbid",
			},
			wantErrors: 0,
		},
		{
			name: "missing schedule",
			input: CronJobInput{
				Name:              "my-cron",
				Namespace:         "default",
				Container:         ContainerInput{Image: "busybox"},
				RestartPolicy:     "Never",
				ConcurrencyPolicy: "Allow",
			},
			wantErrors: 1, wantFields: []string{"schedule"},
		},
		{
			name: "invalid schedule not 5-field",
			input: CronJobInput{
				Name:              "my-cron",
				Namespace:         "default",
				Schedule:          "every day",
				Container:         ContainerInput{Image: "busybox"},
				RestartPolicy:     "Never",
				ConcurrencyPolicy: "Allow",
			},
			wantErrors: 1, wantFields: []string{"schedule"},
		},
		{
			name: "invalid concurrencyPolicy",
			input: CronJobInput{
				Name:              "my-cron",
				Namespace:         "default",
				Schedule:          "*/5 * * * *",
				Container:         ContainerInput{Image: "busybox"},
				RestartPolicy:     "Never",
				ConcurrencyPolicy: "Invalid",
			},
			wantErrors: 1, wantFields: []string{"concurrencyPolicy"},
		},
		{
			name: "empty concurrencyPolicy",
			input: CronJobInput{
				Name:          "my-cron",
				Namespace:     "default",
				Schedule:      "*/5 * * * *",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "Never",
			},
			wantErrors: 1, wantFields: []string{"concurrencyPolicy"},
		},
		{
			name: "invalid restartPolicy Always",
			input: CronJobInput{
				Name:              "my-cron",
				Namespace:         "default",
				Schedule:          "0 * * * *",
				Container:         ContainerInput{Image: "busybox"},
				RestartPolicy:     "Always",
				ConcurrencyPolicy: "Allow",
			},
			wantErrors: 1, wantFields: []string{"restartPolicy"},
		},
		{
			name: "empty restartPolicy required",
			input: CronJobInput{
				Name:              "my-cron",
				Namespace:         "default",
				Schedule:          "0 * * * *",
				Container:         ContainerInput{Image: "busybox"},
				ConcurrencyPolicy: "Allow",
			},
			wantErrors: 1, wantFields: []string{"restartPolicy"},
		},
		{
			name: "negative successfulJobsHistoryLimit",
			input: CronJobInput{
				Name:                       "my-cron",
				Namespace:                  "default",
				Schedule:                   "0 * * * *",
				Container:                  ContainerInput{Image: "busybox"},
				RestartPolicy:              "Never",
				ConcurrencyPolicy:          "Allow",
				SuccessfulJobsHistoryLimit: int32Ptr(-1),
			},
			wantErrors: 1, wantFields: []string{"successfulJobsHistoryLimit"},
		},
		{
			name: "negative failedJobsHistoryLimit",
			input: CronJobInput{
				Name:                   "my-cron",
				Namespace:              "default",
				Schedule:               "0 * * * *",
				Container:              ContainerInput{Image: "busybox"},
				RestartPolicy:          "Never",
				ConcurrencyPolicy:      "Allow",
				FailedJobsHistoryLimit: int32Ptr(-3),
			},
			wantErrors: 1, wantFields: []string{"failedJobsHistoryLimit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if len(errs) != tt.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrors, len(errs), errs)
			}
			for _, wf := range tt.wantFields {
				found := false
				for _, e := range errs {
					if e.Field == wf {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, not found in %v", wf, errs)
				}
			}
		})
	}
}

func TestCronJobInputToYAML(t *testing.T) {
	input := CronJobInput{
		Name:              "hourly-sync",
		Namespace:         "jobs",
		Schedule:          "0 * * * *",
		Container:         ContainerInput{Image: "sync:v2"},
		RestartPolicy:     "OnFailure",
		ConcurrencyPolicy: "Replace",
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: CronJob") {
		t.Error("expected kind: CronJob")
	}
	if !strings.Contains(yaml, "0 * * * *") {
		t.Error("expected schedule in output")
	}
	if !strings.Contains(yaml, "name: hourly-sync") {
		t.Error("expected name: hourly-sync")
	}
}
