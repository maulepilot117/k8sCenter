package wizard

import (
	"strings"
	"testing"
)

// withTestProviderValidator registers a stub validator for the duration of a
// test, restoring the prior registration (or absence) on cleanup. Lets unit
// tests exercise the registered-validator branch of Validate() without
// pulling in any real provider code (those land in Unit 19).
func withTestProviderValidator(t *testing.T, p SecretStoreProvider, v providerValidator) {
	t.Helper()
	prev, hadPrev := lookupProviderValidator(p)
	RegisterSecretStoreProvider(p, v)
	t.Cleanup(func() {
		if hadPrev {
			RegisterSecretStoreProvider(p, prev)
			return
		}
		// No prior validator — best-effort delete by registering a nil
		// sentinel and accepting the leftover map entry. Nothing else in the
		// scaffold reads this map after the test ends, so a leftover key is
		// harmless. Real providers in Unit 19 will register their own
		// validators in init() before any test runs.
		providerValidatorsMu.Lock()
		delete(providerValidators, p)
		providerValidatorsMu.Unlock()
	})
}

func validNamespacedStoreInput(provider SecretStoreProvider) SecretStoreInput {
	return SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "vault-store",
		Namespace:    "apps",
		Provider:     provider,
		ProviderSpec: map[string]any{"server": "https://vault.example.com"},
	}
}

func validClusterStoreInput(provider SecretStoreProvider) SecretStoreInput {
	return SecretStoreInput{
		Scope:        StoreScopeCluster,
		Name:         "shared-vault-store",
		Provider:     provider,
		ProviderSpec: map[string]any{"server": "https://vault.example.com"},
	}
}

// allowAllValidator returns no errors regardless of input — stand-in for a
// real provider validator that hasn't been registered yet.
var allowAllValidator providerValidator = func(_ map[string]any) []FieldError { return nil }

func TestSecretStoreValidate_Namespaced_Valid(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, allowAllValidator)
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestSecretStoreValidate_Cluster_Valid(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, allowAllValidator)
	s := validClusterStoreInput(SecretStoreProviderVault)
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestSecretStoreValidate_BadScope(t *testing.T) {
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	s.Scope = "garbage"
	if !hasField(s.Validate(), "scope") {
		t.Error("expected scope error for invalid scope")
	}
}

func TestSecretStoreValidate_NamespacedRequiresNamespace(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, allowAllValidator)
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	s.Namespace = ""
	if !hasField(s.Validate(), "namespace") {
		t.Error("expected namespace error for namespaced scope without namespace")
	}
}

func TestSecretStoreValidate_ClusterIgnoresNamespace(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, allowAllValidator)
	s := validClusterStoreInput(SecretStoreProviderVault)
	s.Namespace = "leaked-namespace"
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("namespace should be ignored for cluster scope; got errors %v", errs)
	}
}

func TestSecretStoreValidate_BadName(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, allowAllValidator)
	cases := []string{"", "BadName", "-leading", "trailing-", strings.Repeat("a", 64)}
	for _, n := range cases {
		s := validNamespacedStoreInput(SecretStoreProviderVault)
		s.Name = n
		if !hasField(s.Validate(), "name") {
			t.Errorf("expected name error for %q", n)
		}
	}
}

func TestSecretStoreValidate_MissingProvider(t *testing.T) {
	s := validNamespacedStoreInput("")
	if !hasField(s.Validate(), "provider") {
		t.Error("expected provider error when empty")
	}
}

func TestSecretStoreValidate_UnknownProvider(t *testing.T) {
	s := validNamespacedStoreInput("not-a-real-provider")
	errs := s.Validate()
	if !hasField(errs, "provider") {
		t.Fatalf("expected provider error for unknown provider")
	}
	// Verify the error mentions the YAML editor escape hatch so users can
	// route around the wizard without reading source.
	found := false
	for _, e := range errs {
		if e.Field == "provider" && strings.Contains(e.Message, "YAML") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected provider error message to reference YAML editor; got %v", errs)
	}
}

func TestSecretStoreValidate_NoValidatorRegistered_FallsThrough(t *testing.T) {
	// Vault is a valid provider key, but with no registered validator the
	// dispatcher should reject the input rather than silently emit YAML.
	// Use a provider that the per-test cleanup will not have registered.
	providerValidatorsMu.Lock()
	delete(providerValidators, SecretStoreProviderInfisical)
	providerValidatorsMu.Unlock()

	s := validNamespacedStoreInput(SecretStoreProviderInfisical)
	errs := s.Validate()
	if !hasField(errs, "provider") {
		t.Fatalf("expected provider error when no validator registered; got %v", errs)
	}
}

func TestSecretStoreValidate_DelegatesToRegisteredValidator(t *testing.T) {
	called := false
	withTestProviderValidator(t, SecretStoreProviderVault, func(spec map[string]any) []FieldError {
		called = true
		if spec["server"] != "https://vault.example.com" {
			return []FieldError{{Field: "server", Message: "expected"}}
		}
		return nil
	})
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected validator to pass, got %v", errs)
	}
	if !called {
		t.Error("registered validator was not called")
	}
}

func TestSecretStoreValidate_RegisteredValidatorErrorsPropagate(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, func(_ map[string]any) []FieldError {
		return []FieldError{{Field: "server", Message: "bad URL"}}
	})
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	if !hasField(s.Validate(), "server") {
		t.Error("expected validator error to propagate")
	}
}

func TestSecretStoreValidate_MissingProviderSpec(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, allowAllValidator)
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	s.ProviderSpec = nil
	if !hasField(s.Validate(), "providerSpec") {
		t.Error("expected providerSpec required error")
	}
}

func TestSecretStoreToYAML_Namespaced(t *testing.T) {
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"apiVersion: external-secrets.io/v1",
		"kind: SecretStore",
		"name: vault-store",
		"namespace: apps",
		"provider:",
		"vault:",
		"server: https://vault.example.com",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected YAML to contain %q\n%s", want, y)
		}
	}
}

func TestSecretStoreToYAML_Cluster(t *testing.T) {
	s := validClusterStoreInput(SecretStoreProviderVault)
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(y, "kind: ClusterSecretStore") {
		t.Errorf("expected ClusterSecretStore kind; got\n%s", y)
	}
	if strings.Contains(y, "namespace:") {
		t.Errorf("ClusterSecretStore must not emit namespace; got\n%s", y)
	}
}

func TestSecretStoreToYAML_OmitsRefreshIntervalWhenEmpty(t *testing.T) {
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(y, "refreshInterval") {
		t.Errorf("expected refreshInterval omitted when empty; got\n%s", y)
	}
}

func TestSecretStoreToYAML_IncludesRefreshIntervalWhenSet(t *testing.T) {
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	s.RefreshInterval = "1h"
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(y, "refreshInterval: 1h") {
		t.Errorf("expected refreshInterval emitted; got\n%s", y)
	}
}

func TestRegisterSecretStoreProvider_OverridesPriorRegistration(t *testing.T) {
	// Verify the registry replaces existing entries rather than appending.
	first := func(_ map[string]any) []FieldError {
		return []FieldError{{Field: "x", Message: "first"}}
	}
	second := func(_ map[string]any) []FieldError {
		return []FieldError{{Field: "x", Message: "second"}}
	}
	withTestProviderValidator(t, SecretStoreProviderAzure, first)
	withTestProviderValidator(t, SecretStoreProviderAzure, second)

	s := validNamespacedStoreInput(SecretStoreProviderAzure)
	errs := s.Validate()
	for _, e := range errs {
		if e.Field == "x" && e.Message != "second" {
			t.Errorf("expected second validator to win; got %q", e.Message)
		}
	}
}
