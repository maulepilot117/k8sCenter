package wizard

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// validGCPSMSpec returns a minimal valid GCP Secret Manager provider spec using
// the workloadIdentity auth method — the most common GKE path.
func validGCPSMSpec() map[string]any {
	return map[string]any{
		"projectID": "my-gcp-project",
		"auth": map[string]any{
			"workloadIdentity": map[string]any{
				"serviceAccountRef": map[string]any{
					"name": "eso-gcp-sa",
				},
			},
		},
	}
}

// validGCPSMSpecSecretRef returns a minimal valid spec using the Service
// Account JSON key (secretRef) auth path.
func validGCPSMSpecSecretRef() map[string]any {
	return map[string]any{
		"projectID": "my-gcp-project",
		"auth": map[string]any{
			"secretRef": map[string]any{
				"secretAccessKeySecretRef": map[string]any{
					"name": "gcp-sa-key",
					"key":  "key.json",
				},
			},
		},
	}
}

// --- Top-level field tests ---

func TestValidateGCPSMSpec_Valid_WorkloadIdentity(t *testing.T) {
	if errs := validateGCPSMSpec(validGCPSMSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateGCPSMSpec_Valid_SecretRef(t *testing.T) {
	if errs := validateGCPSMSpec(validGCPSMSpecSecretRef()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateGCPSMSpec_Valid_DefaultCredentials(t *testing.T) {
	// Omitting auth entirely is valid — ESO uses GKE node identity or ADC.
	spec := map[string]any{"projectID": "my-project"}
	if errs := validateGCPSMSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors for default-credentials path, got %v", errs)
	}
}

func TestValidateGCPSMSpec_Valid_WithOptionalLocation(t *testing.T) {
	spec := validGCPSMSpec()
	spec["location"] = "us-central1"
	if errs := validateGCPSMSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with location set, got %v", errs)
	}
}

func TestValidateGCPSMSpec_MissingProjectID(t *testing.T) {
	spec := validGCPSMSpec()
	delete(spec, "projectID")
	if !hasField(validateGCPSMSpec(spec), "projectID") {
		t.Error("expected projectID required error")
	}
}

func TestValidateGCPSMSpec_BlankProjectID(t *testing.T) {
	spec := validGCPSMSpec()
	spec["projectID"] = "   "
	if !hasField(validateGCPSMSpec(spec), "projectID") {
		t.Error("expected projectID error for whitespace-only value")
	}
}

func TestValidateGCPSMSpec_EmptyLocationRejected(t *testing.T) {
	spec := validGCPSMSpec()
	spec["location"] = ""
	if !hasField(validateGCPSMSpec(spec), "location") {
		t.Error("expected location error for empty when set")
	}
}

// --- Auth method picker tests ---

func TestValidateGCPSMSpec_AuthMultipleMethods(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{
			"serviceAccountRef": map[string]any{"name": "sa"},
		},
		"secretRef": map[string]any{
			"secretAccessKeySecretRef": map[string]any{"name": "s", "key": "k"},
		},
	}
	errs := validateGCPSMSpec(spec)
	if !hasField(errs, "auth") {
		t.Errorf("expected auth error for multiple methods; got %v", errs)
	}
	// Error message should name both methods.
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

// --- Workload Identity auth tests ---

func TestValidateGCPSMSpec_WorkloadIdentity_WithAllOptionals(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{
			"serviceAccountRef": map[string]any{"name": "eso-sa"},
			"clusterLocation":   "us-central1",
			"clusterName":       "my-cluster",
			"clusterProjectID":  "my-project",
		},
	}
	if errs := validateGCPSMSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with all optional WI fields set; got %v", errs)
	}
}

func TestValidateGCPSMSpec_WorkloadIdentity_MissingServiceAccountRef(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.workloadIdentity.serviceAccountRef") {
		t.Error("expected serviceAccountRef required error")
	}
}

func TestValidateGCPSMSpec_WorkloadIdentity_BlankServiceAccountName(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{
			"serviceAccountRef": map[string]any{"name": ""},
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.workloadIdentity.serviceAccountRef.name") {
		t.Error("expected serviceAccountRef.name required error")
	}
}

func TestValidateGCPSMSpec_WorkloadIdentity_InvalidServiceAccountName(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{
			"serviceAccountRef": map[string]any{"name": "InvalidName_123"},
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.workloadIdentity.serviceAccountRef.name") {
		t.Error("expected DNS label error for invalid service account name")
	}
}

func TestValidateGCPSMSpec_WorkloadIdentity_EmptyClusterLocation(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{
			"serviceAccountRef": map[string]any{"name": "sa"},
			"clusterLocation":   "",
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.workloadIdentity.clusterLocation") {
		t.Error("expected clusterLocation error for empty-when-set")
	}
}

func TestValidateGCPSMSpec_WorkloadIdentity_EmptyClusterName(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{
			"serviceAccountRef": map[string]any{"name": "sa"},
			"clusterName":       "",
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.workloadIdentity.clusterName") {
		t.Error("expected clusterName error for empty-when-set")
	}
}

func TestValidateGCPSMSpec_WorkloadIdentity_EmptyClusterProjectID(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"workloadIdentity": map[string]any{
			"serviceAccountRef": map[string]any{"name": "sa"},
			"clusterProjectID":  "",
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.workloadIdentity.clusterProjectID") {
		t.Error("expected clusterProjectID error for empty-when-set")
	}
}

// --- Service Account Key (secretRef) auth tests ---

func TestValidateGCPSMSpec_SecretRef_MissingSecretAccessKeyRef(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.secretRef.secretAccessKeySecretRef") {
		t.Error("expected secretAccessKeySecretRef required error")
	}
}

func TestValidateGCPSMSpec_SecretRef_MissingName(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"secretAccessKeySecretRef": map[string]any{"key": "key.json"},
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.secretRef.secretAccessKeySecretRef.name") {
		t.Error("expected secretAccessKeySecretRef.name required error")
	}
}

func TestValidateGCPSMSpec_SecretRef_InvalidName(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"secretAccessKeySecretRef": map[string]any{"name": "InvalidName", "key": "k"},
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.secretRef.secretAccessKeySecretRef.name") {
		t.Error("expected DNS label error for invalid secret name")
	}
}

func TestValidateGCPSMSpec_SecretRef_MissingKey(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"secretAccessKeySecretRef": map[string]any{"name": "gcp-sa-key"},
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.secretRef.secretAccessKeySecretRef.key") {
		t.Error("expected secretAccessKeySecretRef.key required error")
	}
}

func TestValidateGCPSMSpec_SecretRef_BlankKey(t *testing.T) {
	spec := validGCPSMSpec()
	spec["auth"] = map[string]any{
		"secretRef": map[string]any{
			"secretAccessKeySecretRef": map[string]any{"name": "gcp-sa-key", "key": "  "},
		},
	}
	if !hasField(validateGCPSMSpec(spec), "auth.secretRef.secretAccessKeySecretRef.key") {
		t.Error("expected key error for whitespace-only value")
	}
}

// --- Dispatcher integration tests ---

// TestSecretStoreInput_GCPSMIntegration confirms validateGCPSMSpec is wired to
// the dispatcher via the init() RegisterSecretStoreProvider call.
func TestSecretStoreInput_GCPSMIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "gcpsm-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderGCP,
		ProviderSpec: validGCPSMSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_GCPSMIntegration_SecretRef(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeCluster,
		Name:         "gcpsm-store",
		Provider:     SecretStoreProviderGCP,
		ProviderSpec: validGCPSMSpecSecretRef(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher (secretRef), got %v", errs)
	}
}

func TestSecretStoreInput_GCPSMIntegration_DefaultCredentials(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "gcpsm-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderGCP,
		ProviderSpec: map[string]any{"projectID": "my-project"},
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors for default-credentials path via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_GCPSMIntegration_PropagatesProviderError(t *testing.T) {
	spec := validGCPSMSpec()
	delete(spec, "projectID")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "gcpsm-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderGCP,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "projectID") {
		t.Errorf("expected provider-level projectID error, got %v", errs)
	}
}

// TestSecretStoreInput_GCPSMIntegration_ToYAML asserts the wizard preview's
// emitted YAML places the spec under spec.provider.gcpsm and the projectID
// and auth sub-object are present at the right paths.
func TestSecretStoreInput_GCPSMIntegration_ToYAML(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "gcpsm-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderGCP,
		ProviderSpec: validGCPSMSpec(),
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

	// Walk spec.provider.gcpsm and verify required shape.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	spec, _ := doc["spec"].(map[string]any)
	provider, _ := spec["provider"].(map[string]any)
	gcpsmSpec, _ := provider["gcpsm"].(map[string]any)
	if gcpsmSpec == nil {
		t.Fatalf("expected spec.provider.gcpsm, got provider keys: %v", keys(provider))
	}
	if gcpsmSpec["projectID"] == nil {
		t.Errorf("expected spec.provider.gcpsm.projectID to be present; got %v", gcpsmSpec)
	}
	auth, _ := gcpsmSpec["auth"].(map[string]any)
	wi, _ := auth["workloadIdentity"].(map[string]any)
	saRef, _ := wi["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		t.Fatalf("expected spec.provider.gcpsm.auth.workloadIdentity.serviceAccountRef; auth=%v", auth)
	}
	if saRef["name"] == nil {
		t.Errorf("expected serviceAccountRef.name; got %v", saRef)
	}
}
