package wizard

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// IssuerScope indicates whether the wizard produces a namespaced Issuer or a cluster-scoped ClusterIssuer.
type IssuerScope string

const (
	IssuerScopeNamespaced IssuerScope = "namespaced"
	IssuerScopeCluster    IssuerScope = "cluster"
)

// IssuerType enumerates the supported cert-manager issuer backends for v1.
type IssuerType string

const (
	IssuerTypeSelfSigned IssuerType = "selfSigned"
	IssuerTypeACME       IssuerType = "acme"
	IssuerTypeCA         IssuerType = "ca"
	IssuerTypeVault      IssuerType = "vault"
)

var validIssuerTypes = map[IssuerType]bool{
	IssuerTypeSelfSigned: true,
	IssuerTypeACME:       true,
	IssuerTypeCA:         true,
	IssuerTypeVault:      true,
}

// ACMEHTTP01IngressInput configures the HTTP01 ingress solver.
type ACMEHTTP01IngressInput struct {
	IngressClassName string `json:"ingressClassName,omitempty"`
}

// ACMESolverInput is a single solver entry. v1 supports HTTP01 ingress only.
type ACMESolverInput struct {
	HTTP01Ingress *ACMEHTTP01IngressInput `json:"http01Ingress,omitempty"`
}

// ACMEInput configures an ACME issuer.
type ACMEInput struct {
	Server                 string            `json:"server"`
	Email                  string            `json:"email"`
	PrivateKeySecretRefName string           `json:"privateKeySecretRefName"`
	Solvers                []ACMESolverInput `json:"solvers"`
}

// CAInput configures a CA issuer.
type CAInput struct {
	SecretName string `json:"secretName"`
}

// VaultAuthInput configures one of Vault's authentication methods.
// Exactly one of the nested fields must be set.
type VaultAuthInput struct {
	TokenSecretRefName string `json:"tokenSecretRefName,omitempty"`
	AppRoleSecretRefName string `json:"appRoleSecretRefName,omitempty"`
	KubernetesRole     string `json:"kubernetesRole,omitempty"`
}

// VaultInput configures a Vault issuer.
type VaultInput struct {
	Server string         `json:"server"`
	Path   string         `json:"path"`
	Auth   VaultAuthInput `json:"auth"`
}

// IssuerInput represents the wizard form data for creating a cert-manager Issuer or ClusterIssuer.
type IssuerInput struct {
	Scope     IssuerScope `json:"scope"`
	Name      string      `json:"name"`
	Namespace string      `json:"namespace,omitempty"` // ignored for cluster scope
	Type      IssuerType  `json:"type"`

	SelfSigned *struct{}   `json:"selfSigned,omitempty"`
	ACME       *ACMEInput  `json:"acme,omitempty"`
	CA         *CAInput    `json:"ca,omitempty"`
	Vault      *VaultInput `json:"vault,omitempty"`
}

// Validate checks the IssuerInput and returns field-level errors.
func (i *IssuerInput) Validate() []FieldError {
	var errs []FieldError

	if i.Scope != IssuerScopeNamespaced && i.Scope != IssuerScopeCluster {
		errs = append(errs, FieldError{Field: "scope", Message: "must be namespaced or cluster"})
	}

	if !dnsLabelRegex.MatchString(i.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if i.Scope == IssuerScopeNamespaced {
		if i.Namespace == "" {
			errs = append(errs, FieldError{Field: "namespace", Message: "is required for namespaced Issuer"})
		} else if !dnsLabelRegex.MatchString(i.Namespace) {
			errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
		}
	}

	if !validIssuerTypes[i.Type] {
		errs = append(errs, FieldError{Field: "type", Message: "must be selfSigned, acme, ca, or vault"})
		return errs
	}

	// Exactly one type body must be populated and it must match Type.
	populated := 0
	if i.SelfSigned != nil {
		populated++
	}
	if i.ACME != nil {
		populated++
	}
	if i.CA != nil {
		populated++
	}
	if i.Vault != nil {
		populated++
	}
	if populated != 1 {
		errs = append(errs, FieldError{Field: "type", Message: "exactly one issuer body must be provided"})
		return errs
	}

	switch i.Type {
	case IssuerTypeSelfSigned:
		if i.SelfSigned == nil {
			errs = append(errs, FieldError{Field: "selfSigned", Message: "selfSigned body is required when type=selfSigned"})
		}
	case IssuerTypeACME:
		if i.ACME == nil {
			errs = append(errs, FieldError{Field: "acme", Message: "acme body is required when type=acme"})
			return errs
		}
		errs = append(errs, i.ACME.validate()...)
	case IssuerTypeCA:
		if i.CA == nil {
			errs = append(errs, FieldError{Field: "ca", Message: "ca body is required when type=ca"})
			return errs
		}
		errs = append(errs, i.CA.validate()...)
	case IssuerTypeVault:
		if i.Vault == nil {
			errs = append(errs, FieldError{Field: "vault", Message: "vault body is required when type=vault"})
			return errs
		}
		errs = append(errs, i.Vault.validate()...)
	}

	return errs
}

func (a *ACMEInput) validate() []FieldError {
	var errs []FieldError

	if a.Server == "" {
		errs = append(errs, FieldError{Field: "acme.server", Message: "is required"})
	} else if err := validateHTTPSPublicURL(a.Server); err != nil {
		errs = append(errs, FieldError{Field: "acme.server", Message: err.Error()})
	}

	if !validateEmailAddress(a.Email) {
		errs = append(errs, FieldError{Field: "acme.email", Message: "must be a valid email address"})
	}

	if a.PrivateKeySecretRefName == "" {
		errs = append(errs, FieldError{Field: "acme.privateKeySecretRefName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(a.PrivateKeySecretRefName) {
		errs = append(errs, FieldError{Field: "acme.privateKeySecretRefName", Message: "must be a valid DNS label"})
	}

	if len(a.Solvers) == 0 {
		errs = append(errs, FieldError{Field: "acme.solvers", Message: "at least one solver is required"})
	}
	for idx, s := range a.Solvers {
		if s.HTTP01Ingress == nil {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("acme.solvers[%d]", idx),
				Message: "http01Ingress is required (DNS01 solvers are not supported in v1)",
			})
			continue
		}
		if s.HTTP01Ingress.IngressClassName != "" && !dnsLabelRegex.MatchString(s.HTTP01Ingress.IngressClassName) {
			errs = append(errs, FieldError{
				Field:   fmt.Sprintf("acme.solvers[%d].http01Ingress.ingressClassName", idx),
				Message: "must be a valid DNS label",
			})
		}
	}

	return errs
}

// validateHTTPSPublicURL rejects non-HTTPS URLs and URLs that resolve to RFC1918 or loopback addresses.
func validateHTTPSPublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("must be a valid URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("must use https scheme")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("must not target a private or loopback IP")
		}
	}
	return nil
}

func (c *CAInput) validate() []FieldError {
	var errs []FieldError
	if c.SecretName == "" {
		errs = append(errs, FieldError{Field: "ca.secretName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(c.SecretName) {
		errs = append(errs, FieldError{Field: "ca.secretName", Message: "must be a valid DNS label"})
	}
	return errs
}

func (v *VaultInput) validate() []FieldError {
	var errs []FieldError
	if v.Server == "" {
		errs = append(errs, FieldError{Field: "vault.server", Message: "is required"})
	} else if err := validateHTTPSPublicURL(v.Server); err != nil {
		errs = append(errs, FieldError{Field: "vault.server", Message: err.Error()})
	}
	if v.Path == "" {
		errs = append(errs, FieldError{Field: "vault.path", Message: "is required"})
	}
	populated := 0
	if v.Auth.TokenSecretRefName != "" {
		populated++
	}
	if v.Auth.AppRoleSecretRefName != "" {
		populated++
	}
	if v.Auth.KubernetesRole != "" {
		populated++
	}
	if populated != 1 {
		errs = append(errs, FieldError{Field: "vault.auth", Message: "exactly one of tokenSecretRefName, appRoleSecretRefName, or kubernetesRole must be set"})
	}
	return errs
}

// ToIssuer returns a map representation suitable for YAML marshaling.
func (i *IssuerInput) ToIssuer() map[string]any {
	kind := "Issuer"
	if i.Scope == IssuerScopeCluster {
		kind = "ClusterIssuer"
	}

	metadata := map[string]any{
		"name": i.Name,
	}
	if i.Scope == IssuerScopeNamespaced {
		metadata["namespace"] = i.Namespace
	}

	return map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       kind,
		"metadata":   metadata,
		"spec":       i.buildSpec(),
	}
}

func (i *IssuerInput) buildSpec() map[string]any {
	switch i.Type {
	case IssuerTypeSelfSigned:
		return map[string]any{"selfSigned": map[string]any{}}
	case IssuerTypeACME:
		return map[string]any{"acme": i.ACME.toMap()}
	case IssuerTypeCA:
		return map[string]any{"ca": map[string]any{"secretName": i.CA.SecretName}}
	case IssuerTypeVault:
		return map[string]any{"vault": i.Vault.toMap()}
	}
	return map[string]any{}
}

func (a *ACMEInput) toMap() map[string]any {
	out := map[string]any{
		"server":              a.Server,
		"email":               a.Email,
		"privateKeySecretRef": map[string]any{"name": a.PrivateKeySecretRefName},
	}
	solvers := make([]map[string]any, 0, len(a.Solvers))
	for _, s := range a.Solvers {
		if s.HTTP01Ingress == nil {
			continue
		}
		ingress := map[string]any{}
		if s.HTTP01Ingress.IngressClassName != "" {
			ingress["ingressClassName"] = s.HTTP01Ingress.IngressClassName
		}
		solvers = append(solvers, map[string]any{
			"http01": map[string]any{"ingress": ingress},
		})
	}
	if len(solvers) > 0 {
		out["solvers"] = solvers
	}
	return out
}

func (v *VaultInput) toMap() map[string]any {
	out := map[string]any{
		"server": v.Server,
		"path":   v.Path,
	}
	auth := map[string]any{}
	switch {
	case v.Auth.TokenSecretRefName != "":
		auth["tokenSecretRef"] = map[string]any{"name": v.Auth.TokenSecretRefName}
	case v.Auth.AppRoleSecretRefName != "":
		auth["appRole"] = map[string]any{
			"secretRef": map[string]any{"name": v.Auth.AppRoleSecretRefName},
		}
	case v.Auth.KubernetesRole != "":
		auth["kubernetes"] = map[string]any{"role": v.Auth.KubernetesRole}
	}
	out["auth"] = auth
	return out
}

// ToYAML implements WizardInput.
func (i *IssuerInput) ToYAML() (string, error) {
	y, err := sigsyaml.Marshal(i.ToIssuer())
	if err != nil {
		return "", err
	}
	// Strip trailing empty line, for prettier preview rendering.
	return strings.TrimRight(string(y), "\n") + "\n", nil
}
