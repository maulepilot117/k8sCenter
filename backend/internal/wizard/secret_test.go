package wizard

import (
	"strings"
	"testing"
)

func TestSecretInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      SecretInput
		wantErrors int
		wantFields []string
	}{
		{
			name:       "valid opaque",
			input:      SecretInput{Name: "my-secret", Namespace: "default", Type: "Opaque", Data: map[string]string{"key": "val"}},
			wantErrors: 0,
		},
		{
			name: "valid tls",
			input: SecretInput{Name: "tls-cert", Namespace: "default", Type: "kubernetes.io/tls",
				Data: map[string]string{"tls.crt": "cert-data", "tls.key": "key-data"}},
			wantErrors: 0,
		},
		{
			name: "valid basic-auth",
			input: SecretInput{Name: "creds", Namespace: "default", Type: "kubernetes.io/basic-auth",
				Data: map[string]string{"username": "admin", "password": "pass"}},
			wantErrors: 0,
		},
		{
			name: "valid dockerconfigjson",
			input: SecretInput{Name: "registry", Namespace: "default", Type: "kubernetes.io/dockerconfigjson",
				Data: map[string]string{".dockerconfigjson": "{}"}},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      SecretInput{Namespace: "default", Type: "Opaque"},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      SecretInput{Name: "s", Type: "Opaque"},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "invalid type",
			input:      SecretInput{Name: "s", Namespace: "default", Type: "InvalidType"},
			wantErrors: 1, wantFields: []string{"type"},
		},
		{
			name:       "tls missing cert",
			input:      SecretInput{Name: "s", Namespace: "default", Type: "kubernetes.io/tls", Data: map[string]string{"tls.key": "k"}},
			wantErrors: 1, wantFields: []string{"data.tls.crt"},
		},
		{
			name:       "tls missing key",
			input:      SecretInput{Name: "s", Namespace: "default", Type: "kubernetes.io/tls", Data: map[string]string{"tls.crt": "c"}},
			wantErrors: 1, wantFields: []string{"data.tls.key"},
		},
		{
			name:       "basic-auth missing username",
			input:      SecretInput{Name: "s", Namespace: "default", Type: "kubernetes.io/basic-auth", Data: map[string]string{"password": "p"}},
			wantErrors: 1, wantFields: []string{"data.username"},
		},
		{
			name:       "dockerconfigjson missing config",
			input:      SecretInput{Name: "s", Namespace: "default", Type: "kubernetes.io/dockerconfigjson", Data: map[string]string{}},
			wantErrors: 1, wantFields: []string{"data..dockerconfigjson"},
		},
		{
			name: "data too large",
			input: SecretInput{Name: "big", Namespace: "default", Type: "Opaque",
				Data: map[string]string{"large": strings.Repeat("x", 1<<20+1)}},
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

func TestSecretInputToYAML(t *testing.T) {
	input := SecretInput{
		Name: "db-creds", Namespace: "prod", Type: "Opaque",
		Data: map[string]string{"password": "secret123"},
	}
	yaml, err := input.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	if !strings.Contains(yaml, "kind: Secret") {
		t.Error("expected kind: Secret")
	}
	if !strings.Contains(yaml, "type: Opaque") {
		t.Error("expected type: Opaque")
	}
	// Verify values are UNMASKED in preview
	if !strings.Contains(yaml, "password: secret123") {
		t.Error("expected unmasked password in stringData")
	}
}
