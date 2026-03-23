package wizard

import (
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestPDBInputValidate(t *testing.T) {
	validSelector := map[string]string{"app": "myapp"}

	tests := []struct {
		name       string
		input      PDBInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid minAvailable number",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("2"),
			},
			wantErrors: 0,
		},
		{
			name: "valid minAvailable percentage",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("50%"),
			},
			wantErrors: 0,
		},
		{
			name: "valid maxUnavailable number",
			input: PDBInput{
				Name:           "my-pdb",
				Namespace:      "default",
				Selector:       validSelector,
				MaxUnavailable: strPtr("1"),
			},
			wantErrors: 0,
		},
		{
			name: "valid maxUnavailable percentage",
			input: PDBInput{
				Name:           "my-pdb",
				Namespace:      "default",
				Selector:       validSelector,
				MaxUnavailable: strPtr("25%"),
			},
			wantErrors: 0,
		},
		{
			name: "missing name",
			input: PDBInput{
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "missing namespace",
			input: PDBInput{
				Name:         "my-pdb",
				Selector:     validSelector,
				MinAvailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name: "empty selector",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     map[string]string{},
				MinAvailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"selector"},
		},
		{
			name: "nil selector",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     nil,
				MinAvailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"selector"},
		},
		{
			name: "both minAvailable and maxUnavailable set",
			input: PDBInput{
				Name:           "my-pdb",
				Namespace:      "default",
				Selector:       validSelector,
				MinAvailable:   strPtr("1"),
				MaxUnavailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
		{
			name: "neither minAvailable nor maxUnavailable set",
			input: PDBInput{
				Name:      "my-pdb",
				Namespace: "default",
				Selector:  validSelector,
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
		{
			name: "invalid value abc",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("abc"),
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
		{
			name: "negative value",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("-1"),
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
		{
			name: "invalid name uppercase",
			input: PDBInput{
				Name:         "MyPDB",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name: "invalid namespace",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "INVALID",
				Selector:     validSelector,
				MinAvailable: strPtr("1"),
			},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name: "zero is valid for minAvailable",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("0"),
			},
			wantErrors: 0,
		},
		{
			name: "zero percent is valid",
			input: PDBInput{
				Name:           "my-pdb",
				Namespace:      "default",
				Selector:       validSelector,
				MaxUnavailable: strPtr("0%"),
			},
			wantErrors: 0,
		},
		{
			name: "percentage over 100 is invalid",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("200%"),
			},
			wantErrors: 1, wantFields: []string{"minAvailable"},
		},
		{
			name: "100 percent is valid",
			input: PDBInput{
				Name:         "my-pdb",
				Namespace:    "default",
				Selector:     validSelector,
				MinAvailable: strPtr("100%"),
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

func TestPDBInputToYAML(t *testing.T) {
	t.Run("minAvailable integer", func(t *testing.T) {
		input := PDBInput{
			Name:         "my-pdb",
			Namespace:    "prod",
			Selector:     map[string]string{"app": "myapp"},
			MinAvailable: strPtr("2"),
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "kind: PodDisruptionBudget") {
			t.Errorf("expected kind: PodDisruptionBudget, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, "name: my-pdb") {
			t.Errorf("expected name: my-pdb, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, "app: myapp") {
			t.Errorf("expected app: myapp in matchLabels, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, "minAvailable: 2") {
			t.Errorf("expected minAvailable: 2, got:\n%s", yaml)
		}
		if !strings.Contains(yaml, "policy/v1") {
			t.Errorf("expected apiVersion policy/v1, got:\n%s", yaml)
		}
	})

	t.Run("maxUnavailable percentage", func(t *testing.T) {
		input := PDBInput{
			Name:           "my-pdb",
			Namespace:      "default",
			Selector:       map[string]string{"tier": "frontend"},
			MaxUnavailable: strPtr("50%"),
		}
		yaml, err := input.ToYAML()
		if err != nil {
			t.Fatalf("ToYAML: %v", err)
		}
		if !strings.Contains(yaml, "maxUnavailable: 50%") {
			t.Errorf("expected maxUnavailable: 50%%, got:\n%s", yaml)
		}
	})
}
