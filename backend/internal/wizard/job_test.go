package wizard

import (
	"strings"
	"testing"
)

func int32Ptr(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64 { return &v }

func TestJobInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      JobInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid job",
			input: JobInput{
				Name:          "my-job",
				Namespace:     "default",
				Container:     ContainerInput{Image: "busybox:latest"},
				RestartPolicy: "Never",
			},
			wantErrors: 0,
		},
		{
			name: "missing name",
			input: JobInput{
				Namespace:     "default",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "Never",
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "missing namespace",
			input: JobInput{
				Name:          "my-job",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "Never",
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name: "invalid restartPolicy Always",
			input: JobInput{
				Name:          "my-job",
				Namespace:     "default",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "Always",
			},
			wantErrors: 1, wantFields: []string{"restartPolicy"},
		},
		{
			name: "empty restartPolicy defaults to Never",
			input: JobInput{
				Name:          "my-job",
				Namespace:     "default",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "",
			},
			wantErrors: 0,
		},
		{
			name: "negative completions",
			input: JobInput{
				Name:          "my-job",
				Namespace:     "default",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "Never",
				Completions:   int32Ptr(-1),
			},
			wantErrors: 1, wantFields: []string{"completions"},
		},
		{
			name: "negative parallelism",
			input: JobInput{
				Name:          "my-job",
				Namespace:     "default",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "Never",
				Parallelism:   int32Ptr(-5),
			},
			wantErrors: 1, wantFields: []string{"parallelism"},
		},
		{
			name: "negative backoffLimit",
			input: JobInput{
				Name:          "my-job",
				Namespace:     "default",
				Container:     ContainerInput{Image: "busybox"},
				RestartPolicy: "Never",
				BackoffLimit:  int32Ptr(-1),
			},
			wantErrors: 1, wantFields: []string{"backoffLimit"},
		},
		{
			name: "container validation empty image",
			input: JobInput{
				Name:          "my-job",
				Namespace:     "default",
				Container:     ContainerInput{Image: ""},
				RestartPolicy: "Never",
			},
			wantErrors: 1, wantFields: []string{"container.image"},
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

func TestJobInputToYAML(t *testing.T) {
	input := JobInput{
		Name:          "data-loader",
		Namespace:     "batch",
		Container:     ContainerInput{Image: "python:3.12"},
		RestartPolicy: "OnFailure",
		Completions:   int32Ptr(3),
		Parallelism:   int32Ptr(2),
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: Job") {
		t.Error("expected kind: Job")
	}
	if !strings.Contains(yaml, "batch/v1") {
		t.Error("expected apiVersion batch/v1")
	}
	if !strings.Contains(yaml, "restartPolicy: OnFailure") {
		t.Error("expected restartPolicy: OnFailure in output")
	}
	if !strings.Contains(yaml, "name: data-loader") {
		t.Error("expected name: data-loader")
	}
}
