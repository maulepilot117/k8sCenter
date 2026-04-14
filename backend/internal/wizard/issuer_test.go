package wizard

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIssuerInput_ScopeNotJSONDecoded ensures a client cannot override the
// route-assigned scope by supplying it in the JSON body. Route authority is
// required because wizard preview feeds into server-side apply.
func TestIssuerInput_ScopeNotJSONDecoded(t *testing.T) {
	// Factory sets cluster scope; client body attempts to downgrade to namespaced.
	in := &IssuerInput{Scope: IssuerScopeCluster}
	body := []byte(`{"scope":"namespaced","name":"x","type":"selfSigned","selfSigned":{}}`)
	if err := json.Unmarshal(body, in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if in.Scope != IssuerScopeCluster {
		t.Errorf("scope overridden by client: got %q, want %q", in.Scope, IssuerScopeCluster)
	}
}

func validSelfSignedNamespaced() IssuerInput {
	return IssuerInput{
		Scope:      IssuerScopeNamespaced,
		Name:       "selfsigned",
		Namespace:  "default",
		Type:       IssuerTypeSelfSigned,
		SelfSigned: &struct{}{},
	}
}

func validSelfSignedCluster() IssuerInput {
	return IssuerInput{
		Scope:      IssuerScopeCluster,
		Name:       "selfsigned",
		Type:       IssuerTypeSelfSigned,
		SelfSigned: &struct{}{},
	}
}

func validACMECluster() IssuerInput {
	return IssuerInput{
		Scope: IssuerScopeCluster,
		Name:  "letsencrypt-staging",
		Type:  IssuerTypeACME,
		ACME: &ACMEInput{
			Server:                  "https://acme-staging-v02.api.letsencrypt.org/directory",
			Email:                   "admin@example.com",
			PrivateKeySecretRefName: "letsencrypt-staging-account",
			Solvers: []ACMESolverInput{
				{HTTP01Ingress: &ACMEHTTP01IngressInput{IngressClassName: "nginx"}},
			},
		},
	}
}

func TestIssuerValidate_SelfSignedNamespaced(t *testing.T) {
	i := validSelfSignedNamespaced()
	if errs := i.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestIssuerValidate_SelfSignedCluster(t *testing.T) {
	i := validSelfSignedCluster()
	if errs := i.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestIssuerValidate_ACMECluster(t *testing.T) {
	i := validACMECluster()
	if errs := i.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestIssuerValidate_InvalidScope(t *testing.T) {
	i := validSelfSignedNamespaced()
	i.Scope = "bogus"
	errs := i.Validate()
	if !hasFieldError(errs, "scope") {
		t.Errorf("expected scope error, got %v", errs)
	}
}

func TestIssuerValidate_NamespacedRequiresNamespace(t *testing.T) {
	i := validSelfSignedNamespaced()
	i.Namespace = ""
	errs := i.Validate()
	if !hasFieldError(errs, "namespace") {
		t.Errorf("expected namespace error, got %v", errs)
	}
}

func TestIssuerValidate_ClusterScopeAllowsEmptyNamespace(t *testing.T) {
	i := validSelfSignedCluster()
	i.Namespace = ""
	if errs := i.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestIssuerValidate_UnknownType(t *testing.T) {
	i := validSelfSignedNamespaced()
	i.Type = "venafi"
	errs := i.Validate()
	if !hasFieldError(errs, "type") {
		t.Errorf("expected type error, got %v", errs)
	}
}

func TestIssuerValidate_TypeBodyMismatch(t *testing.T) {
	// Type=acme but only selfSigned body is populated. Surfaces as "acme body is required".
	i := validSelfSignedNamespaced()
	i.Type = IssuerTypeACME
	errs := i.Validate()
	if !hasFieldError(errs, "acme") {
		t.Errorf("expected acme body-required error when type=acme but body is selfSigned, got %v", errs)
	}
}

func TestIssuerValidate_MultipleTypeBodies(t *testing.T) {
	i := validSelfSignedNamespaced()
	i.ACME = &ACMEInput{Server: "https://acme.example.com/", Email: "x@y", PrivateKeySecretRefName: "x"}
	errs := i.Validate()
	if !hasFieldError(errs, "type") {
		t.Errorf("expected type error for multiple bodies, got %v", errs)
	}
}

func TestACMEValidate_Server(t *testing.T) {
	tests := []struct {
		name   string
		server string
		ok     bool
	}{
		{"letsencrypt prod", "https://acme-v02.api.letsencrypt.org/directory", true},
		{"letsencrypt staging", "https://acme-staging-v02.api.letsencrypt.org/directory", true},
		{"http rejected", "http://acme.example.com/directory", false},
		{"loopback rejected", "https://127.0.0.1/directory", false},
		{"private IP rejected", "https://10.0.0.1/directory", false},
		{"empty rejected", "", false},
		{"garbage rejected", "not a url", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := validACMECluster()
			i.ACME.Server = tt.server
			errs := i.Validate()
			has := hasFieldErrorWithPrefix(errs, "acme.server")
			if tt.ok && has {
				t.Errorf("server %q should be valid, got %v", tt.server, errs)
			}
			if !tt.ok && !has {
				t.Errorf("server %q should be invalid, got errs=%v", tt.server, errs)
			}
		})
	}
}

func TestACMEValidate_Email(t *testing.T) {
	tests := []struct {
		email string
		ok    bool
	}{
		{"ok@example.com", true},
		{"", false},
		{"not-an-email", false},
	}
	for _, tt := range tests {
		i := validACMECluster()
		i.ACME.Email = tt.email
		errs := i.Validate()
		has := hasFieldError(errs, "acme.email")
		if tt.ok && has {
			t.Errorf("email %q should be valid, got %v", tt.email, errs)
		}
		if !tt.ok && !has {
			t.Errorf("email %q should be invalid, got %v", tt.email, errs)
		}
	}
}

func TestACMEValidate_SolverRequired(t *testing.T) {
	i := validACMECluster()
	i.ACME.Solvers = nil
	errs := i.Validate()
	if !hasFieldError(errs, "acme.solvers") {
		t.Errorf("expected solvers error, got %v", errs)
	}
}

func TestACMEValidate_DNS01Rejected(t *testing.T) {
	i := validACMECluster()
	i.ACME.Solvers = []ACMESolverInput{{HTTP01Ingress: nil}} // no HTTP01 body signals DNS01 intent
	errs := i.Validate()
	if !hasFieldErrorWithPrefix(errs, "acme.solvers[0]") {
		t.Errorf("expected solver error for missing http01Ingress, got %v", errs)
	}
}

func TestCAValidate(t *testing.T) {
	i := IssuerInput{
		Scope: IssuerScopeNamespaced, Name: "ca", Namespace: "cert-manager",
		Type: IssuerTypeCA, CA: &CAInput{SecretName: ""},
	}
	errs := i.Validate()
	if !hasFieldError(errs, "ca.secretName") {
		t.Errorf("expected ca.secretName error, got %v", errs)
	}
}

func TestVaultValidate(t *testing.T) {
	base := IssuerInput{
		Scope: IssuerScopeCluster, Name: "vault", Type: IssuerTypeVault,
		Vault: &VaultInput{
			Server: "https://vault.example.com",
			Path:   "pki/sign/example",
			Auth:   VaultAuthInput{TokenSecretRefName: "vault-token"},
		},
	}
	if errs := base.Validate(); len(errs) != 0 {
		t.Errorf("valid vault issuer: %v", errs)
	}

	none := base
	none.Vault = &VaultInput{Server: base.Vault.Server, Path: base.Vault.Path}
	errs := none.Validate()
	if !hasFieldError(errs, "vault.auth") {
		t.Errorf("expected vault.auth error for no auth method, got %v", errs)
	}

	multi := base
	multi.Vault = &VaultInput{
		Server: base.Vault.Server, Path: base.Vault.Path,
		Auth: VaultAuthInput{TokenSecretRefName: "t", KubernetesRole: "r"},
	}
	errs = multi.Validate()
	if !hasFieldError(errs, "vault.auth") {
		t.Errorf("expected vault.auth error for multiple methods, got %v", errs)
	}
}

func TestIssuerToYAML_SelfSignedCluster(t *testing.T) {
	i := validSelfSignedCluster()
	y, err := i.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"apiVersion: cert-manager.io/v1",
		"kind: ClusterIssuer",
		"name: selfsigned",
		"selfSigned: {}",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("missing %q in YAML:\n%s", want, y)
		}
	}
	if strings.Contains(y, "namespace:") {
		t.Errorf("ClusterIssuer should not have namespace:\n%s", y)
	}
}

func TestIssuerToYAML_ACMECluster(t *testing.T) {
	i := validACMECluster()
	y, err := i.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"kind: ClusterIssuer",
		"acme:",
		"server: https://acme-staging-v02.api.letsencrypt.org/directory",
		"email: admin@example.com",
		"name: letsencrypt-staging-account",
		"http01:",
		"ingressClassName: nginx",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("missing %q in YAML:\n%s", want, y)
		}
	}
}

func TestIssuerToYAML_NamespacedIssuer(t *testing.T) {
	i := validSelfSignedNamespaced()
	y, err := i.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(y, "kind: Issuer") {
		t.Errorf("expected kind: Issuer, got:\n%s", y)
	}
	if !strings.Contains(y, "namespace: default") {
		t.Errorf("expected namespace: default, got:\n%s", y)
	}
}
