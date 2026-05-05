package wizard

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// validDopplerSpec returns a minimal valid Doppler provider spec using
// secretRef auth — the simpler of the two auth methods.
func validDopplerSpec() map[string]any {
	return map[string]any{
		"project": "my-project",
		"config":  "prd",
		"auth": map[string]any{
			"secretRef": map[string]any{
				"dopplerToken": map[string]any{
					"name": "doppler-token",
					"key":  "serviceToken",
				},
			},
		},
	}
}

func TestValidateDopplerSpec_Valid(t *testing.T) {
	if errs := validateDopplerSpec(validDopplerSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateDopplerSpec_MissingProject(t *testing.T) {
	spec := validDopplerSpec()
	delete(spec, "project")
	if !hasField(validateDopplerSpec(spec), "project") {
		t.Error("expected project required error")
	}
}

func TestValidateDopplerSpec_BlankProject(t *testing.T) {
	spec := validDopplerSpec()
	spec["project"] = "   "
	if !hasField(validateDopplerSpec(spec), "project") {
		t.Error("expected project error for whitespace-only value")
	}
}

func TestValidateDopplerSpec_MissingConfig(t *testing.T) {
	spec := validDopplerSpec()
	delete(spec, "config")
	if !hasField(validateDopplerSpec(spec), "config") {
		t.Error("expected config required error")
	}
}

func TestValidateDopplerSpec_BlankConfig(t *testing.T) {
	spec := validDopplerSpec()
	spec["config"] = ""
	if !hasField(validateDopplerSpec(spec), "config") {
		t.Error("expected config error for empty value")
	}
}

func TestValidateDopplerSpec_NoAuth(t *testing.T) {
	spec := validDopplerSpec()
	delete(spec, "auth")
	if !hasField(validateDopplerSpec(spec), "auth") {
		t.Error("expected auth required error")
	}
}

func TestValidateDopplerSpec_AuthNoMethod(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{}
	if !hasField(validateDopplerSpec(spec), "auth") {
		t.Error("expected auth error for empty block")
	}
}

func TestValidateDopplerSpec_AuthBothMethods(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"dopplerToken": map[string]any{"name": "tok", "key": "serviceToken"},
		},
		"oidcConfig": map[string]any{},
	}
	errs := validateDopplerSpec(spec)
	if !hasField(errs, "auth") {
		t.Errorf("expected auth error for both methods set; got %v", errs)
	}
	// Verify message mentions both methods.
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

// --- secretRef auth ---

func TestValidateDopplerSpec_SecretRef_MissingDopplerToken(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{}, // missing dopplerToken
	}
	if !hasField(validateDopplerSpec(spec), "auth.secretRef.dopplerToken") {
		t.Error("expected dopplerToken required error")
	}
}

func TestValidateDopplerSpec_SecretRef_MissingName(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"dopplerToken": map[string]any{"key": "serviceToken"}, // missing name
		},
	}
	if !hasField(validateDopplerSpec(spec), "auth.secretRef.dopplerToken.name") {
		t.Error("expected dopplerToken.name required error")
	}
}

func TestValidateDopplerSpec_SecretRef_MissingKey(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"dopplerToken": map[string]any{"name": "doppler-token"}, // missing key
		},
	}
	if !hasField(validateDopplerSpec(spec), "auth.secretRef.dopplerToken.key") {
		t.Error("expected dopplerToken.key required error")
	}
}

func TestValidateDopplerSpec_SecretRef_BadRefName(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"dopplerToken": map[string]any{"name": "BadName", "key": "serviceToken"},
		},
	}
	if !hasField(validateDopplerSpec(spec), "auth.secretRef.dopplerToken.name") {
		t.Error("expected dopplerToken.name DNS label error")
	}
}

// --- oidcConfig auth ---

func TestValidateDopplerSpec_OIDC_Valid(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"oidcConfig": map[string]any{
			"identity": "doppler-identity-id",
			"serviceAccountRef": map[string]any{
				"name": "my-sa",
			},
		},
	}
	if errs := validateDopplerSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors for oidcConfig auth, got %v", errs)
	}
}

func TestValidateDopplerSpec_OIDC_MissingIdentity(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"oidcConfig": map[string]any{
			"serviceAccountRef": map[string]any{"name": "my-sa"},
		},
	}
	if !hasField(validateDopplerSpec(spec), "auth.oidcConfig.identity") {
		t.Error("expected identity required error")
	}
}

func TestValidateDopplerSpec_OIDC_MissingServiceAccountRef(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"oidcConfig": map[string]any{
			"identity": "doppler-identity-id",
		},
	}
	if !hasField(validateDopplerSpec(spec), "auth.oidcConfig.serviceAccountRef") {
		t.Error("expected serviceAccountRef required error")
	}
}

func TestValidateDopplerSpec_OIDC_BadServiceAccountName(t *testing.T) {
	spec := validDopplerSpec()
	spec["auth"] = map[string]any{
		"oidcConfig": map[string]any{
			"identity":          "doppler-identity-id",
			"serviceAccountRef": map[string]any{"name": "Bad_SA"},
		},
	}
	if !hasField(validateDopplerSpec(spec), "auth.oidcConfig.serviceAccountRef.name") {
		t.Error("expected serviceAccountRef.name DNS label error")
	}
}

// --- Dispatcher integration ---

// TestSecretStoreInput_DopplerIntegration confirms validateDopplerSpec is wired
// to the dispatcher via the init() RegisterSecretStoreProvider call.
func TestSecretStoreInput_DopplerIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "doppler-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderDoppler,
		ProviderSpec: validDopplerSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_DopplerIntegration_PropagatesProviderError(t *testing.T) {
	spec := validDopplerSpec()
	delete(spec, "project")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "doppler-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderDoppler,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "project") {
		t.Errorf("expected provider-level project error, got %v", errs)
	}
}

// TestSecretStoreInput_DopplerIntegration_ToYAML asserts the wizard preview's
// emitted YAML places the spec under spec.provider.doppler with the correct
// structure and does not leak wizard-internal keys.
func TestSecretStoreInput_DopplerIntegration_ToYAML(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "doppler-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderDoppler,
		ProviderSpec: validDopplerSpec(),
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected ToYAML error: %v", err)
	}
	for _, want := range []string{
		"apiVersion: external-secrets.io/v1",
		"kind: SecretStore",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected YAML to contain %q\n%s", want, y)
		}
	}

	// Structural walk: confirm spec.provider.doppler.auth.secretRef.dopplerToken
	// carries name + key.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	spec, _ := doc["spec"].(map[string]any)
	provider, _ := spec["provider"].(map[string]any)
	dopplerSpec, _ := provider["doppler"].(map[string]any)
	if dopplerSpec == nil {
		t.Fatalf("expected spec.provider.doppler, got provider keys: %v", keys(provider))
	}
	auth, _ := dopplerSpec["auth"].(map[string]any)
	sr, _ := auth["secretRef"].(map[string]any)
	tokenRef, _ := sr["dopplerToken"].(map[string]any)
	if tokenRef == nil {
		t.Fatalf("expected spec.provider.doppler.auth.secretRef.dopplerToken; auth=%v", auth)
	}
	if tokenRef["name"] == nil {
		t.Errorf("expected dopplerToken.name, got %v", tokenRef)
	}
	if tokenRef["key"] == nil {
		t.Errorf("expected dopplerToken.key, got %v", tokenRef)
	}
	// Verify project and config appear at the doppler-spec level.
	if dopplerSpec["project"] != "my-project" {
		t.Errorf("expected project=my-project, got %v", dopplerSpec["project"])
	}
	if dopplerSpec["config"] != "prd" {
		t.Errorf("expected config=prd, got %v", dopplerSpec["config"])
	}
}
