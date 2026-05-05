package wizard

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// validAzureKVSpecSP returns a minimal valid AzureKV provider spec using
// ServicePrincipal auth — the most field-rich auth type, so it covers the most
// validation code. Other auth-method tests override spec fields as needed.
func validAzureKVSpecSP() map[string]any {
	return map[string]any{
		"vaultUrl": "https://my-vault.vault.azure.net",
		"tenantId": "00000000-0000-0000-0000-000000000001",
		"authType": "ServicePrincipal",
		"authSecretRef": map[string]any{
			"clientId": map[string]any{
				"name": "azure-sp-secret",
				"key":  "client-id",
			},
			"clientSecret": map[string]any{
				"name": "azure-sp-secret",
				"key":  "client-secret",
			},
		},
	}
}

// validAzureKVSpecMI returns a minimal valid spec for ManagedIdentity auth.
func validAzureKVSpecMI() map[string]any {
	return map[string]any{
		"vaultUrl": "https://my-vault.vault.azure.net",
		"authType": "ManagedIdentity",
	}
}

// validAzureKVSpecWI returns a minimal valid spec for WorkloadIdentity auth.
func validAzureKVSpecWI() map[string]any {
	return map[string]any{
		"vaultUrl": "https://my-vault.vault.azure.net",
		"tenantId": "00000000-0000-0000-0000-000000000002",
		"authType": "WorkloadIdentity",
		"serviceAccountRef": map[string]any{
			"name": "eso-workload-sa",
		},
	}
}

// --- Top-level field validation ---

func TestValidateAzureKVSpec_SP_Valid(t *testing.T) {
	if errs := validateAzureKVSpec(validAzureKVSpecSP()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateAzureKVSpec_MI_Valid(t *testing.T) {
	if errs := validateAzureKVSpec(validAzureKVSpecMI()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateAzureKVSpec_WI_Valid(t *testing.T) {
	if errs := validateAzureKVSpec(validAzureKVSpecWI()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateAzureKVSpec_MissingVaultUrl(t *testing.T) {
	spec := validAzureKVSpecSP()
	delete(spec, "vaultUrl")
	if !hasField(validateAzureKVSpec(spec), "vaultUrl") {
		t.Error("expected vaultUrl required error")
	}
}

func TestValidateAzureKVSpec_BlankVaultUrl(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["vaultUrl"] = "   "
	if !hasField(validateAzureKVSpec(spec), "vaultUrl") {
		t.Error("expected vaultUrl error for whitespace-only value")
	}
}

func TestValidateAzureKVSpec_NonHTTPSVaultUrl(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["vaultUrl"] = "http://my-vault.vault.azure.net"
	errs := validateAzureKVSpec(spec)
	if !hasField(errs, "vaultUrl") {
		t.Errorf("expected vaultUrl https error; got %v", errs)
	}
}

func TestValidateAzureKVSpec_MissingAuthType(t *testing.T) {
	spec := validAzureKVSpecSP()
	delete(spec, "authType")
	errs := validateAzureKVSpec(spec)
	if !hasField(errs, "authType") {
		t.Errorf("expected authType required error; got %v", errs)
	}
}

func TestValidateAzureKVSpec_InvalidAuthType(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["authType"] = "CertificateAuth"
	errs := validateAzureKVSpec(spec)
	if !hasField(errs, "authType") {
		t.Errorf("expected authType invalid error; got %v", errs)
	}
	// Confirm all three valid values are named in the error message.
	for _, e := range errs {
		if e.Field == "authType" {
			if !strings.Contains(e.Message, "ManagedIdentity") ||
				!strings.Contains(e.Message, "ServicePrincipal") ||
				!strings.Contains(e.Message, "WorkloadIdentity") {
				t.Errorf("expected all auth type names in error message, got %q", e.Message)
			}
		}
	}
}

// --- ManagedIdentity auth ---

func TestValidateAzureKVSpec_MI_WithOptionalTenantId(t *testing.T) {
	spec := validAzureKVSpecMI()
	spec["tenantId"] = "optional-tenant"
	if errs := validateAzureKVSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with optional tenantId; got %v", errs)
	}
}

func TestValidateAzureKVSpec_MI_WithOptionalIdentityId(t *testing.T) {
	spec := validAzureKVSpecMI()
	spec["identityId"] = "some-client-id"
	if errs := validateAzureKVSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with optional identityId; got %v", errs)
	}
}

// ManagedIdentity: tenantId is truly optional (not required like SP/WI).
func TestValidateAzureKVSpec_MI_NoTenantIdIsOk(t *testing.T) {
	spec := validAzureKVSpecMI()
	// spec already has no tenantId — should pass clean.
	if errs := validateAzureKVSpec(spec); len(errs) != 0 {
		t.Errorf("expected ManagedIdentity to pass without tenantId; got %v", errs)
	}
}

// --- ServicePrincipal auth ---

func TestValidateAzureKVSpec_SP_MissingTenantId(t *testing.T) {
	spec := validAzureKVSpecSP()
	delete(spec, "tenantId")
	if !hasField(validateAzureKVSpec(spec), "tenantId") {
		t.Error("expected tenantId required for ServicePrincipal")
	}
}

func TestValidateAzureKVSpec_SP_BlankTenantId(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["tenantId"] = "  "
	if !hasField(validateAzureKVSpec(spec), "tenantId") {
		t.Error("expected tenantId error for whitespace-only value")
	}
}

func TestValidateAzureKVSpec_SP_MissingAuthSecretRef(t *testing.T) {
	spec := validAzureKVSpecSP()
	delete(spec, "authSecretRef")
	if !hasField(validateAzureKVSpec(spec), "authSecretRef") {
		t.Error("expected authSecretRef required for ServicePrincipal")
	}
}

func TestValidateAzureKVSpec_SP_MissingClientId(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["authSecretRef"] = map[string]any{
		"clientSecret": map[string]any{"name": "s", "key": "k"},
	}
	if !hasField(validateAzureKVSpec(spec), "authSecretRef.clientId") {
		t.Error("expected authSecretRef.clientId required")
	}
}

func TestValidateAzureKVSpec_SP_MissingClientSecret(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["authSecretRef"] = map[string]any{
		"clientId": map[string]any{"name": "s", "key": "k"},
	}
	if !hasField(validateAzureKVSpec(spec), "authSecretRef.clientSecret") {
		t.Error("expected authSecretRef.clientSecret required")
	}
}

func TestValidateAzureKVSpec_SP_ClientIdMissingName(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["authSecretRef"] = map[string]any{
		"clientId":     map[string]any{"key": "client-id"}, // missing name
		"clientSecret": map[string]any{"name": "s", "key": "k"},
	}
	if !hasField(validateAzureKVSpec(spec), "authSecretRef.clientId.name") {
		t.Error("expected authSecretRef.clientId.name required")
	}
}

func TestValidateAzureKVSpec_SP_ClientIdMissingKey(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["authSecretRef"] = map[string]any{
		"clientId":     map[string]any{"name": "s"}, // missing key
		"clientSecret": map[string]any{"name": "s", "key": "k"},
	}
	if !hasField(validateAzureKVSpec(spec), "authSecretRef.clientId.key") {
		t.Error("expected authSecretRef.clientId.key required")
	}
}

func TestValidateAzureKVSpec_SP_ClientIdBadName(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["authSecretRef"] = map[string]any{
		"clientId":     map[string]any{"name": "BadName", "key": "k"},
		"clientSecret": map[string]any{"name": "s", "key": "k"},
	}
	if !hasField(validateAzureKVSpec(spec), "authSecretRef.clientId.name") {
		t.Error("expected authSecretRef.clientId.name DNS error")
	}
}

func TestValidateAzureKVSpec_SP_ClientSecretMissingKey(t *testing.T) {
	spec := validAzureKVSpecSP()
	spec["authSecretRef"] = map[string]any{
		"clientId":     map[string]any{"name": "s", "key": "k"},
		"clientSecret": map[string]any{"name": "s"}, // missing key
	}
	if !hasField(validateAzureKVSpec(spec), "authSecretRef.clientSecret.key") {
		t.Error("expected authSecretRef.clientSecret.key required")
	}
}

// --- WorkloadIdentity auth ---

func TestValidateAzureKVSpec_WI_MissingTenantId(t *testing.T) {
	spec := validAzureKVSpecWI()
	delete(spec, "tenantId")
	if !hasField(validateAzureKVSpec(spec), "tenantId") {
		t.Error("expected tenantId required for WorkloadIdentity")
	}
}

func TestValidateAzureKVSpec_WI_MissingServiceAccountRef(t *testing.T) {
	spec := validAzureKVSpecWI()
	delete(spec, "serviceAccountRef")
	if !hasField(validateAzureKVSpec(spec), "serviceAccountRef") {
		t.Error("expected serviceAccountRef required for WorkloadIdentity")
	}
}

func TestValidateAzureKVSpec_WI_MissingServiceAccountName(t *testing.T) {
	spec := validAzureKVSpecWI()
	spec["serviceAccountRef"] = map[string]any{} // missing name
	if !hasField(validateAzureKVSpec(spec), "serviceAccountRef.name") {
		t.Error("expected serviceAccountRef.name required")
	}
}

func TestValidateAzureKVSpec_WI_BadServiceAccountName(t *testing.T) {
	spec := validAzureKVSpecWI()
	spec["serviceAccountRef"] = map[string]any{"name": "Bad_Name"}
	if !hasField(validateAzureKVSpec(spec), "serviceAccountRef.name") {
		t.Error("expected serviceAccountRef.name DNS label error")
	}
}

func TestValidateAzureKVSpec_WI_WithOptionalClientId(t *testing.T) {
	spec := validAzureKVSpecWI()
	spec["clientId"] = "override-client-id"
	if errs := validateAzureKVSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with optional clientId; got %v", errs)
	}
}

// --- Dispatcher integration ---

// TestSecretStoreInput_AzureKVIntegration confirms validateAzureKVSpec is
// wired to the dispatcher via the init() RegisterSecretStoreProvider call.
func TestSecretStoreInput_AzureKVIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "azure-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAzure,
		ProviderSpec: validAzureKVSpecSP(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_AzureKVIntegration_PropagatesProviderError(t *testing.T) {
	spec := validAzureKVSpecSP()
	delete(spec, "vaultUrl")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "azure-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAzure,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "vaultUrl") {
		t.Errorf("expected provider-level vaultUrl error, got %v", errs)
	}
}

// TestSecretStoreInput_AzureKVIntegration_ToYAML asserts the YAML places the
// spec under spec.provider.azurekv with the correct top-level shape.
func TestSecretStoreInput_AzureKVIntegration_ToYAML(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "azure-kv-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAzure,
		ProviderSpec: validAzureKVSpecSP(),
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		"apiVersion: external-secrets.io/v1",
		"kind: SecretStore",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected YAML to contain %q\n%s", want, y)
		}
	}

	// Structural assertion: confirm spec.provider.azurekv.vaultUrl is present.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	spec, _ := doc["spec"].(map[string]any)
	provider, _ := spec["provider"].(map[string]any)
	azureSpec, _ := provider["azurekv"].(map[string]any)
	if azureSpec == nil {
		t.Fatalf("expected spec.provider.azurekv, got provider keys: %v", keys(provider))
	}
	if azureSpec["vaultUrl"] == nil {
		t.Errorf("expected spec.provider.azurekv.vaultUrl to be present; got %v", azureSpec)
	}
	if azureSpec["authType"] == nil {
		t.Errorf("expected spec.provider.azurekv.authType to be present; got %v", azureSpec)
	}
}

// TestSecretStoreInput_AzureKVIntegration_ClusterScope verifies a
// ClusterSecretStore is emitted correctly for cluster scope.
func TestSecretStoreInput_AzureKVIntegration_ClusterScope(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeCluster,
		Name:         "shared-azure-store",
		Provider:     SecretStoreProviderAzure,
		ProviderSpec: validAzureKVSpecMI(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Fatalf("expected no errors for cluster scope, got %v", errs)
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(y, "kind: ClusterSecretStore") {
		t.Errorf("expected ClusterSecretStore in YAML\n%s", y)
	}
}
