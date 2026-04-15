package wizard

import (
	"fmt"
	"strings"
	"time"

	sigsyaml "sigs.k8s.io/yaml"
)

var validPrivateKeyAlgorithms = map[string]bool{
	"RSA":     true,
	"ECDSA":   true,
	"Ed25519": true,
}

var validRSASizes = map[int]bool{2048: true, 3072: true, 4096: true}

var validECDSASizes = map[int]bool{256: true, 384: true, 521: true}

var validRotationPolicies = map[string]bool{"Always": true, "Never": true}

// CertificateIssuerRefInput identifies the Issuer or ClusterIssuer that signs the certificate.
type CertificateIssuerRefInput struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`            // "Issuer" or "ClusterIssuer"
	Group string `json:"group,omitempty"` // defaults to "cert-manager.io"
}

// CertificatePrivateKeyInput configures the private key generation.
type CertificatePrivateKeyInput struct {
	Algorithm      string `json:"algorithm,omitempty"`      // RSA, ECDSA, Ed25519
	Size           int    `json:"size,omitempty"`           // algorithm-dependent; must be 0 for Ed25519
	RotationPolicy string `json:"rotationPolicy,omitempty"` // Always or Never
}

// CertificateInput represents the wizard form data for creating a cert-manager Certificate.
type CertificateInput struct {
	Name        string                      `json:"name"`
	Namespace   string                      `json:"namespace"`
	SecretName  string                      `json:"secretName"`
	IssuerRef   CertificateIssuerRefInput   `json:"issuerRef"`
	DNSNames    []string                    `json:"dnsNames,omitempty"`
	CommonName  string                      `json:"commonName,omitempty"`
	Duration    string                      `json:"duration,omitempty"`    // Go duration string, default 2160h
	RenewBefore string                      `json:"renewBefore,omitempty"` // default 360h
	PrivateKey  *CertificatePrivateKeyInput `json:"privateKey,omitempty"`
	IsCA        bool                        `json:"isCA,omitempty"`
}

// Validate checks the CertificateInput and returns field-level errors.
func (c *CertificateInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(c.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}
	if c.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(c.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}
	if c.SecretName == "" {
		errs = append(errs, FieldError{Field: "secretName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(c.SecretName) {
		errs = append(errs, FieldError{Field: "secretName", Message: "must be a valid DNS label"})
	}

	// issuerRef
	if c.IssuerRef.Kind != "Issuer" && c.IssuerRef.Kind != "ClusterIssuer" {
		errs = append(errs, FieldError{Field: "issuerRef.kind", Message: "must be Issuer or ClusterIssuer"})
	}
	if c.IssuerRef.Name == "" {
		errs = append(errs, FieldError{Field: "issuerRef.name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(c.IssuerRef.Name) {
		errs = append(errs, FieldError{Field: "issuerRef.name", Message: "must be a valid DNS label"})
	}

	// Need at least one identifier.
	if len(c.DNSNames) == 0 && c.CommonName == "" {
		errs = append(errs, FieldError{Field: "dnsNames", Message: "at least one of dnsNames or commonName is required"})
	}

	// dnsNames: RFC 1123 with optional leftmost wildcard.
	if len(c.DNSNames) > 100 {
		errs = append(errs, FieldError{Field: "dnsNames", Message: "must have 100 or fewer entries"})
	}
	for i, name := range c.DNSNames {
		lower := strings.ToLower(name)
		if !dnsNameRegex.MatchString(lower) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("dnsNames[%d]", i),
				Message: "must be a valid DNS name (wildcards only in leftmost label)",
			})
		}
		if len(name) > 253 {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("dnsNames[%d]", i),
				Message: "must be 253 characters or fewer",
			})
		}
	}

	// commonName: CA/Browser Forum and x509 enforce ≤64 chars. Reject control
	// characters — they survive into x509 subject fields and can corrupt logs.
	if len(c.CommonName) > 64 {
		errs = append(errs, FieldError{Field: "commonName", Message: "must be 64 characters or fewer"})
	}
	for _, r := range c.CommonName {
		if r < 0x20 || r == 0x7f {
			errs = append(errs, FieldError{Field: "commonName", Message: "must not contain control characters"})
			break
		}
	}

	// duration and renewBefore must parse and obey renewBefore < duration.
	var dur, renew time.Duration
	var durOK, renewOK bool
	if c.Duration != "" {
		d, err := time.ParseDuration(c.Duration)
		if err != nil {
			errs = append(errs, FieldError{Field: "duration", Message: "must be a valid Go duration (e.g. 2160h)"})
		} else if d < time.Hour {
			errs = append(errs, FieldError{Field: "duration", Message: "must be at least 1h"})
		} else {
			dur = d
			durOK = true
		}
	}
	if c.RenewBefore != "" {
		rb, err := time.ParseDuration(c.RenewBefore)
		if err != nil {
			errs = append(errs, FieldError{Field: "renewBefore", Message: "must be a valid Go duration (e.g. 360h)"})
		} else if rb < 5*time.Minute {
			errs = append(errs, FieldError{Field: "renewBefore", Message: "must be at least 5m"})
		} else {
			renew = rb
			renewOK = true
		}
	}
	if durOK && renewOK && renew >= dur {
		errs = append(errs, FieldError{Field: "renewBefore", Message: "must be less than duration"})
	}

	// privateKey
	if c.PrivateKey != nil {
		errs = append(errs, c.PrivateKey.validate()...)
	}

	return errs
}

func (pk *CertificatePrivateKeyInput) validate() []FieldError {
	var errs []FieldError
	if pk.Algorithm != "" && !validPrivateKeyAlgorithms[pk.Algorithm] {
		errs = append(errs, FieldError{Field: "privateKey.algorithm", Message: "must be RSA, ECDSA, or Ed25519"})
	}
	switch pk.Algorithm {
	case "RSA":
		if pk.Size != 0 && !validRSASizes[pk.Size] {
			errs = append(errs, FieldError{Field: "privateKey.size", Message: "RSA size must be 2048, 3072, or 4096"})
		}
	case "ECDSA":
		if pk.Size != 0 && !validECDSASizes[pk.Size] {
			errs = append(errs, FieldError{Field: "privateKey.size", Message: "ECDSA size must be 256, 384, or 521"})
		}
	case "Ed25519":
		if pk.Size != 0 {
			errs = append(errs, FieldError{Field: "privateKey.size", Message: "Ed25519 does not accept a size"})
		}
	}
	if pk.RotationPolicy != "" && !validRotationPolicies[pk.RotationPolicy] {
		errs = append(errs, FieldError{Field: "privateKey.rotationPolicy", Message: "must be Always or Never"})
	}
	return errs
}

// ToCertificate returns a map representation suitable for YAML marshaling.
// cert-manager is not in go.mod; we construct the object as a map to avoid
// pulling in a large transitive dependency tree.
func (c *CertificateInput) ToCertificate() map[string]any {
	group := c.IssuerRef.Group
	if group == "" {
		group = "cert-manager.io"
	}

	spec := map[string]any{
		"secretName": c.SecretName,
		"issuerRef": map[string]any{
			"name":  c.IssuerRef.Name,
			"kind":  c.IssuerRef.Kind,
			"group": group,
		},
	}

	if len(c.DNSNames) > 0 {
		spec["dnsNames"] = c.DNSNames
	}
	if c.CommonName != "" {
		spec["commonName"] = c.CommonName
	}
	if c.Duration != "" {
		spec["duration"] = c.Duration
	}
	if c.RenewBefore != "" {
		spec["renewBefore"] = c.RenewBefore
	}
	if c.PrivateKey != nil {
		pk := map[string]any{}
		if c.PrivateKey.Algorithm != "" {
			pk["algorithm"] = c.PrivateKey.Algorithm
		}
		if c.PrivateKey.Size != 0 {
			pk["size"] = c.PrivateKey.Size
		}
		if c.PrivateKey.RotationPolicy != "" {
			pk["rotationPolicy"] = c.PrivateKey.RotationPolicy
		}
		if len(pk) > 0 {
			spec["privateKey"] = pk
		}
	}
	if c.IsCA {
		spec["isCA"] = true
	}

	return map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name":      c.Name,
			"namespace": c.Namespace,
		},
		"spec": spec,
	}
}

// ToYAML implements WizardInput.
func (c *CertificateInput) ToYAML() (string, error) {
	y, err := sigsyaml.Marshal(c.ToCertificate())
	if err != nil {
		return "", err
	}
	return string(y), nil
}
