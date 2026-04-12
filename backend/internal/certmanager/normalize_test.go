package certmanager

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ptrTime returns a pointer to the given time.Time value.
func ptrTime(t time.Time) *time.Time { return &t }

// TestComputeStatus covers the 8 status derivation cases.
func TestComputeStatus(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name        string
		readyStatus string
		reason      string
		notAfter    *time.Time
		want        Status
	}{
		{
			name:        "ready-valid-60d",
			readyStatus: "True",
			reason:      "",
			notAfter:    ptrTime(now.Add(60 * 24 * time.Hour)),
			want:        StatusReady,
		},
		{
			name:        "ready-expiring-warning-20d",
			readyStatus: "True",
			reason:      "",
			notAfter:    ptrTime(now.Add(20 * 24 * time.Hour)),
			want:        StatusExpiring,
		},
		{
			name:        "ready-expiring-critical-3d",
			readyStatus: "True",
			reason:      "",
			notAfter:    ptrTime(now.Add(3 * 24 * time.Hour)),
			want:        StatusExpiring,
		},
		{
			name:        "expired",
			readyStatus: "True",
			reason:      "",
			notAfter:    ptrTime(now.Add(-1 * time.Hour)),
			want:        StatusExpired,
		},
		{
			name:        "issuing",
			readyStatus: "False",
			reason:      "Issuing",
			notAfter:    nil,
			want:        StatusIssuing,
		},
		{
			name:        "failed",
			readyStatus: "False",
			reason:      "Failed",
			notAfter:    nil,
			want:        StatusFailed,
		},
		{
			name:        "unknown-status",
			readyStatus: "Unknown",
			reason:      "",
			notAfter:    nil,
			want:        StatusUnknown,
		},
		{
			name:        "missing-ready",
			readyStatus: "",
			reason:      "",
			notAfter:    nil,
			want:        StatusUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeStatus(tc.readyStatus, tc.reason, tc.notAfter)
			if got != tc.want {
				t.Errorf("computeStatus(%q, %q, notAfter) = %q; want %q",
					tc.readyStatus, tc.reason, got, tc.want)
			}
		})
	}
}

// TestNormalizeCertificate covers certificate parsing including edge cases.
func TestNormalizeCertificate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	notAfter45d := now.Add(45 * 24 * time.Hour)

	t.Run("happy-path", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{
				"name":      "my-cert",
				"namespace": "production",
				"uid":       "abc-123",
			},
			"spec": map[string]any{
				"secretName": "my-cert-tls",
				"dnsNames":   []any{"example.com", "www.example.com"},
				"issuerRef": map[string]any{
					"name":  "letsencrypt",
					"kind":  "ClusterIssuer",
					"group": "cert-manager.io",
				},
				"duration":    "2160h",
				"renewBefore": "360h",
			},
			"status": map[string]any{
				"notAfter": notAfter45d.Format(time.RFC3339),
				"conditions": []any{
					map[string]any{
						"type":    "Ready",
						"status":  "True",
						"reason":  "Ready",
						"message": "Certificate is up to date and has not expired",
					},
				},
			},
		}}

		cert, err := normalizeCertificate(u)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cert.Name != "my-cert" {
			t.Errorf("Name = %q; want %q", cert.Name, "my-cert")
		}
		if cert.Namespace != "production" {
			t.Errorf("Namespace = %q; want %q", cert.Namespace, "production")
		}
		if cert.Status != StatusReady {
			t.Errorf("Status = %q; want %q", cert.Status, StatusReady)
		}
		if cert.SecretName != "my-cert-tls" {
			t.Errorf("SecretName = %q; want %q", cert.SecretName, "my-cert-tls")
		}
		if len(cert.DNSNames) != 2 {
			t.Errorf("DNSNames len = %d; want 2", len(cert.DNSNames))
		}
		if cert.IssuerRef.Name != "letsencrypt" {
			t.Errorf("IssuerRef.Name = %q; want %q", cert.IssuerRef.Name, "letsencrypt")
		}
		if cert.IssuerRef.Kind != "ClusterIssuer" {
			t.Errorf("IssuerRef.Kind = %q; want %q", cert.IssuerRef.Kind, "ClusterIssuer")
		}
		if cert.DaysRemaining == nil {
			t.Error("DaysRemaining is nil; want non-nil")
		} else if *cert.DaysRemaining < 44 || *cert.DaysRemaining > 45 {
			t.Errorf("DaysRemaining = %d; want ~45", *cert.DaysRemaining)
		}
		if cert.NotAfter == nil {
			t.Error("NotAfter is nil; want non-nil")
		}
		if cert.Duration != "2160h" {
			t.Errorf("Duration = %q; want %q", cert.Duration, "2160h")
		}
	})

	t.Run("malformed-conditions", func(t *testing.T) {
		// conditions is a string instead of []any — must not panic.
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{
				"name":      "bad-cert",
				"namespace": "default",
				"uid":       "xyz",
			},
			"spec": map[string]any{
				"secretName": "bad-cert-tls",
				"issuerRef":  map[string]any{"name": "ca", "kind": "Issuer", "group": "cert-manager.io"},
			},
			"status": map[string]any{
				"conditions": "this-is-not-a-slice",
			},
		}}

		cert, err := normalizeCertificate(u)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cert.Status != StatusUnknown {
			t.Errorf("Status = %q; want %q", cert.Status, StatusUnknown)
		}
	})

	t.Run("nil-status", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{
				"name":      "no-status",
				"namespace": "default",
				"uid":       "xyz",
			},
			"spec": map[string]any{
				"secretName": "no-status-tls",
				"issuerRef":  map[string]any{"name": "ca", "kind": "Issuer", "group": "cert-manager.io"},
			},
		}}

		cert, err := normalizeCertificate(u)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cert.Status != StatusUnknown {
			t.Errorf("Status = %q; want %q", cert.Status, StatusUnknown)
		}
		if cert.DaysRemaining != nil {
			t.Errorf("DaysRemaining = %v; want nil", *cert.DaysRemaining)
		}
	})

	t.Run("expired-cert", func(t *testing.T) {
		pastTime := now.Add(-48 * time.Hour)
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{
				"name":      "expired-cert",
				"namespace": "default",
				"uid":       "exp-uid",
			},
			"spec": map[string]any{
				"secretName": "expired-tls",
				"issuerRef":  map[string]any{"name": "ca", "kind": "Issuer", "group": "cert-manager.io"},
			},
			"status": map[string]any{
				"notAfter": pastTime.Format(time.RFC3339),
				"conditions": []any{
					map[string]any{
						"type":   "Ready",
						"status": "True",
						"reason": "Ready",
					},
				},
			},
		}}

		cert, err := normalizeCertificate(u)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cert.Status != StatusExpired {
			t.Errorf("Status = %q; want %q", cert.Status, StatusExpired)
		}
	})
}

// TestNormalizeIssuer covers Issuer type detection and field extraction.
func TestNormalizeIssuer(t *testing.T) {
	t.Run("acme-type", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{
				"name": "letsencrypt-prod",
				"uid":  "issuer-uid-1",
			},
			"spec": map[string]any{
				"acme": map[string]any{
					"email":  "admin@example.com",
					"server": "https://acme-v02.api.letsencrypt.org/directory",
				},
			},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":    "Ready",
						"status":  "True",
						"reason":  "ACMEAccountRegistered",
						"message": "The ACME account was registered with the ACME server",
					},
				},
			},
		}}

		issuer := normalizeIssuer(u, "Cluster")
		if issuer.Type != "ACME" {
			t.Errorf("Type = %q; want ACME", issuer.Type)
		}
		if issuer.ACMEEmail != "admin@example.com" {
			t.Errorf("ACMEEmail = %q; want admin@example.com", issuer.ACMEEmail)
		}
		if issuer.ACMEServer != "https://acme-v02.api.letsencrypt.org/directory" {
			t.Errorf("ACMEServer = %q; want letsencrypt URL", issuer.ACMEServer)
		}
		if !issuer.Ready {
			t.Error("Ready = false; want true")
		}
		if issuer.Scope != "Cluster" {
			t.Errorf("Scope = %q; want Cluster", issuer.Scope)
		}
	})

	t.Run("ca-type", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{
				"name":      "internal-ca",
				"namespace": "cert-manager",
				"uid":       "issuer-uid-2",
			},
			"spec": map[string]any{
				"ca": map[string]any{
					"secretName": "ca-key-pair",
				},
			},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":   "Ready",
						"status": "True",
						"reason": "KeyPairVerified",
					},
				},
			},
		}}

		issuer := normalizeIssuer(u, "Namespaced")
		if issuer.Type != "CA" {
			t.Errorf("Type = %q; want CA", issuer.Type)
		}
		if !issuer.Ready {
			t.Error("Ready = false; want true")
		}
		if issuer.Scope != "Namespaced" {
			t.Errorf("Scope = %q; want Namespaced", issuer.Scope)
		}
	})

	t.Run("unknown-type-empty-spec", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{
				"name": "mystery-issuer",
				"uid":  "issuer-uid-3",
			},
			"spec":   map[string]any{},
			"status": map[string]any{},
		}}

		issuer := normalizeIssuer(u, "Cluster")
		if issuer.Type != "Unknown" {
			t.Errorf("Type = %q; want Unknown", issuer.Type)
		}
		if issuer.Ready {
			t.Error("Ready = true; want false")
		}
	})
}

// TestDetectIssuerType covers all five issuer type branches.
func TestDetectIssuerType(t *testing.T) {
	cases := []struct {
		name string
		spec map[string]any
		want string
	}{
		{
			name: "acme",
			spec: map[string]any{"acme": map[string]any{"email": "x@x.com"}},
			want: "ACME",
		},
		{
			name: "ca",
			spec: map[string]any{"ca": map[string]any{"secretName": "ca-secret"}},
			want: "CA",
		},
		{
			name: "vault",
			spec: map[string]any{"vault": map[string]any{"server": "https://vault"}},
			want: "Vault",
		},
		{
			name: "selfSigned",
			spec: map[string]any{"selfSigned": map[string]any{}},
			want: "SelfSigned",
		},
		{
			name: "unknown",
			spec: map[string]any{},
			want: "Unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectIssuerType(tc.spec)
			if got != tc.want {
				t.Errorf("detectIssuerType = %q; want %q", got, tc.want)
			}
		})
	}
}
