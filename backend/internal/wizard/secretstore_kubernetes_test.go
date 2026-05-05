package wizard

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// validKubernetesSpec returns a minimal valid Kubernetes provider spec using
// serviceAccount auth — the most common method for cross-namespace access.
func validKubernetesSpec() map[string]any {
	return map[string]any{
		"remoteNamespace": "secrets-ns",
		"auth": map[string]any{
			"serviceAccount": map[string]any{
				"name": "eso-reader",
			},
		},
	}
}

// --- Top-level field validation ---

func TestValidateKubernetesSpec_Valid(t *testing.T) {
	if errs := validateKubernetesSpec(validKubernetesSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateKubernetesSpec_NoRemoteNamespace(t *testing.T) {
	// remoteNamespace is optional; omitting it is valid.
	spec := validKubernetesSpec()
	delete(spec, "remoteNamespace")
	if errs := validateKubernetesSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors when remoteNamespace omitted, got %v", errs)
	}
}

func TestValidateKubernetesSpec_BlankRemoteNamespace(t *testing.T) {
	spec := validKubernetesSpec()
	spec["remoteNamespace"] = "   "
	if !hasField(validateKubernetesSpec(spec), "remoteNamespace") {
		t.Error("expected remoteNamespace error for whitespace-only value")
	}
}

func TestValidateKubernetesSpec_InvalidRemoteNamespace(t *testing.T) {
	spec := validKubernetesSpec()
	spec["remoteNamespace"] = "Invalid_NS"
	if !hasField(validateKubernetesSpec(spec), "remoteNamespace") {
		t.Error("expected remoteNamespace error for non-DNS-label value")
	}
}

// --- server block validation ---

func TestValidateKubernetesSpec_Server_ValidURL(t *testing.T) {
	spec := validKubernetesSpec()
	spec["server"] = map[string]any{
		"url": "https://apiserver.example.com:6443",
	}
	if errs := validateKubernetesSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with valid server.url, got %v", errs)
	}
}

func TestValidateKubernetesSpec_Server_NonHTTPS(t *testing.T) {
	spec := validKubernetesSpec()
	spec["server"] = map[string]any{"url": "http://apiserver.example.com:6443"}
	if !hasField(validateKubernetesSpec(spec), "server.url") {
		t.Error("expected server.url error for http scheme")
	}
}

func TestValidateKubernetesSpec_Server_NoHost(t *testing.T) {
	spec := validKubernetesSpec()
	spec["server"] = map[string]any{"url": "https://"}
	if !hasField(validateKubernetesSpec(spec), "server.url") {
		t.Error("expected server.url error for empty host")
	}
}

func TestValidateKubernetesSpec_Server_BlankURL(t *testing.T) {
	spec := validKubernetesSpec()
	spec["server"] = map[string]any{"url": ""}
	if !hasField(validateKubernetesSpec(spec), "server.url") {
		t.Error("expected server.url error for blank when set")
	}
}

func TestValidateKubernetesSpec_Server_CABundle(t *testing.T) {
	spec := validKubernetesSpec()
	spec["server"] = map[string]any{
		"url":      "https://apiserver.example.com:6443",
		"caBundle": "LS0tLS1CRUdJTi==",
	}
	if errs := validateKubernetesSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with caBundle set, got %v", errs)
	}
}

func TestValidateKubernetesSpec_Server_BlankCABundle(t *testing.T) {
	spec := validKubernetesSpec()
	spec["server"] = map[string]any{"caBundle": "   "}
	if !hasField(validateKubernetesSpec(spec), "server.caBundle") {
		t.Error("expected server.caBundle error for blank when set")
	}
}

func TestValidateKubernetesSpec_Server_NotObject(t *testing.T) {
	spec := validKubernetesSpec()
	spec["server"] = "https://apiserver.example.com"
	if !hasField(validateKubernetesSpec(spec), "server") {
		t.Error("expected server error when not an object")
	}
}

// --- auth block validation ---

func TestValidateKubernetesSpec_NoAuth(t *testing.T) {
	spec := validKubernetesSpec()
	delete(spec, "auth")
	if !hasField(validateKubernetesSpec(spec), "auth") {
		t.Error("expected auth required error")
	}
}

func TestValidateKubernetesSpec_AuthNoMethod(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{}
	if !hasField(validateKubernetesSpec(spec), "auth") {
		t.Error("expected auth error for empty block")
	}
}

func TestValidateKubernetesSpec_AuthMultipleMethods(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"serviceAccount": map[string]any{"name": "sa"},
		"token":          map[string]any{},
	}
	errs := validateKubernetesSpec(spec)
	if !hasField(errs, "auth") {
		t.Errorf("expected auth error for multiple methods; got %v", errs)
	}
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

// --- ServiceAccount auth ---

func TestValidateKubernetesSpec_ServiceAccountAuth_Valid(t *testing.T) {
	if errs := validateKubernetesSpec(validKubernetesSpec()); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateKubernetesSpec_ServiceAccountAuth_MissingName(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"serviceAccount": map[string]any{},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.serviceAccount.name") {
		t.Error("expected serviceAccount.name required error")
	}
}

func TestValidateKubernetesSpec_ServiceAccountAuth_BadName(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"serviceAccount": map[string]any{"name": "Bad_SA"},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.serviceAccount.name") {
		t.Error("expected serviceAccount.name DNS-label error")
	}
}

func TestValidateKubernetesSpec_ServiceAccountAuth_WithAudiences(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"serviceAccount": map[string]any{
			"name":      "eso-reader",
			"audiences": []any{"https://kubernetes.default.svc"},
		},
	}
	if errs := validateKubernetesSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors with audiences, got %v", errs)
	}
}

func TestValidateKubernetesSpec_ServiceAccountAuth_EmptyAudiences(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"serviceAccount": map[string]any{
			"name":      "eso-reader",
			"audiences": []any{},
		},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.serviceAccount.audiences") {
		t.Error("expected audiences error for empty list when set")
	}
}

func TestValidateKubernetesSpec_ServiceAccountAuth_NilBlock(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{"serviceAccount": nil}
	if !hasField(validateKubernetesSpec(spec), "auth.serviceAccount") {
		t.Error("expected serviceAccount required error when nil")
	}
}

// --- Token auth ---

func TestValidateKubernetesSpec_TokenAuth_Valid(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"token": map[string]any{
			"bearerToken": map[string]any{
				"name": "my-token-secret",
				"key":  "token",
			},
		},
	}
	if errs := validateKubernetesSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateKubernetesSpec_TokenAuth_MissingBearerToken(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{"token": map[string]any{}}
	if !hasField(validateKubernetesSpec(spec), "auth.token.bearerToken") {
		t.Error("expected bearerToken required error")
	}
}

func TestValidateKubernetesSpec_TokenAuth_MissingName(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"token": map[string]any{
			"bearerToken": map[string]any{"key": "token"},
		},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.token.bearerToken.name") {
		t.Error("expected bearerToken.name required error")
	}
}

func TestValidateKubernetesSpec_TokenAuth_MissingKey(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"token": map[string]any{
			"bearerToken": map[string]any{"name": "my-token-secret"},
		},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.token.bearerToken.key") {
		t.Error("expected bearerToken.key required error")
	}
}

func TestValidateKubernetesSpec_TokenAuth_BadSecretName(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"token": map[string]any{
			"bearerToken": map[string]any{"name": "BadSecret", "key": "token"},
		},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.token.bearerToken.name") {
		t.Error("expected bearerToken.name DNS-label error")
	}
}

func TestValidateKubernetesSpec_TokenAuth_NilBlock(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{"token": nil}
	if !hasField(validateKubernetesSpec(spec), "auth.token") {
		t.Error("expected token required error when nil")
	}
}

// --- Cert auth ---

func TestValidateKubernetesSpec_CertAuth_Valid(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"cert": map[string]any{
			"clientCert": map[string]any{"name": "tls-cert", "key": "tls.crt"},
			"clientKey":  map[string]any{"name": "tls-cert", "key": "tls.key"},
		},
	}
	if errs := validateKubernetesSpec(spec); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateKubernetesSpec_CertAuth_MissingClientCert(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"cert": map[string]any{
			"clientKey": map[string]any{"name": "tls-cert", "key": "tls.key"},
		},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.cert.clientCert") {
		t.Error("expected clientCert required error")
	}
}

func TestValidateKubernetesSpec_CertAuth_MissingClientKey(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"cert": map[string]any{
			"clientCert": map[string]any{"name": "tls-cert", "key": "tls.crt"},
		},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.cert.clientKey") {
		t.Error("expected clientKey required error")
	}
}

func TestValidateKubernetesSpec_CertAuth_NilBlock(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{"cert": nil}
	if !hasField(validateKubernetesSpec(spec), "auth.cert") {
		t.Error("expected cert required error when nil")
	}
}

func TestValidateKubernetesSpec_CertAuth_BadSecretName(t *testing.T) {
	spec := validKubernetesSpec()
	spec["auth"] = map[string]any{
		"cert": map[string]any{
			"clientCert": map[string]any{"name": "BadSecret", "key": "tls.crt"},
			"clientKey":  map[string]any{"name": "tls-cert", "key": "tls.key"},
		},
	}
	if !hasField(validateKubernetesSpec(spec), "auth.cert.clientCert.name") {
		t.Error("expected clientCert.name DNS-label error for invalid secret name")
	}
}

// --- Dispatcher integration ---

// TestSecretStoreInput_KubernetesIntegration confirms validateKubernetesSpec is
// wired to the dispatcher via the init() RegisterSecretStoreProvider call.
func TestSecretStoreInput_KubernetesIntegration(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "k8s-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderKubernetes,
		ProviderSpec: validKubernetesSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors via dispatcher, got %v", errs)
	}
}

func TestSecretStoreInput_KubernetesIntegration_ClusterScope(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeCluster,
		Name:         "shared-k8s-store",
		Provider:     SecretStoreProviderKubernetes,
		ProviderSpec: validKubernetesSpec(),
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors for cluster scope, got %v", errs)
	}
}

func TestSecretStoreInput_KubernetesIntegration_PropagatesProviderError(t *testing.T) {
	spec := validKubernetesSpec()
	delete(spec, "auth")
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "k8s-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderKubernetes,
		ProviderSpec: spec,
	}
	errs := s.Validate()
	if !hasField(errs, "auth") {
		t.Errorf("expected provider-level auth error, got %v", errs)
	}
}

// TestSecretStoreInput_KubernetesIntegration_ToYAML asserts the emitted YAML
// places the spec under spec.provider.kubernetes and that auth.serviceAccount
// is correctly nested inside it.
func TestSecretStoreInput_KubernetesIntegration_ToYAML(t *testing.T) {
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "k8s-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderKubernetes,
		ProviderSpec: validKubernetesSpec(),
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

	// Structural: walk spec.provider.kubernetes.auth.serviceAccount.name.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	spec, _ := doc["spec"].(map[string]any)
	provider, _ := spec["provider"].(map[string]any)
	k8sSpec, _ := provider["kubernetes"].(map[string]any)
	if k8sSpec == nil {
		t.Fatalf("expected spec.provider.kubernetes, got provider keys: %v", keys(provider))
	}
	auth, _ := k8sSpec["auth"].(map[string]any)
	sa, _ := auth["serviceAccount"].(map[string]any)
	if sa == nil {
		t.Fatalf("expected auth.serviceAccount, got auth keys: %v", keys(auth))
	}
	if sa["name"] == nil {
		t.Errorf("expected auth.serviceAccount.name to be present; got %v", sa)
	}
}

func TestSecretStoreInput_KubernetesIntegration_ToYAML_TokenAuth(t *testing.T) {
	spec := map[string]any{
		"remoteNamespace": "prod",
		"auth": map[string]any{
			"token": map[string]any{
				"bearerToken": map[string]any{
					"name": "k8s-token",
					"key":  "token",
				},
			},
		},
	}
	s := SecretStoreInput{
		Scope:        StoreScopeNamespaced,
		Name:         "k8s-store",
		Namespace:    "apps",
		Provider:     SecretStoreProviderKubernetes,
		ProviderSpec: spec,
	}
	y, err := s.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v\n%s", err, y)
	}
	spc, _ := doc["spec"].(map[string]any)
	prov, _ := spc["provider"].(map[string]any)
	k8s, _ := prov["kubernetes"].(map[string]any)
	auth, _ := k8s["auth"].(map[string]any)
	tok, _ := auth["token"].(map[string]any)
	bt, _ := tok["bearerToken"].(map[string]any)
	if bt == nil {
		t.Fatalf("expected auth.token.bearerToken, got auth: %v", auth)
	}
	if bt["name"] == nil || bt["key"] == nil {
		t.Errorf("expected bearerToken name+key; got %v", bt)
	}
}
