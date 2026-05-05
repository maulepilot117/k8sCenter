package wizard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
		// No prior validator — delete the map entry entirely so the test
		// cannot pollute sibling tests. providerValidators is written
		// sequentially (tests run -count=N not concurrently) so no lock needed.
		delete(providerValidators, p)
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
		Scope:    StoreScopeCluster,
		Name:     "shared-vault-store",
		Provider: provider,
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

// TestSecretStoreValidate_ClusterRejectsNamespace asserts that a non-empty
// Namespace field is rejected for cluster-scoped stores. Previously the wizard
// silently ignored the field; now it returns a validation error so the frontend
// can surface the problem rather than emit a misleading ClusterSecretStore with
// a namespace set in metadata.
func TestSecretStoreValidate_ClusterRejectsNamespace(t *testing.T) {
	withTestProviderValidator(t, SecretStoreProviderVault, allowAllValidator)
	s := validClusterStoreInput(SecretStoreProviderVault)
	s.Namespace = "leaked-namespace"
	errs := s.Validate()
	if !hasField(errs, "namespace") {
		t.Errorf("expected namespace error for cluster scope with non-empty namespace; got errors %v", errs)
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

// TestSecretStoreValidate_NoValidatorRegistered_FallsThrough uses
// withTestProviderValidator with a synthetic test-only provider key so the
// test doesn't side-effect the real provider registry. The test verifies that
// a recognized but unregistered provider key is rejected cleanly rather than
// silently emitting a half-formed YAML.
func TestSecretStoreValidate_NoValidatorRegistered_FallsThrough(t *testing.T) {
	const syntheticKey SecretStoreProvider = "test-only-unregistered"

	// Temporarily register the synthetic key as a valid (but unimplemented)
	// provider so validSecretStoreProviders lets it through to the dispatcher.
	validSecretStoreProviders[syntheticKey] = true
	t.Cleanup(func() { delete(validSecretStoreProviders, syntheticKey) })

	// No validator registered for syntheticKey — dispatcher must fall through.
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "test-store",
		Namespace:    "apps",
		Provider:     syntheticKey,
		ProviderSpec: map[string]any{"dummy": "value"},
	}
	errs := s.Validate()
	if !hasField(errs, "provider") {
		t.Fatalf("expected provider error when no validator registered; got %v", errs)
	}
	found := false
	for _, e := range errs {
		if e.Field == "provider" && strings.Contains(e.Message, "not yet implemented") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fall-through message to say 'not yet implemented'; got %v", errs)
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

// TestSecretStoreToYAML_Namespaced uses parsed YAML assertions for structural
// fields to avoid false positives from serialization order or whitespace.
func TestSecretStoreToYAML_Namespaced(t *testing.T) {
	s := validNamespacedStoreInput(SecretStoreProviderVault)
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Smoke-check the apiVersion line with substring (cheap + readable).
	if !strings.Contains(y, "apiVersion: external-secrets.io/v1") {
		t.Errorf("expected YAML to contain apiVersion line; got\n%s", y)
	}

	// Parse and walk the structure for the real assertions.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}

	if doc["kind"] != "SecretStore" {
		t.Errorf("expected kind SecretStore, got %v", doc["kind"])
	}

	meta, _ := doc["metadata"].(map[string]any)
	if meta == nil {
		t.Fatal("missing metadata")
	}
	if meta["name"] != "vault-store" {
		t.Errorf("expected name vault-store, got %v", meta["name"])
	}
	if meta["namespace"] != "apps" {
		t.Errorf("expected namespace apps, got %v", meta["namespace"])
	}

	spec, _ := doc["spec"].(map[string]any)
	if spec == nil {
		t.Fatal("missing spec")
	}
	provider, _ := spec["provider"].(map[string]any)
	if provider == nil {
		t.Fatal("missing spec.provider")
	}
	vaultSpec, _ := provider["vault"].(map[string]any)
	if vaultSpec == nil {
		t.Fatalf("expected spec.provider.vault, got keys: %v", keys(provider))
	}
	if vaultSpec["server"] != "https://vault.example.com" {
		t.Errorf("expected spec.provider.vault.server=https://vault.example.com, got %v", vaultSpec["server"])
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

// TestSecretStoreToYAML_AWSPSTranslatesToAWS verifies that the synthetic
// "awsps" UX discriminator is correctly translated to a real ESO v1
// spec.provider.aws block with service: ParameterStore injected.
// ESO v1 has no "awsps" provider key — both SM and PS live under spec.provider.aws;
// the `service` field (SecretsManager|ParameterStore) distinguishes them.
// (Verified via external-secrets/external-secrets main@apis/externalsecrets/v1/secretstore_types.go)
func TestSecretStoreToYAML_AWSPSTranslatesToAWS(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "ps-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAWSPS,
		ProviderSpec: map[string]any{"region": "us-east-1"},
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(y, "awsps:") {
		t.Errorf("YAML must not contain synthetic 'awsps' key; got\n%s", y)
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}

	spec, _ := doc["spec"].(map[string]any)
	provider, _ := spec["provider"].(map[string]any)
	awsSpec, _ := provider["aws"].(map[string]any)
	if awsSpec == nil {
		t.Fatalf("expected spec.provider.aws, got provider keys: %v", keys(provider))
	}
	if awsSpec["service"] != "ParameterStore" {
		t.Errorf("expected service=ParameterStore, got %v", awsSpec["service"])
	}
	if awsSpec["region"] != "us-east-1" {
		t.Errorf("expected region=us-east-1 preserved; got %v", awsSpec["region"])
	}
}

// TestRegisterSecretStoreProvider_OverridesPriorRegistration verifies the
// registry replaces existing entries rather than appending. Uses positive
// assertions: exactly one error has Field=="x" and Message=="second"; no
// error has Message=="first".
func TestRegisterSecretStoreProvider_OverridesPriorRegistration(t *testing.T) {
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

	var xErrors []FieldError
	for _, e := range errs {
		if e.Field == "x" {
			xErrors = append(xErrors, e)
		}
	}
	if len(xErrors) != 1 {
		t.Fatalf("expected exactly 1 error with Field==x, got %d: %v", len(xErrors), xErrors)
	}
	if xErrors[0].Message != "second" {
		t.Errorf("expected Message=second (second validator wins); got %q", xErrors[0].Message)
	}
	for _, e := range errs {
		if e.Message == "first" {
			t.Errorf("first validator must not appear in results; got %v", errs)
		}
	}
}

// --- HTTP handler tests (#7) ---

// TestHandleSecretStorePreview_NamespacedScope verifies the route factory
// bakes Scope=Namespaced and the request body cannot override it. Uses Vault
// (registered in U19) so the preview produces YAML rather than the
// fall-through error from U18; the absence of an "auth required" error
// proves the validator dispatched correctly.
func TestHandleSecretStorePreview_NamespacedScope(t *testing.T) {
	h := testHandler()
	input := map[string]any{
		"name":      "my-store",
		"namespace": "apps",
		"provider":  "vault",
		"providerSpec": map[string]any{
			"server": "https://vault.example.com",
			"auth": map[string]any{
				"token": map[string]any{
					"tokenSecretRef": map[string]any{
						"name": "vault-token",
						"key":  "token",
					},
				},
			},
		},
	}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/secret-store/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	// Route factory bakes in StoreScopeNamespaced — mirrors routes.go.
	h.HandlePreview(func() WizardInput {
		return &SecretStoreInput{Scope: StoreScopeNamespaced}
	})(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (valid Vault input), got %d: %s", rr.Code, rr.Body.String())
	}

	respBody := rr.Body.String()
	if !strings.Contains(respBody, "kind: SecretStore") {
		t.Errorf("expected YAML to contain SecretStore kind (scope baked from factory); got %s", respBody)
	}
	if strings.Contains(respBody, "ClusterSecretStore") {
		t.Errorf("scope must be Namespaced (factory wins over body), got ClusterSecretStore in response")
	}
}

// TestHandleSecretStorePreview_NoValidatorRegistered verifies the dispatcher
// fall-through path for a recognized provider with no registered validator —
// the contract that U18 introduced so the wizard surface is honest about
// what U19 has yet to ship. Uses a synthetic test-only provider key.
func TestHandleSecretStorePreview_NoValidatorRegistered(t *testing.T) {
	const syntheticKey SecretStoreProvider = "test-only-fall-through"
	validSecretStoreProviders[syntheticKey] = true
	t.Cleanup(func() { delete(validSecretStoreProviders, syntheticKey) })

	h := testHandler()
	input := map[string]any{
		"name":         "my-store",
		"namespace":    "apps",
		"provider":     string(syntheticKey),
		"providerSpec": map[string]any{"any": "value"},
	}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/secret-store/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	h.HandlePreview(func() WizardInput {
		return &SecretStoreInput{Scope: StoreScopeNamespaced}
	})(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 (no validator registered), got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not yet implemented") {
		t.Errorf("expected fall-through message to contain 'not yet implemented'; got %s", rr.Body.String())
	}
}

func TestHandleSecretStorePreview_ClusterScope(t *testing.T) {
	h := testHandler()
	input := map[string]any{
		"name":        "shared-store",
		"namespace":   "should-be-ignored", // cluster scope — must be rejected now
		"provider":    "vault",
		"providerSpec": map[string]any{"server": "https://vault.example.com"},
	}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wizards/cluster-secret-store/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = addAuthContext(req)

	rr := httptest.NewRecorder()
	// Route factory bakes in StoreScopeCluster — mirrors routes.go.
	h.HandlePreview(func() WizardInput {
		return &SecretStoreInput{Scope: StoreScopeCluster}
	})(rr, req)

	// Namespace is set but scope is cluster → validation must reject it (422).
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 (namespace must be empty for cluster scope), got %d: %s", rr.Code, rr.Body.String())
	}

	respBody := rr.Body.String()
	if !strings.Contains(respBody, "namespace") {
		t.Errorf("expected error to reference namespace field; got %s", respBody)
	}
}

// keys is a test-local helper returning the keys of a map[string]any.
func keys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
