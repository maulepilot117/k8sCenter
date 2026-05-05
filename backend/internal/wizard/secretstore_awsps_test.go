package wizard

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// validAWSPSSpec returns a minimal valid AWS Parameter Store provider spec
// using IAM workload identity (jwt) auth — the simplest auth method to
// construct in tests. Static-credentials tests override the auth block.
func validAWSPSSpec() map[string]any {
	return map[string]any{
		"region": "us-east-1",
		"auth": map[string]any{
			"jwt": map[string]any{
				"serviceAccountRef": map[string]any{
					"name": "my-service-account",
				},
				"role": "arn:aws:iam::123456789012:role/my-role",
			},
		},
	}
}

func TestValidateAWSPSSpec_Valid(t *testing.T) {
	if errs := validateAWSPSSpec(validAWSPSSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

// --- Top-level fields ---

func TestValidateAWSPSSpec_MissingRegion(t *testing.T) {
	spec := validAWSPSSpec()
	delete(spec, "region")
	if !hasField(validateAWSPSSpec(spec), "region") {
		t.Error("expected region required error")
	}
}

func TestValidateAWSPSSpec_BlankRegion(t *testing.T) {
	spec := validAWSPSSpec()
	spec["region"] = "   "
	if !hasField(validateAWSPSSpec(spec), "region") {
		t.Error("expected region error for whitespace-only value")
	}
}

func TestValidateAWSPSSpec_OptionalRole_AcceptsWhenSet(t *testing.T) {
	spec := validAWSPSSpec()
	spec["role"] = "arn:aws:iam::123456789012:role/assume-role"
	if errs := validateAWSPSSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with optional role set; got %v", errs)
	}
}

func TestValidateAWSPSSpec_OptionalRole_EmptyStringRejected(t *testing.T) {
	spec := validAWSPSSpec()
	spec["role"] = "   "
	if !hasField(validateAWSPSSpec(spec), "role") {
		t.Error("expected role error for whitespace-only value when set")
	}
}

// --- Auth method selection ---

func TestValidateAWSPSSpec_NoAuth(t *testing.T) {
	spec := validAWSPSSpec()
	delete(spec, "auth")
	if !hasField(validateAWSPSSpec(spec), "auth") {
		t.Error("expected auth required error")
	}
}

func TestValidateAWSPSSpec_AuthNoMethod(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{}
	if !hasField(validateAWSPSSpec(spec), "auth") {
		t.Error("expected auth error for empty block")
	}
}

func TestValidateAWSPSSpec_AuthMultipleMethods(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{"name": "sa"},
			"role":              "arn:aws:iam::123:role/r",
		},
		"secretRef": map[string]any{},
	}
	errs := validateAWSPSSpec(spec)
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
		t.Errorf("expected only-one error message; got %v", errs)
	}
}

// --- JWT auth (IAM workload identity / IRSA) ---

func TestValidateAWSPSSpec_JWTAuth_Valid(t *testing.T) {
	if errs := validateAWSPSSpec(validAWSPSSpec()); len(errs) != 0 {
		t.Errorf("expected no errors for valid jwt auth; got %v", errs)
	}
}

func TestValidateAWSPSSpec_JWTAuth_MissingServiceAccountRef(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"role": "arn:aws:iam::123:role/r",
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.jwt.serviceAccountRef") {
		t.Error("expected serviceAccountRef required error")
	}
}

func TestValidateAWSPSSpec_JWTAuth_BlankServiceAccountName(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{"name": ""},
			"role":              "arn:aws:iam::123:role/r",
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.jwt.serviceAccountRef.name") {
		t.Error("expected serviceAccountRef.name required error")
	}
}

func TestValidateAWSPSSpec_JWTAuth_BadServiceAccountName(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{"name": "BadName"},
			"role":              "arn:aws:iam::123:role/r",
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.jwt.serviceAccountRef.name") {
		t.Error("expected serviceAccountRef.name DNS error for BadName")
	}
}

func TestValidateAWSPSSpec_JWTAuth_MissingRole(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{"name": "my-sa"},
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.jwt.role") {
		t.Error("expected role required error")
	}
}

// --- Static credentials auth (secretRef) ---

func TestValidateAWSPSSpec_SecretRefAuth_Valid(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef": map[string]any{
				"name": "aws-creds",
				"key":  "access-key-id",
			},
			"secretAccessKeySecretRef": map[string]any{
				"name": "aws-creds",
				"key":  "secret-access-key",
			},
		},
	}
	if errs := validateAWSPSSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors for valid secretRef auth; got %v", errs)
	}
}

func TestValidateAWSPSSpec_SecretRefAuth_MissingAccessKeyRef(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"secretAccessKeySecretRef": map[string]any{
				"name": "aws-creds",
				"key":  "secret-access-key",
			},
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.secretRef.accessKeyIDSecretRef") {
		t.Error("expected accessKeyIDSecretRef required error")
	}
}

func TestValidateAWSPSSpec_SecretRefAuth_MissingSecretAccessKeyRef(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef": map[string]any{
				"name": "aws-creds",
				"key":  "access-key-id",
			},
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.secretRef.secretAccessKeySecretRef") {
		t.Error("expected secretAccessKeySecretRef required error")
	}
}

func TestValidateAWSPSSpec_SecretRefAuth_AccessKeyRef_MissingName(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef": map[string]any{
				"key": "access-key-id",
			},
			"secretAccessKeySecretRef": map[string]any{
				"name": "aws-creds",
				"key":  "secret-access-key",
			},
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.secretRef.accessKeyIDSecretRef.name") {
		t.Error("expected accessKeyIDSecretRef.name required error")
	}
}

func TestValidateAWSPSSpec_SecretRefAuth_AccessKeyRef_MissingKey(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef": map[string]any{
				"name": "aws-creds",
				// key intentionally omitted
			},
			"secretAccessKeySecretRef": map[string]any{
				"name": "aws-creds",
				"key":  "secret-access-key",
			},
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.secretRef.accessKeyIDSecretRef.key") {
		t.Error("expected accessKeyIDSecretRef.key required error")
	}
}

func TestValidateAWSPSSpec_SecretRefAuth_BadRefName(t *testing.T) {
	spec := validAWSPSSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef": map[string]any{
				"name": "Bad_Name",
				"key":  "access-key-id",
			},
			"secretAccessKeySecretRef": map[string]any{
				"name": "aws-creds",
				"key":  "secret-access-key",
			},
		},
	}
	if !hasField(validateAWSPSSpec(spec), "auth.secretRef.accessKeyIDSecretRef.name") {
		t.Error("expected DNS error for bad Secret name")
	}
}

// --- Dispatcher integration ---

// TestSecretStoreInput_AWSPSIntegration confirms validateAWSPSSpec is wired
// to the dispatcher via the init() RegisterSecretStoreProvider call. A
// SecretStoreInput with provider=awsps should route through validateAWSPSSpec
// and surface its errors at the providerSpec level.
func TestSecretStoreInput_AWSPSIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "ps-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAWSPS,
		ProviderSpec: validAWSPSSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_AWSPSIntegration_PropagatesProviderError(t *testing.T) {
	spec := validAWSPSSpec()
	delete(spec, "region")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "ps-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAWSPS,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "region") {
		t.Errorf("expected provider-level region error, got %v", errs)
	}
}

// TestSecretStoreInput_AWSPSIntegration_ToYAML confirms the critical U18
// remap: the synthetic "awsps" provider key must be translated to
// spec.provider.aws with service: ParameterStore injected. This test pins the
// end-to-end contract between the wizard's UX discriminator and the ESO v1
// API shape.
func TestSecretStoreInput_AWSPSIntegration_ToYAML(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "ps-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAWSPS,
		ProviderSpec: validAWSPSSpec(),
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The synthetic "awsps" key must never appear in the emitted YAML.
	if strings.Contains(y, "awsps:") {
		t.Errorf("YAML must not contain synthetic 'awsps' key; got\n%s", y)
	}

	// Top-level kind and apiVersion must be correct.
	for _, want := range []string{
		"apiVersion: external-secrets.io/v1",
		"kind: SecretStore",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected YAML to contain %q\n%s", want, y)
		}
	}

	// Structural walk: spec.provider.aws must be present (not spec.provider.awsps).
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	spec, _ := doc["spec"].(map[string]any)
	provider, _ := spec["provider"].(map[string]any)
	awsSpec, _ := provider["aws"].(map[string]any)
	if awsSpec == nil {
		t.Fatalf("expected spec.provider.aws (U18 remap), got provider keys: %v", keys(provider))
	}

	// service: ParameterStore must be injected by ToSecretStore.
	if awsSpec["service"] != "ParameterStore" {
		t.Errorf("expected service=ParameterStore injected by U18 remap; got %v", awsSpec["service"])
	}

	// region from providerSpec must be preserved verbatim.
	if awsSpec["region"] != "us-east-1" {
		t.Errorf("expected region=us-east-1 preserved; got %v", awsSpec["region"])
	}

	// auth.jwt block must be nested under aws, not leaked to the top level.
	auth, _ := awsSpec["auth"].(map[string]any)
	jwtBlock, _ := auth["jwt"].(map[string]any)
	if jwtBlock == nil {
		t.Fatalf("expected spec.provider.aws.auth.jwt to be non-nil; auth=%v", auth)
	}
	saRef, _ := jwtBlock["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		t.Fatalf("expected serviceAccountRef to be non-nil; jwt=%v", jwtBlock)
	}
	if saRef["name"] == nil {
		t.Errorf("expected serviceAccountRef.name to be present; got %v", saRef)
	}
}

// TestSecretStoreInput_AWSPSIntegration_ToYAML_ClusterScope verifies the
// remap also works for ClusterSecretStore scope.
func TestSecretStoreInput_AWSPSIntegration_ToYAML_ClusterScope(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeCluster,
		Name:         "shared-ps-store",
		Provider:     SecretStoreProviderAWSPS,
		ProviderSpec: validAWSPSSpec(),
	}
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
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	spec, _ := doc["spec"].(map[string]any)
	provider, _ := spec["provider"].(map[string]any)
	awsSpec, _ := provider["aws"].(map[string]any)
	if awsSpec == nil {
		t.Fatalf("expected spec.provider.aws for ClusterSecretStore; got keys: %v", keys(provider))
	}
	if awsSpec["service"] != "ParameterStore" {
		t.Errorf("expected service=ParameterStore for ClusterSecretStore; got %v", awsSpec["service"])
	}
}
