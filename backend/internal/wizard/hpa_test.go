package wizard

import (
	"strings"
	"testing"
)

func int32Ptr(v int32) *int32 { return &v }

func TestHPAInputValidate(t *testing.T) {
	validMetric := HPAMetricInput{
		Type:               "Resource",
		ResourceName:       "cpu",
		TargetType:         "Utilization",
		TargetAverageValue: 80,
	}

	tests := []struct {
		name       string
		input      HPAInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid cpu utilization",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 10,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 0,
		},
		{
			name: "valid memory averagevalue",
			input: HPAInput{
				Name:        "mem-hpa",
				Namespace:   "default",
				TargetKind:  "StatefulSet",
				TargetName:  "my-sts",
				MinReplicas: int32Ptr(2),
				MaxReplicas: 8,
				Metrics: []HPAMetricInput{
					{
						Type:               "Resource",
						ResourceName:       "memory",
						TargetType:         "AverageValue",
						TargetAverageValue: 512,
					},
				},
			},
			wantErrors: 0,
		},
		{
			name: "valid replicaset target",
			input: HPAInput{
				Name:        "rs-hpa",
				Namespace:   "default",
				TargetKind:  "ReplicaSet",
				TargetName:  "my-rs",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 0,
		},
		{
			name: "missing name",
			input: HPAInput{
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "invalid name uppercase",
			input: HPAInput{
				Name:        "MyHPA",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "missing namespace",
			input: HPAInput{
				Name:        "my-hpa",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name: "invalid namespace",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "UPPER",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name: "invalid targetKind pod",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Pod",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"targetKind"},
		},
		{
			name: "invalid targetKind empty",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"targetKind"},
		},
		{
			name: "missing targetName",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"targetName"},
		},
		{
			name: "invalid targetName",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "INVALID",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"targetName"},
		},
		{
			name: "maxReplicas zero",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 0,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"maxReplicas"},
		},
		{
			name: "maxReplicas over 1000",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 1001,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"maxReplicas"},
		},
		{
			name: "maxReplicas exactly 1000 is valid",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 1000,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 0,
		},
		{
			name: "minReplicas zero",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MinReplicas: int32Ptr(0),
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"minReplicas"},
		},
		{
			name: "minReplicas exceeds maxReplicas",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MinReplicas: int32Ptr(10),
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{validMetric},
			},
			wantErrors: 1, wantFields: []string{"minReplicas"},
		},
		{
			name: "no metrics",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics:     []HPAMetricInput{},
			},
			wantErrors: 1, wantFields: []string{"metrics"},
		},
		{
			name: "nil metrics",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
			},
			wantErrors: 1, wantFields: []string{"metrics"},
		},
		{
			name: "invalid metric type",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics: []HPAMetricInput{
					{
						Type:               "External",
						ResourceName:       "cpu",
						TargetType:         "Utilization",
						TargetAverageValue: 80,
					},
				},
			},
			wantErrors: 1, wantFields: []string{"metrics[0].type"},
		},
		{
			name: "invalid resource name",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics: []HPAMetricInput{
					{
						Type:               "Resource",
						ResourceName:       "disk",
						TargetType:         "Utilization",
						TargetAverageValue: 80,
					},
				},
			},
			wantErrors: 1, wantFields: []string{"metrics[0].resourceName"},
		},
		{
			name: "invalid targetType",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics: []HPAMetricInput{
					{
						Type:               "Resource",
						ResourceName:       "cpu",
						TargetType:         "Value",
						TargetAverageValue: 80,
					},
				},
			},
			wantErrors: 1, wantFields: []string{"metrics[0].targetType"},
		},
		{
			name: "targetAverageValue zero",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics: []HPAMetricInput{
					{
						Type:               "Resource",
						ResourceName:       "cpu",
						TargetType:         "Utilization",
						TargetAverageValue: 0,
					},
				},
			},
			wantErrors: 1, wantFields: []string{"metrics[0].targetAverageValue"},
		},
		{
			name: "utilization over 100 percent",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics: []HPAMetricInput{
					{
						Type:               "Resource",
						ResourceName:       "cpu",
						TargetType:         "Utilization",
						TargetAverageValue: 150,
					},
				},
			},
			wantErrors: 1, wantFields: []string{"metrics[0].targetAverageValue"},
		},
		{
			name: "utilization exactly 100 is valid",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics: []HPAMetricInput{
					{
						Type:               "Resource",
						ResourceName:       "cpu",
						TargetType:         "Utilization",
						TargetAverageValue: 100,
					},
				},
			},
			wantErrors: 0,
		},
		{
			name: "averagevalue over 100 is valid (not a percentage)",
			input: HPAInput{
				Name:        "my-hpa",
				Namespace:   "default",
				TargetKind:  "Deployment",
				TargetName:  "my-app",
				MaxReplicas: 5,
				Metrics: []HPAMetricInput{
					{
						Type:               "Resource",
						ResourceName:       "memory",
						TargetType:         "AverageValue",
						TargetAverageValue: 500,
					},
				},
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

func TestHPAInputToYAML(t *testing.T) {
	t.Run("cpu utilization deployment", func(t *testing.T) {
		input := HPAInput{
			Name:        "app-hpa",
			Namespace:   "production",
			TargetKind:  "Deployment",
			TargetName:  "my-app",
			MinReplicas: int32Ptr(2),
			MaxReplicas: 10,
			Metrics: []HPAMetricInput{
				{
					Type:               "Resource",
					ResourceName:       "cpu",
					TargetType:         "Utilization",
					TargetAverageValue: 75,
				},
			},
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "kind: HorizontalPodAutoscaler") {
			t.Error("expected kind: HorizontalPodAutoscaler")
		}
		if !strings.Contains(yaml, "autoscaling/v2") {
			t.Error("expected apiVersion autoscaling/v2")
		}
		if !strings.Contains(yaml, "name: app-hpa") {
			t.Error("expected name: app-hpa")
		}
		if !strings.Contains(yaml, "kind: Deployment") {
			t.Error("expected scaleTargetRef kind: Deployment")
		}
		if !strings.Contains(yaml, "name: my-app") {
			t.Error("expected scaleTargetRef name: my-app")
		}
		if !strings.Contains(yaml, "apps/v1") {
			t.Error("expected scaleTargetRef apiVersion apps/v1")
		}
		if !strings.Contains(yaml, "cpu") {
			t.Error("expected cpu resource metric")
		}
	})

	t.Run("memory averagevalue statefulset", func(t *testing.T) {
		input := HPAInput{
			Name:        "sts-hpa",
			Namespace:   "default",
			TargetKind:  "StatefulSet",
			TargetName:  "db",
			MaxReplicas: 5,
			Metrics: []HPAMetricInput{
				{
					Type:               "Resource",
					ResourceName:       "memory",
					TargetType:         "AverageValue",
					TargetAverageValue: 256,
				},
			},
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "kind: StatefulSet") {
			t.Error("expected scaleTargetRef kind: StatefulSet")
		}
		if !strings.Contains(yaml, "memory") {
			t.Error("expected memory resource metric")
		}
	})
}
