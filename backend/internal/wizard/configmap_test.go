package wizard

import (
	"strings"
	"testing"
)

func TestConfigMapInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      ConfigMapInput
		wantErrors int
		wantFields []string
	}{
		{
			name:       "valid",
			input:      ConfigMapInput{Name: "my-config", Namespace: "default", Data: map[string]string{"key": "value"}},
			wantErrors: 0,
		},
		{
			name:       "valid empty data",
			input:      ConfigMapInput{Name: "empty", Namespace: "default", Data: map[string]string{}},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      ConfigMapInput{Namespace: "default", Data: map[string]string{"k": "v"}},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "invalid name",
			input:      ConfigMapInput{Name: "UPPER", Namespace: "default"},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      ConfigMapInput{Name: "cfg"},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "invalid data key",
			input:      ConfigMapInput{Name: "cfg", Namespace: "default", Data: map[string]string{"bad key!": "v"}},
			wantErrors: 1, wantFields: []string{"data[bad key!]"},
		},
		{
			name: "data too large",
			input: ConfigMapInput{
				Name: "big", Namespace: "default",
				Data: map[string]string{"large": strings.Repeat("x", 1<<20+1)},
			},
			wantErrors: 1, wantFields: []string{"data"},
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

func TestConfigMapInputToYAML(t *testing.T) {
	input := ConfigMapInput{
		Name: "app-config", Namespace: "prod",
		Data: map[string]string{"DB_HOST": "postgres", "DB_PORT": "5432"},
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: ConfigMap") {
		t.Error("expected kind: ConfigMap")
	}
	if !strings.Contains(yaml, "name: app-config") {
		t.Error("expected name: app-config")
	}
	if !strings.Contains(yaml, "DB_HOST: postgres") {
		t.Error("expected DB_HOST in data")
	}
}
