package wizard

import (
	"strings"
	"testing"
)

func TestStatefulSetInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      StatefulSetInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid statefulset with VCTs",
			input: StatefulSetInput{
				Name:        "postgres",
				Namespace:   "default",
				ServiceName: "postgres-headless",
				Replicas:    3,
				Container:   ContainerInput{Image: "postgres:16"},
				VolumeClaimTemplates: []VolumeClaimTemplate{
					{Name: "data", Size: "10Gi", AccessMode: "ReadWriteOnce"},
				},
			},
			wantErrors: 0,
		},
		{
			name: "missing serviceName",
			input: StatefulSetInput{
				Name:      "my-sts",
				Namespace: "default",
				Replicas:  1,
				Container: ContainerInput{Image: "redis:7"},
			},
			wantErrors: 1, wantFields: []string{"serviceName"},
		},
		{
			name: "negative replicas",
			input: StatefulSetInput{
				Name:        "my-sts",
				Namespace:   "default",
				ServiceName: "my-svc",
				Replicas:    -1,
				Container:   ContainerInput{Image: "redis:7"},
			},
			wantErrors: 1, wantFields: []string{"replicas"},
		},
		{
			name: "replicas over 1000",
			input: StatefulSetInput{
				Name:        "my-sts",
				Namespace:   "default",
				ServiceName: "my-svc",
				Replicas:    1001,
				Container:   ContainerInput{Image: "redis:7"},
			},
			wantErrors: 1, wantFields: []string{"replicas"},
		},
		{
			name: "VCT invalid size",
			input: StatefulSetInput{
				Name:        "my-sts",
				Namespace:   "default",
				ServiceName: "my-svc",
				Replicas:    1,
				Container:   ContainerInput{Image: "redis:7"},
				VolumeClaimTemplates: []VolumeClaimTemplate{
					{Name: "data", Size: "not-a-quantity", AccessMode: "ReadWriteOnce"},
				},
			},
			wantErrors: 1, wantFields: []string{"volumeClaimTemplates[0].size"},
		},
		{
			name: "VCT duplicate name",
			input: StatefulSetInput{
				Name:        "my-sts",
				Namespace:   "default",
				ServiceName: "my-svc",
				Replicas:    1,
				Container:   ContainerInput{Image: "redis:7"},
				VolumeClaimTemplates: []VolumeClaimTemplate{
					{Name: "data", Size: "10Gi", AccessMode: "ReadWriteOnce"},
					{Name: "data", Size: "5Gi", AccessMode: "ReadWriteOnce"},
				},
			},
			wantErrors: 1, wantFields: []string{"volumeClaimTemplates[1].name"},
		},
		{
			name: "VCT invalid accessMode",
			input: StatefulSetInput{
				Name:        "my-sts",
				Namespace:   "default",
				ServiceName: "my-svc",
				Replicas:    1,
				Container:   ContainerInput{Image: "redis:7"},
				VolumeClaimTemplates: []VolumeClaimTemplate{
					{Name: "data", Size: "10Gi", AccessMode: "InvalidMode"},
				},
			},
			wantErrors: 1, wantFields: []string{"volumeClaimTemplates[0].accessMode"},
		},
		{
			name: "invalid podManagementPolicy",
			input: StatefulSetInput{
				Name:                "my-sts",
				Namespace:           "default",
				ServiceName:         "my-svc",
				Replicas:            1,
				Container:           ContainerInput{Image: "redis:7"},
				PodManagementPolicy: "Random",
			},
			wantErrors: 1, wantFields: []string{"podManagementPolicy"},
		},
		{
			name: "valid podManagementPolicy Parallel",
			input: StatefulSetInput{
				Name:                "my-sts",
				Namespace:           "default",
				ServiceName:         "my-svc",
				Replicas:            2,
				Container:           ContainerInput{Image: "redis:7"},
				PodManagementPolicy: "Parallel",
			},
			wantErrors: 0,
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

func TestStatefulSetInputToYAML(t *testing.T) {
	input := StatefulSetInput{
		Name:        "redis-cluster",
		Namespace:   "cache",
		ServiceName: "redis-headless",
		Replicas:    3,
		Container:   ContainerInput{Image: "redis:7-alpine"},
		VolumeClaimTemplates: []VolumeClaimTemplate{
			{Name: "data", StorageClassName: "fast-ssd", Size: "20Gi", AccessMode: "ReadWriteOnce"},
		},
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: StatefulSet") {
		t.Error("expected kind: StatefulSet")
	}
	if !strings.Contains(yaml, "serviceName: redis-headless") {
		t.Error("expected serviceName in output")
	}
	if !strings.Contains(yaml, "name: data") {
		t.Error("expected VCT name in output")
	}
	if !strings.Contains(yaml, "storage: 20Gi") {
		t.Error("expected VCT storage size in output")
	}
}
