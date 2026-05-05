package wizard

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// valid1PasswordSpec returns a minimal valid 1Password Connect provider spec.
// The ESO provider key is "onepassword"; auth uses connectTokenSecretRef.
func valid1PasswordSpec() map[string]any {
	return map[string]any{
		"connectHost": "https://connect.example.com:8080",
		"auth": map[string]any{
			"secretRef": map[string]any{
				"connectTokenSecretRef": map[string]any{
					"name": "op-connect-token",
					"key":  "token",
				},
			},
		},
		"vaults": map[string]any{
			"production": float64(1),
		},
	}
}

func TestValidate1PasswordSpec_Valid(t *testing.T) {
	if errs := validate1PasswordSpec(valid1PasswordSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

// --- connectHost ---

func TestValidate1PasswordSpec_MissingConnectHost(t *testing.T) {
	spec := valid1PasswordSpec()
	delete(spec, "connectHost")
	if !hasField(validate1PasswordSpec(spec), "connectHost") {
		t.Error("expected connectHost required error")
	}
}

func TestValidate1PasswordSpec_BlankConnectHost(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["connectHost"] = "   "
	if !hasField(validate1PasswordSpec(spec), "connectHost") {
		t.Error("expected connectHost error for whitespace-only value")
	}
}

func TestValidate1PasswordSpec_NonHTTPSConnectHost(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["connectHost"] = "http://connect.example.com"
	if !hasField(validate1PasswordSpec(spec), "connectHost") {
		t.Error("expected connectHost error for http scheme")
	}
}

func TestValidate1PasswordSpec_InClusterConnectHost(t *testing.T) {
	// Connect server may run in-cluster at a private address.
	spec := valid1PasswordSpec()
	spec["connectHost"] = "https://onepassword-connect.default.svc.cluster.local:8080"
	if errs := validate1PasswordSpec(spec); len(errs) != 0 {
		t.Errorf("expected private https address to validate, got %v", errs)
	}
}

// --- auth block ---

func TestValidate1PasswordSpec_NoAuth(t *testing.T) {
	spec := valid1PasswordSpec()
	delete(spec, "auth")
	if !hasField(validate1PasswordSpec(spec), "auth") {
		t.Error("expected auth required error")
	}
}

func TestValidate1PasswordSpec_NoSecretRef(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["auth"] = map[string]any{}
	if !hasField(validate1PasswordSpec(spec), "auth.secretRef") {
		t.Error("expected auth.secretRef required error")
	}
}

func TestValidate1PasswordSpec_NoConnectTokenSecretRef(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{},
	}
	if !hasField(validate1PasswordSpec(spec), "auth.secretRef.connectTokenSecretRef") {
		t.Error("expected connectTokenSecretRef required error")
	}
}

func TestValidate1PasswordSpec_ConnectTokenRef_MissingName(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"connectTokenSecretRef": map[string]any{
				"key": "token", // name omitted
			},
		},
	}
	if !hasField(validate1PasswordSpec(spec), "auth.secretRef.connectTokenSecretRef.name") {
		t.Error("expected connectTokenSecretRef.name required error")
	}
}

func TestValidate1PasswordSpec_ConnectTokenRef_MissingKey(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"connectTokenSecretRef": map[string]any{
				"name": "op-connect-token", // key omitted
			},
		},
	}
	if !hasField(validate1PasswordSpec(spec), "auth.secretRef.connectTokenSecretRef.key") {
		t.Error("expected connectTokenSecretRef.key required error")
	}
}

func TestValidate1PasswordSpec_ConnectTokenRef_BadName(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"connectTokenSecretRef": map[string]any{
				"name": "InvalidName_With_Underscores",
				"key":  "token",
			},
		},
	}
	if !hasField(validate1PasswordSpec(spec), "auth.secretRef.connectTokenSecretRef.name") {
		t.Error("expected connectTokenSecretRef.name DNS label error")
	}
}

// --- vaults ---

func TestValidate1PasswordSpec_NoVaults(t *testing.T) {
	spec := valid1PasswordSpec()
	delete(spec, "vaults")
	if !hasField(validate1PasswordSpec(spec), "vaults") {
		t.Error("expected vaults required error")
	}
}

func TestValidate1PasswordSpec_EmptyVaults(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["vaults"] = map[string]any{}
	if !hasField(validate1PasswordSpec(spec), "vaults") {
		t.Error("expected vaults non-empty error")
	}
}

func TestValidate1PasswordSpec_MultipleVaults(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["vaults"] = map[string]any{
		"production": float64(1),
		"staging":    float64(2),
	}
	if errs := validate1PasswordSpec(spec); len(errs) != 0 {
		t.Errorf("expected multiple vaults to validate, got %v", errs)
	}
}

func TestValidate1PasswordSpec_WrongVaultsType(t *testing.T) {
	spec := valid1PasswordSpec()
	spec["vaults"] = "not-a-map"
	if !hasField(validate1PasswordSpec(spec), "vaults") {
		t.Error("expected vaults type error")
	}
}

// --- Dispatcher integration ---

// TestSecretStoreInput_1PasswordIntegration confirms validate1PasswordSpec is
// wired to the dispatcher via the init() RegisterSecretStoreProvider call.
func TestSecretStoreInput_1PasswordIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "op-connect-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderOnePassword,
		ProviderSpec: valid1PasswordSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_1PasswordIntegration_PropagatesProviderError(t *testing.T) {
	spec := valid1PasswordSpec()
	delete(spec, "connectHost")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "op-connect-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderOnePassword,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "connectHost") {
		t.Errorf("expected connectHost error via dispatcher, got %v", errs)
	}
}

// TestSecretStoreInput_1PasswordIntegration_ProviderKeyCorrect verifies the
// emitted YAML uses "onepassword" (the real ESO key) not "onepasswordsdk"
// (the incorrect U18 value). This is the regression guard for the enum fix.
func TestSecretStoreInput_1PasswordIntegration_ProviderKeyCorrect(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "op-connect-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderOnePassword,
		ProviderSpec: valid1PasswordSpec(),
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The provider key in the emitted YAML must be "onepassword", not "onepasswordsdk".
	if strings.Contains(y, "onepasswordsdk") {
		t.Errorf("emitted YAML contains deprecated key 'onepasswordsdk'; expected 'onepassword'\n%s", y)
	}
	if !strings.Contains(y, "onepassword:") {
		t.Errorf("emitted YAML does not contain 'onepassword:' provider key\n%s", y)
	}

	// Structural assertion: walk spec.provider.onepassword.auth.secretRef.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	specBlock, _ := doc["spec"].(map[string]any)
	provider, _ := specBlock["provider"].(map[string]any)
	opSpec, _ := provider["onepassword"].(map[string]any)
	if opSpec == nil {
		t.Fatalf("expected spec.provider.onepassword, got provider keys: %v", keys(provider))
	}
	auth, _ := opSpec["auth"].(map[string]any)
	secretRef, _ := auth["secretRef"].(map[string]any)
	if secretRef == nil {
		t.Fatalf("expected spec.provider.onepassword.auth.secretRef, got auth=%v", auth)
	}
	tokenRef, _ := secretRef["connectTokenSecretRef"].(map[string]any)
	if tokenRef == nil {
		t.Fatalf("expected connectTokenSecretRef, got secretRef=%v", secretRef)
	}
	if tokenRef["name"] == nil {
		t.Errorf("expected connectTokenSecretRef.name, got %v", tokenRef)
	}
	if tokenRef["key"] == nil {
		t.Errorf("expected connectTokenSecretRef.key, got %v", tokenRef)
	}
}

// TestSecretStoreInput_1PasswordIntegration_ClusterScope verifies the wizard
// emits ClusterSecretStore for cluster scope.
func TestSecretStoreInput_1PasswordIntegration_ClusterScope(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeCluster,
		Name:         "op-connect-cluster-store",
		Provider:     SecretStoreProviderOnePassword,
		ProviderSpec: valid1PasswordSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors for cluster scope, got %v", errs)
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(y, "kind: ClusterSecretStore") {
		t.Errorf("expected ClusterSecretStore kind\n%s", y)
	}
}
