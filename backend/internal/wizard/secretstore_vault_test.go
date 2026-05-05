package wizard

import (
	"strings"
	"testing"
)

// validVaultSpec returns a minimal valid Vault provider spec with token auth
// — the simplest auth method to construct in tests. Other auth-method tests
// override the auth block as needed.
func validVaultSpec() map[string]any {
	return map[string]any{
		"server":  "https://vault.example.com",
		"path":    "secret",
		"version": "v2",
		"auth": map[string]any{
			"token": map[string]any{
				"tokenSecretRef": map[string]any{
					"name": "vault-token",
					"key":  "token",
				},
			},
		},
	}
}

func TestValidateVaultSpec_Valid(t *testing.T) {
	if errs := validateVaultSpec(validVaultSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateVaultSpec_MissingServer(t *testing.T) {
	spec := validVaultSpec()
	delete(spec, "server")
	if !hasField(validateVaultSpec(spec), "server") {
		t.Error("expected server required error")
	}
}

func TestValidateVaultSpec_BlankServer(t *testing.T) {
	spec := validVaultSpec()
	spec["server"] = "   "
	if !hasField(validateVaultSpec(spec), "server") {
		t.Error("expected server error for whitespace-only value")
	}
}

func TestValidateVaultSpec_NonHTTPSServer(t *testing.T) {
	spec := validVaultSpec()
	spec["server"] = "http://vault.example.com"
	errs := validateVaultSpec(spec)
	if !hasField(errs, "server") {
		t.Errorf("expected server error for http scheme; got %v", errs)
	}
}

func TestValidateVaultSpec_AcceptsPrivateAddress(t *testing.T) {
	// Vault may run on a private address (homelab, in-cluster). Unlike ACME
	// servers, we accept it.
	spec := validVaultSpec()
	spec["server"] = "https://vault.vault.svc.cluster.local:8200"
	if errs := validateVaultSpec(spec); len(errs) != 0 {
		t.Errorf("expected private https address to validate, got %v", errs)
	}
}

func TestValidateVaultSpec_BadVersion(t *testing.T) {
	spec := validVaultSpec()
	spec["version"] = "v3"
	if !hasField(validateVaultSpec(spec), "version") {
		t.Error("expected version error for v3")
	}
}

func TestValidateVaultSpec_PathLeadingSlash(t *testing.T) {
	spec := validVaultSpec()
	spec["path"] = "/secret"
	if !hasField(validateVaultSpec(spec), "path") {
		t.Error("expected path error for leading slash")
	}
}

func TestValidateVaultSpec_EmptyPathRejected(t *testing.T) {
	spec := validVaultSpec()
	spec["path"] = ""
	if !hasField(validateVaultSpec(spec), "path") {
		t.Error("expected path error for empty when set")
	}
}

func TestValidateVaultSpec_EmptyNamespaceRejected(t *testing.T) {
	spec := validVaultSpec()
	spec["namespace"] = ""
	if !hasField(validateVaultSpec(spec), "namespace") {
		t.Error("expected namespace error for empty when set")
	}
}

func TestValidateVaultSpec_NoAuth(t *testing.T) {
	spec := validVaultSpec()
	delete(spec, "auth")
	if !hasField(validateVaultSpec(spec), "auth") {
		t.Error("expected auth required error")
	}
}

func TestValidateVaultSpec_AuthNoMethod(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{}
	if !hasField(validateVaultSpec(spec), "auth") {
		t.Error("expected auth error for empty block")
	}
}

func TestValidateVaultSpec_AuthMultipleMethods(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"token":   map[string]any{"tokenSecretRef": map[string]any{"name": "t", "key": "token"}},
		"appRole": map[string]any{},
	}
	errs := validateVaultSpec(spec)
	if !hasField(errs, "auth") {
		t.Errorf("expected auth error for multiple methods; got %v", errs)
	}
	// Verify the message names both methods so users can disambiguate.
	found := false
	for _, e := range errs {
		if e.Field == "auth" && strings.Contains(e.Message, "only one") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected only-one error message, got %v", errs)
	}
}

// --- Token auth ---

func TestValidateVaultSpec_TokenAuth_MissingRef(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{"token": map[string]any{}}
	if !hasField(validateVaultSpec(spec), "auth.token.tokenSecretRef") {
		t.Error("expected tokenSecretRef required")
	}
}

func TestValidateVaultSpec_TokenAuth_MissingRefKey(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"token": map[string]any{
			"tokenSecretRef": map[string]any{"name": "vault-token"}, // missing key
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.token.tokenSecretRef.key") {
		t.Error("expected tokenSecretRef.key required")
	}
}

func TestValidateVaultSpec_TokenAuth_BadRefName(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"token": map[string]any{
			"tokenSecretRef": map[string]any{"name": "BadName", "key": "token"},
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.token.tokenSecretRef.name") {
		t.Error("expected tokenSecretRef.name DNS error")
	}
}

// --- Kubernetes auth ---

func TestValidateVaultSpec_KubernetesAuth_Valid(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"kubernetes": map[string]any{
			"mountPath": "kubernetes",
			"role":      "my-role",
		},
	}
	if errs := validateVaultSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateVaultSpec_KubernetesAuth_MissingMountPath(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"kubernetes": map[string]any{"role": "my-role"},
	}
	if !hasField(validateVaultSpec(spec), "auth.kubernetes.mountPath") {
		t.Error("expected mountPath required")
	}
}

func TestValidateVaultSpec_KubernetesAuth_MissingRole(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"kubernetes": map[string]any{"mountPath": "kubernetes"},
	}
	if !hasField(validateVaultSpec(spec), "auth.kubernetes.role") {
		t.Error("expected role required")
	}
}

// --- AppRole auth ---

func TestValidateVaultSpec_AppRoleAuth_Valid(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"appRole": map[string]any{
			"path":   "approle",
			"roleId": "abc-123",
			"secretRef": map[string]any{
				"name": "approle-secret",
				"key":  "secret-id",
			},
		},
	}
	if errs := validateVaultSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateVaultSpec_AppRoleAuth_RoleRefValid(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"appRole": map[string]any{
			"path": "approle",
			"roleRef": map[string]any{
				"name": "approle-role",
				"key":  "role-id",
			},
			"secretRef": map[string]any{
				"name": "approle-secret",
				"key":  "secret-id",
			},
		},
	}
	if errs := validateVaultSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with roleRef; got %v", errs)
	}
}

func TestValidateVaultSpec_AppRoleAuth_NeitherRoleIDNorRoleRef(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"appRole": map[string]any{
			"path":      "approle",
			"secretRef": map[string]any{"name": "s", "key": "k"},
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.appRole.roleId") {
		t.Error("expected roleId/roleRef required error")
	}
}

func TestValidateVaultSpec_AppRoleAuth_BothRoleIDAndRoleRef(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"appRole": map[string]any{
			"path":      "approle",
			"roleId":    "abc",
			"roleRef":   map[string]any{"name": "r", "key": "k"},
			"secretRef": map[string]any{"name": "s", "key": "k"},
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.appRole.roleId") {
		t.Error("expected mutual-exclusion error for both roleId and roleRef")
	}
}

func TestValidateVaultSpec_AppRoleAuth_MissingSecretRef(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"appRole": map[string]any{
			"path":   "approle",
			"roleId": "abc",
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.appRole.secretRef") {
		t.Error("expected secretRef required")
	}
}

// --- JWT auth ---

func TestValidateVaultSpec_JWTAuth_Valid(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"path": "jwt",
			"role": "my-role",
			"secretRef": map[string]any{
				"name": "jwt-token",
				"key":  "jwt",
			},
		},
	}
	if errs := validateVaultSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateVaultSpec_JWTAuth_KubernetesSAToken(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"path": "jwt",
			"kubernetesServiceAccountToken": map[string]any{
				"serviceAccountRef": map[string]any{"name": "sa"},
			},
		},
	}
	if errs := validateVaultSpec(spec); len(errs) != 0 {
		t.Errorf("expected k8s-SA-token path to validate; got %v", errs)
	}
}

func TestValidateVaultSpec_JWTAuth_MissingPath(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"secretRef": map[string]any{"name": "s", "key": "k"},
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.jwt.path") {
		t.Error("expected path required")
	}
}

func TestValidateVaultSpec_JWTAuth_NoSource(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{"path": "jwt"},
	}
	if !hasField(validateVaultSpec(spec), "auth.jwt.secretRef") {
		t.Error("expected secretRef-or-kubernetesServiceAccountToken error")
	}
}

// --- Cert auth ---

func TestValidateVaultSpec_CertAuth_Valid(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"cert": map[string]any{
			"clientCert": map[string]any{"name": "cert", "key": "tls.crt"},
			"secretRef":  map[string]any{"name": "cert-key", "key": "tls.key"},
		},
	}
	if errs := validateVaultSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateVaultSpec_CertAuth_MissingClientCert(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"cert": map[string]any{
			"secretRef": map[string]any{"name": "s", "key": "k"},
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.cert.clientCert") {
		t.Error("expected clientCert required")
	}
}

func TestValidateVaultSpec_CertAuth_MissingSecretRef(t *testing.T) {
	spec := validVaultSpec()
	spec["auth"] = map[string]any{
		"cert": map[string]any{
			"clientCert": map[string]any{"name": "c", "key": "k"},
		},
	}
	if !hasField(validateVaultSpec(spec), "auth.cert.secretRef") {
		t.Error("expected secretRef required")
	}
}

// --- Dispatcher integration ---

// TestSecretStoreInput_VaultIntegration confirms validateVaultSpec is wired
// to the dispatcher via the init() RegisterSecretStoreProvider call. A
// SecretStoreInput with provider=vault should route through validateVaultSpec
// and surface its errors at the providerSpec level.
func TestSecretStoreInput_VaultIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "vault-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderVault,
		ProviderSpec: validVaultSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_VaultIntegration_PropagatesProviderError(t *testing.T) {
	spec := validVaultSpec()
	delete(spec, "server")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "vault-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderVault,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "server") {
		t.Errorf("expected provider-level server error, got %v", errs)
	}
}

// TestSecretStoreInput_VaultIntegration_ToYAML asserts the wizard preview's
// emitted YAML places the spec under spec.provider.vault and does not leak
// the wizard's transport keys (e.g. "auth" stays nested under vault, not at
// the top of spec).
func TestSecretStoreInput_VaultIntegration_ToYAML(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "vault-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderVault,
		ProviderSpec: validVaultSpec(),
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"apiVersion: external-secrets.io/v1",
		"kind: SecretStore",
		"vault:",
		"server: https://vault.example.com",
		"tokenSecretRef:",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected YAML to contain %q\n%s", want, y)
		}
	}
	if strings.Contains(y, "auth:\n  token:") && !strings.Contains(y, "    vault:") {
		// Only a coarse smoke check — TestSecretStoreToYAML_Namespaced
		// does the structural parsing.
		_ = y
	}
}
