package wizard

import (
	"strings"
	"testing"
)

func TestNamespaceLimitsInput_Validate(t *testing.T) {
	tests := []struct {
		name       string
		input      NamespaceLimitsInput
		wantErrors []string
	}{
		{
			name: "valid input",
			input: NamespaceLimitsInput{
				Namespace:      "default",
				QuotaName:      "my-quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:    "8",
					MemoryHard: "16Gi",
					PodsHard:   20,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: nil,
		},
		{
			name: "missing namespace",
			input: NamespaceLimitsInput{
				QuotaName:      "my-quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:    "8",
					MemoryHard: "16Gi",
					PodsHard:   20,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: []string{"namespace"},
		},
		{
			name: "invalid quota name",
			input: NamespaceLimitsInput{
				Namespace:      "default",
				QuotaName:      "My-Quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:    "8",
					MemoryHard: "16Gi",
					PodsHard:   20,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: []string{"quotaName"},
		},
		{
			name: "invalid CPU quantity",
			input: NamespaceLimitsInput{
				Namespace:      "default",
				QuotaName:      "my-quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:    "not-a-number",
					MemoryHard: "16Gi",
					PodsHard:   20,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: []string{"quota.cpuHard"},
		},
		{
			name: "missing CPU quota",
			input: NamespaceLimitsInput{
				Namespace:      "default",
				QuotaName:      "my-quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:    "",
					MemoryHard: "16Gi",
					PodsHard:   20,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: []string{"quota.cpuHard"},
		},
		{
			name: "pods must be at least 1",
			input: NamespaceLimitsInput{
				Namespace:      "default",
				QuotaName:      "my-quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:    "8",
					MemoryHard: "16Gi",
					PodsHard:   0,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: []string{"quota.podsHard"},
		},
		{
			name: "invalid warn threshold",
			input: NamespaceLimitsInput{
				Namespace:      "default",
				QuotaName:      "my-quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:       "8",
					MemoryHard:    "16Gi",
					PodsHard:      20,
					WarnThreshold: 150,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: []string{"quota.warnThreshold"},
		},
		{
			name: "missing container default",
			input: NamespaceLimitsInput{
				Namespace:      "default",
				QuotaName:      "my-quota",
				LimitRangeName: "my-limits",
				Quota: QuotaConfig{
					CPUHard:    "8",
					MemoryHard: "16Gi",
					PodsHard:   20,
				},
				Limits: LimitConfig{
					ContainerDefault:        ResourcePair{CPU: "", Memory: "256Mi"},
					ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
					ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
					ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
				},
			},
			wantErrors: []string{"limits.containerDefault.cpu"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()

			if len(tt.wantErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %v", errs)
				}
				return
			}

			for _, wantField := range tt.wantErrors {
				found := false
				for _, err := range errs {
					if err.Field == wantField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error for field %q, got errors: %v", wantField, errs)
				}
			}
		})
	}
}

func TestNamespaceLimitsInput_ToYAML(t *testing.T) {
	input := NamespaceLimitsInput{
		Namespace:      "production",
		QuotaName:      "prod-quota",
		LimitRangeName: "prod-limits",
		Quota: QuotaConfig{
			CPUHard:           "16",
			MemoryHard:        "32Gi",
			PodsHard:          50,
			WarnThreshold:     70,
			CriticalThreshold: 90,
		},
		Limits: LimitConfig{
			ContainerDefault:        ResourcePair{CPU: "500m", Memory: "512Mi"},
			ContainerDefaultRequest: ResourcePair{CPU: "250m", Memory: "256Mi"},
			ContainerMax:            ResourcePair{CPU: "4", Memory: "8Gi"},
			ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
		},
	}

	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML failed: %v", err)
	}

	// Check for expected content
	checks := []string{
		"kind: ResourceQuota",
		"name: prod-quota",
		"namespace: production",
		"cpu: \"16\"",
		"memory: 32Gi",
		"pods: \"50\"",
		"k8scenter.io/warn-threshold: \"70\"",
		"k8scenter.io/critical-threshold: \"90\"",
		"---",
		"kind: LimitRange",
		"name: prod-limits",
		"type: Container",
	}

	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("expected YAML to contain %q, got:\n%s", check, yaml)
		}
	}
}

func TestNamespaceLimitsInput_ToYAML_WithOptionals(t *testing.T) {
	input := NamespaceLimitsInput{
		Namespace:      "test",
		QuotaName:      "test-quota",
		LimitRangeName: "test-limits",
		Quota: QuotaConfig{
			CPUHard:        "8",
			MemoryHard:     "16Gi",
			PodsHard:       20,
			SecretsHard:    10,
			ConfigMapsHard: 20,
			ServicesHard:   5,
			PVCsHard:       3,
			GPUHard:        "2",
		},
		Limits: LimitConfig{
			ContainerDefault:        ResourcePair{CPU: "250m", Memory: "256Mi"},
			ContainerDefaultRequest: ResourcePair{CPU: "100m", Memory: "128Mi"},
			ContainerMax:            ResourcePair{CPU: "2", Memory: "4Gi"},
			ContainerMin:            ResourcePair{CPU: "10m", Memory: "8Mi"},
			PodMax:                  &ResourcePair{CPU: "8", Memory: "16Gi"},
			PVCMinStorage:           "1Gi",
			PVCMaxStorage:           "100Gi",
		},
	}

	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML failed: %v", err)
	}

	// Check for optional content
	checks := []string{
		"secrets:",
		"configmaps:",
		"services:",
		"persistentvolumeclaims:",
		"nvidia.com/gpu:",
		"type: Pod",
		"type: PersistentVolumeClaim",
	}

	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("expected YAML to contain %q, got:\n%s", check, yaml)
		}
	}
}
