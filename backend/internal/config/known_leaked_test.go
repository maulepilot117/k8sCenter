package config

import "testing"

func TestIsKnownLeakedSecret(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		leaked bool
	}{
		{
			name:   "JWT secret is leaked",
			input:  "homelab-jwt-secret-for-k8scenter-minimum-32-bytes",
			leaked: true,
		},
		{
			name:   "setup token is leaked",
			input:  "homelab-setup-token",
			leaked: true,
		},
		{
			name:   "postgres password is leaked",
			input:  "k8sC3nterDB2026",
			leaked: true,
		},
		{
			name:   "safe secret is not leaked",
			input:  "my-very-safe-random-secret-that-is-not-in-the-list",
			leaked: false,
		},
		{
			name:   "empty string is not leaked",
			input:  "",
			leaked: false,
		},
		{
			name:   "partial match is not leaked",
			input:  "homelab-jwt-secret",
			leaked: false,
		},
		{
			name:   "case mismatch is not leaked",
			input:  "HOMELAB-JWT-SECRET-FOR-K8SCENTER-MINIMUM-32-BYTES",
			leaked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsKnownLeakedSecret(tt.input)
			if got != tt.leaked {
				t.Errorf("IsKnownLeakedSecret(%q) = %v, want %v", tt.input, got, tt.leaked)
			}
		})
	}
}

func TestKnownLeakedSecretsSlice(t *testing.T) {
	// Ensure all three expected strings are present so a future edit
	// that removes one is caught immediately.
	expected := map[string]bool{
		"homelab-jwt-secret-for-k8scenter-minimum-32-bytes": false,
		"homelab-setup-token": false,
		"k8sC3nterDB2026":     false,
	}
	for _, s := range KnownLeakedSecrets {
		if _, ok := expected[s]; ok {
			expected[s] = true
		}
	}
	for s, found := range expected {
		if !found {
			t.Errorf("expected leaked secret %q not found in KnownLeakedSecrets", s)
		}
	}
}
