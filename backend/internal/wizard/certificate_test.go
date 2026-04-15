package wizard

import (
	"strings"
	"testing"
)

func validCertificateInput() CertificateInput {
	return CertificateInput{
		Name:       "example-com-tls",
		Namespace:  "default",
		SecretName: "example-com-tls",
		IssuerRef:  CertificateIssuerRefInput{Name: "letsencrypt-prod", Kind: "ClusterIssuer"},
		DNSNames:   []string{"example.com", "www.example.com"},
	}
}

func TestCertificateValidate_Valid(t *testing.T) {
	c := validCertificateInput()
	if errs := c.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestCertificateValidate_MissingRequired(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*CertificateInput)
		wantField string
	}{
		{"empty name", func(c *CertificateInput) { c.Name = "" }, "name"},
		{"uppercase name", func(c *CertificateInput) { c.Name = "BadName" }, "name"},
		{"empty namespace", func(c *CertificateInput) { c.Namespace = "" }, "namespace"},
		{"empty secretName", func(c *CertificateInput) { c.SecretName = "" }, "secretName"},
		{"missing issuerRef.name", func(c *CertificateInput) { c.IssuerRef.Name = "" }, "issuerRef.name"},
		{"invalid issuerRef.kind", func(c *CertificateInput) { c.IssuerRef.Kind = "Random" }, "issuerRef.kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCertificateInput()
			tt.mutate(&c)
			errs := c.Validate()
			if !hasFieldError(errs, tt.wantField) {
				t.Errorf("expected error on %q, got %v", tt.wantField, errs)
			}
		})
	}
}

func TestCertificateValidate_NoIdentifiers(t *testing.T) {
	c := validCertificateInput()
	c.DNSNames = nil
	c.CommonName = ""
	errs := c.Validate()
	if !hasFieldError(errs, "dnsNames") {
		t.Errorf("expected dnsNames error when no identifiers present, got %v", errs)
	}
}

func TestCertificateValidate_CommonNameControlChars(t *testing.T) {
	tests := []string{"with\nnewline", "null\x00byte", "del\x7fchar"}
	for _, cn := range tests {
		c := validCertificateInput()
		c.DNSNames = nil
		c.CommonName = cn
		errs := c.Validate()
		if !hasFieldError(errs, "commonName") {
			t.Errorf("commonName %q should be rejected for control chars, got %v", cn, errs)
		}
	}
}

func TestCertificateValidate_CommonNameOnlyAccepted(t *testing.T) {
	c := validCertificateInput()
	c.DNSNames = nil
	c.CommonName = "example.com"
	if errs := c.Validate(); len(errs) != 0 {
		t.Errorf("commonName alone should satisfy identifier requirement, got %v", errs)
	}
}

func TestCertificateValidate_DNSNames(t *testing.T) {
	tests := []struct {
		name    string
		dnsName string
		wantErr bool
	}{
		{"valid hostname", "example.com", false},
		{"valid subdomain", "api.example.com", false},
		{"wildcard leftmost", "*.example.com", false},
		{"wildcard not leftmost", "api.*.example.com", true},
		{"uppercase normalized ok", "EXAMPLE.com", false},
		{"empty string", "", true},
		{"trailing dot", "example.com.", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCertificateInput()
			c.DNSNames = []string{tt.dnsName}
			errs := c.Validate()
			got := hasFieldErrorWithPrefix(errs, "dnsNames")
			if got != tt.wantErr {
				t.Errorf("dnsName %q: wantErr=%v, got errs=%v", tt.dnsName, tt.wantErr, errs)
			}
		})
	}
}

func TestCertificateValidate_CommonNameTooLong(t *testing.T) {
	c := validCertificateInput()
	c.CommonName = strings.Repeat("a", 65)
	errs := c.Validate()
	if !hasFieldError(errs, "commonName") {
		t.Errorf("expected commonName length error, got %v", errs)
	}
}

func TestCertificateValidate_Duration(t *testing.T) {
	tests := []struct {
		name        string
		duration    string
		renewBefore string
		wantField   string
	}{
		{"invalid duration", "not-a-duration", "", "duration"},
		{"duration too short", "30m", "", "duration"},
		{"invalid renewBefore", "2160h", "not-a-duration", "renewBefore"},
		{"renewBefore too short", "2160h", "1m", "renewBefore"},
		{"renewBefore >= duration", "24h", "48h", "renewBefore"},
		{"renewBefore == duration", "24h", "24h", "renewBefore"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCertificateInput()
			c.Duration = tt.duration
			c.RenewBefore = tt.renewBefore
			errs := c.Validate()
			if !hasFieldError(errs, tt.wantField) {
				t.Errorf("expected error on %q, got %v", tt.wantField, errs)
			}
		})
	}
}

func TestCertificateValidate_PrivateKey(t *testing.T) {
	tests := []struct {
		name      string
		pk        CertificatePrivateKeyInput
		wantField string
	}{
		{"invalid algorithm", CertificatePrivateKeyInput{Algorithm: "DSA"}, "privateKey.algorithm"},
		{"invalid RSA size", CertificatePrivateKeyInput{Algorithm: "RSA", Size: 1024}, "privateKey.size"},
		{"invalid ECDSA size", CertificatePrivateKeyInput{Algorithm: "ECDSA", Size: 192}, "privateKey.size"},
		{"Ed25519 with nonzero size", CertificatePrivateKeyInput{Algorithm: "Ed25519", Size: 2048}, "privateKey.size"},
		{"invalid rotationPolicy", CertificatePrivateKeyInput{RotationPolicy: "Sometimes"}, "privateKey.rotationPolicy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCertificateInput()
			c.PrivateKey = &tt.pk
			errs := c.Validate()
			if !hasFieldError(errs, tt.wantField) {
				t.Errorf("expected error on %q, got %v", tt.wantField, errs)
			}
		})
	}
}

func TestCertificateValidate_PrivateKeyValidCombinations(t *testing.T) {
	valid := []CertificatePrivateKeyInput{
		{Algorithm: "RSA", Size: 2048, RotationPolicy: "Always"},
		{Algorithm: "ECDSA", Size: 256},
		{Algorithm: "Ed25519"},
	}
	for _, pk := range valid {
		c := validCertificateInput()
		pk := pk
		c.PrivateKey = &pk
		if errs := c.Validate(); len(errs) != 0 {
			t.Errorf("pk=%+v should be valid, got %v", pk, errs)
		}
	}
}

func TestCertificateToYAML(t *testing.T) {
	c := validCertificateInput()
	c.Duration = "2160h"
	c.RenewBefore = "360h"
	c.PrivateKey = &CertificatePrivateKeyInput{Algorithm: "RSA", Size: 2048, RotationPolicy: "Always"}

	y, err := c.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"apiVersion: cert-manager.io/v1",
		"kind: Certificate",
		"kind: ClusterIssuer",
		"secretName: example-com-tls",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("YAML missing %q:\n%s", want, y)
		}
	}
}

// --- Helpers ---

func hasFieldError(errs []FieldError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

func hasFieldErrorWithPrefix(errs []FieldError, prefix string) bool {
	for _, e := range errs {
		if strings.HasPrefix(e.Field, prefix) {
			return true
		}
	}
	return false
}
