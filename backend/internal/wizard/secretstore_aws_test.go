package wizard

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// validAWSSpec returns a minimal valid AWS Secrets Manager provider spec
// using IAM workload-identity (jwt) auth — the most common in-cluster pattern.
// Other auth-method tests override the auth block as needed.
func validAWSSpec() map[string]any {
	return map[string]any{
		"region": "us-east-1",
		"auth": map[string]any{
			"jwt": map[string]any{
				"serviceAccountRef": map[string]any{
					"name": "my-service-account",
				},
			},
		},
	}
}

// validAWSSpecStaticCreds returns a minimal valid spec using static credentials.
func validAWSSpecStaticCreds() map[string]any {
	return map[string]any{
		"region": "eu-west-1",
		"auth": map[string]any{
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
		},
	}
}

// --- Top-level field tests ---

func TestValidateAWSSpec_Valid_JWT(t *testing.T) {
	if errs := validateAWSSpec(validAWSSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateAWSSpec_Valid_StaticCreds(t *testing.T) {
	if errs := validateAWSSpec(validAWSSpecStaticCreds()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateAWSSpec_Valid_WithOptionalRole(t *testing.T) {
	spec := validAWSSpec()
	spec["role"] = "arn:aws:iam::123456789012:role/my-role"
	if errs := validateAWSSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with role set, got %v", errs)
	}
}

func TestValidateAWSSpec_MissingRegion(t *testing.T) {
	spec := validAWSSpec()
	delete(spec, "region")
	if !hasField(validateAWSSpec(spec), "region") {
		t.Error("expected region required error")
	}
}

func TestValidateAWSSpec_BlankRegion(t *testing.T) {
	spec := validAWSSpec()
	spec["region"] = "   "
	if !hasField(validateAWSSpec(spec), "region") {
		t.Error("expected region error for whitespace-only value")
	}
}

func TestValidateAWSSpec_RoleEmptyWhenSet(t *testing.T) {
	spec := validAWSSpec()
	spec["role"] = "   "
	if !hasField(validateAWSSpec(spec), "role") {
		t.Error("expected role error for whitespace-only value when set")
	}
}

func TestValidateAWSSpec_RoleAbsent_NoError(t *testing.T) {
	// role is optional — absence must not produce an error.
	spec := validAWSSpec()
	delete(spec, "role")
	if errs := validateAWSSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors when role absent, got %v", errs)
	}
}

// --- Auth block tests ---

func TestValidateAWSSpec_NoAuth(t *testing.T) {
	spec := validAWSSpec()
	delete(spec, "auth")
	if !hasField(validateAWSSpec(spec), "auth") {
		t.Error("expected auth required error")
	}
}

func TestValidateAWSSpec_AuthNoMethod(t *testing.T) {
	spec := validAWSSpec()
	spec["auth"] = map[string]any{}
	if !hasField(validateAWSSpec(spec), "auth") {
		t.Error("expected auth error for empty block")
	}
}

func TestValidateAWSSpec_AuthMultipleMethods(t *testing.T) {
	spec := validAWSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{"name": "sa"},
		},
		"secretRef": map[string]any{
			"accessKeyIDSecretRef":     map[string]any{"name": "s", "key": "k"},
			"secretAccessKeySecretRef": map[string]any{"name": "s", "key": "k"},
		},
	}
	errs := validateAWSSpec(spec)
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

// --- JWT auth tests ---

func TestValidateAWSSpec_JWTAuth_Valid(t *testing.T) {
	spec := validAWSSpec() // already uses jwt
	if errs := validateAWSSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors for jwt auth, got %v", errs)
	}
}

func TestValidateAWSSpec_JWTAuth_MissingJWTBlock(t *testing.T) {
	spec := validAWSSpec()
	spec["auth"] = map[string]any{"jwt": nil}
	if !hasField(validateAWSSpec(spec), "auth.jwt") {
		t.Error("expected auth.jwt required error when block is nil")
	}
}

func TestValidateAWSSpec_JWTAuth_MissingServiceAccountRef(t *testing.T) {
	spec := validAWSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{},
	}
	if !hasField(validateAWSSpec(spec), "auth.jwt.serviceAccountRef") {
		t.Error("expected auth.jwt.serviceAccountRef required error")
	}
}

func TestValidateAWSSpec_JWTAuth_MissingServiceAccountRefName(t *testing.T) {
	spec := validAWSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.jwt.serviceAccountRef.name") {
		t.Error("expected auth.jwt.serviceAccountRef.name required error")
	}
}

func TestValidateAWSSpec_JWTAuth_BlankServiceAccountRefName(t *testing.T) {
	spec := validAWSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{"name": "   "},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.jwt.serviceAccountRef.name") {
		t.Error("expected auth.jwt.serviceAccountRef.name error for whitespace")
	}
}

func TestValidateAWSSpec_JWTAuth_BadServiceAccountRefName(t *testing.T) {
	spec := validAWSSpec()
	spec["auth"] = map[string]any{
		"jwt": map[string]any{
			"serviceAccountRef": map[string]any{"name": "Bad_Name"},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.jwt.serviceAccountRef.name") {
		t.Error("expected auth.jwt.serviceAccountRef.name DNS error")
	}
}

// --- Static credentials (secretRef) auth tests ---

func TestValidateAWSSpec_SecretRefAuth_Valid(t *testing.T) {
	if errs := validateAWSSpec(validAWSSpecStaticCreds()); len(errs) != 0 {
		t.Errorf("expected no errors for secretRef auth, got %v", errs)
	}
}

func TestValidateAWSSpec_SecretRefAuth_MissingSecretRefBlock(t *testing.T) {
	spec := validAWSSpecStaticCreds()
	spec["auth"] = map[string]any{"secretRef": nil}
	if !hasField(validateAWSSpec(spec), "auth.secretRef") {
		t.Error("expected auth.secretRef required error when block is nil")
	}
}

func TestValidateAWSSpec_SecretRefAuth_MissingAccessKeyIDRef(t *testing.T) {
	spec := validAWSSpecStaticCreds()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"secretAccessKeySecretRef": map[string]any{"name": "aws-creds", "key": "sak"},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.secretRef.accessKeyIDSecretRef") {
		t.Error("expected auth.secretRef.accessKeyIDSecretRef required error")
	}
}

func TestValidateAWSSpec_SecretRefAuth_MissingSecretAccessKeyRef(t *testing.T) {
	spec := validAWSSpecStaticCreds()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef": map[string]any{"name": "aws-creds", "key": "akid"},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.secretRef.secretAccessKeySecretRef") {
		t.Error("expected auth.secretRef.secretAccessKeySecretRef required error")
	}
}

func TestValidateAWSSpec_SecretRefAuth_AccessKeyIDRefMissingName(t *testing.T) {
	spec := validAWSSpecStaticCreds()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef":     map[string]any{"key": "akid"}, // missing name
			"secretAccessKeySecretRef": map[string]any{"name": "aws-creds", "key": "sak"},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.secretRef.accessKeyIDSecretRef.name") {
		t.Error("expected auth.secretRef.accessKeyIDSecretRef.name required error")
	}
}

func TestValidateAWSSpec_SecretRefAuth_AccessKeyIDRefMissingKey(t *testing.T) {
	spec := validAWSSpecStaticCreds()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef":     map[string]any{"name": "aws-creds"}, // missing key
			"secretAccessKeySecretRef": map[string]any{"name": "aws-creds", "key": "sak"},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.secretRef.accessKeyIDSecretRef.key") {
		t.Error("expected auth.secretRef.accessKeyIDSecretRef.key required error")
	}
}

func TestValidateAWSSpec_SecretRefAuth_BadAccessKeyIDRefName(t *testing.T) {
	spec := validAWSSpecStaticCreds()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"accessKeyIDSecretRef":     map[string]any{"name": "BadName", "key": "akid"},
			"secretAccessKeySecretRef": map[string]any{"name": "aws-creds", "key": "sak"},
		},
	}
	if !hasField(validateAWSSpec(spec), "auth.secretRef.accessKeyIDSecretRef.name") {
		t.Error("expected auth.secretRef.accessKeyIDSecretRef.name DNS error")
	}
}

// --- Dispatcher integration tests ---

// TestSecretStoreInput_AWSIntegration confirms validateAWSSpec is wired to the
// dispatcher via the init() RegisterSecretStoreProvider call.
func TestSecretStoreInput_AWSIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "aws-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAWS,
		ProviderSpec: validAWSSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_AWSIntegration_PropagatesProviderError(t *testing.T) {
	spec := validAWSSpec()
	delete(spec, "region")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "aws-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAWS,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "region") {
		t.Errorf("expected provider-level region error, got %v", errs)
	}
}

// TestSecretStoreInput_AWSIntegration_ToYAML asserts the wizard preview's
// emitted YAML places the spec under spec.provider.aws and does not leak the
// wizard's transport keys at the wrong nesting level.
func TestSecretStoreInput_AWSIntegration_ToYAML(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "aws-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderAWS,
		ProviderSpec: validAWSSpec(),
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

	// Structural assertion: spec.provider.aws.auth.jwt.serviceAccountRef.name.
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
	if awsSpec["region"] != "us-east-1" {
		t.Errorf("expected region=us-east-1, got %v", awsSpec["region"])
	}
	auth, _ := awsSpec["auth"].(map[string]any)
	jwt, _ := auth["jwt"].(map[string]any)
	saRef, _ := jwt["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		t.Fatalf("expected spec.provider.aws.auth.jwt.serviceAccountRef to be non-nil; auth=%v", auth)
	}
	if saRef["name"] == nil {
		t.Errorf("expected serviceAccountRef.name to be present; got %v", saRef)
	}
}
