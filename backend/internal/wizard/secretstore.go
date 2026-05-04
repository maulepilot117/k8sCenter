package wizard

import (
	"fmt"
	"sync"

	sigsyaml "sigs.k8s.io/yaml"
)

// StoreScope indicates whether the wizard produces a namespaced SecretStore or
// a cluster-scoped ClusterSecretStore. Not JSON-decoded — the HTTP route is
// authoritative and bakes in the scope via the HandlePreview factory (mirrors
// IssuerScope).
type StoreScope string

const (
	StoreScopeNamespaced StoreScope = "namespaced"
	StoreScopeCluster    StoreScope = "cluster"
)

// SecretStoreProvider is the canonical key under spec.provider for an ESO
// SecretStore. Per-provider validators register themselves against this key so
// the wizard core stays provider-agnostic. Phase H Unit 19 lands the actual
// validators; Unit 18 ships the registry empty so the dispatcher fall-through
// path is exercised by tests.
type SecretStoreProvider string

const (
	SecretStoreProviderVault       SecretStoreProvider = "vault"
	SecretStoreProviderAWS         SecretStoreProvider = "aws"
	SecretStoreProviderAWSPS       SecretStoreProvider = "awsps" // AWS Parameter Store (synthetic key — ESO uses spec.provider.aws with serviceType discriminator; we expose it as a separate selector for UX clarity)
	SecretStoreProviderAzure       SecretStoreProvider = "azurekv"
	SecretStoreProviderGCP         SecretStoreProvider = "gcpsm"
	SecretStoreProviderKubernetes  SecretStoreProvider = "kubernetes"
	SecretStoreProviderAkeyless    SecretStoreProvider = "akeyless"
	SecretStoreProviderDoppler     SecretStoreProvider = "doppler"
	SecretStoreProviderOnePassword SecretStoreProvider = "onepasswordsdk"
	SecretStoreProviderBitwarden   SecretStoreProvider = "bitwardensecretsmanager"
	SecretStoreProviderConjur      SecretStoreProvider = "conjur"
	SecretStoreProviderInfisical   SecretStoreProvider = "infisical"
)

// SecretStoreInput is the wizard form data for creating an
// external-secrets.io/v1 SecretStore or ClusterSecretStore. The Scope field
// is authoritative; it is set by the route's HandlePreview factory rather
// than decoded from the request body. Provider names the source-store
// family; ProviderSpec carries the spec.provider.<provider> sub-object
// verbatim so the wizard never holds source-store credentials in typed form
// (mirrors the SecretStore observatory normalization).
type SecretStoreInput struct {
	Scope        StoreScope          `json:"-"`
	Name         string              `json:"name"`
	Namespace    string              `json:"namespace,omitempty"` // ignored for cluster scope
	Provider     SecretStoreProvider `json:"provider"`
	ProviderSpec map[string]any      `json:"providerSpec,omitempty"`

	// RetrySettings + RefreshInterval are top-level on SecretStore.spec; they
	// apply uniformly across providers. Optional — emitted only when set.
	RefreshInterval string `json:"refreshInterval,omitempty"`
}

// providerValidator validates a SecretStoreInput's ProviderSpec block. Each
// provider in Phase H Unit 19 implements one of these and registers itself
// in init().
type providerValidator func(spec map[string]any) []FieldError

var (
	providerValidatorsMu sync.RWMutex
	providerValidators   = map[SecretStoreProvider]providerValidator{}
)

// RegisterSecretStoreProvider wires a validator for a provider key. Called
// from per-provider init() functions; safe under concurrent registration.
// Re-registering a provider replaces the prior validator — useful for tests
// that swap in stubs.
func RegisterSecretStoreProvider(p SecretStoreProvider, v providerValidator) {
	providerValidatorsMu.Lock()
	defer providerValidatorsMu.Unlock()
	providerValidators[p] = v
}

// lookupProviderValidator returns the registered validator (if any) for a
// provider. Exposed for testing the dispatcher fall-through.
func lookupProviderValidator(p SecretStoreProvider) (providerValidator, bool) {
	providerValidatorsMu.RLock()
	defer providerValidatorsMu.RUnlock()
	v, ok := providerValidators[p]
	return v, ok
}

// validSecretStoreProviders enumerates the 12 provider keys the wizard
// recognizes. Used to reject typos at the input layer before dispatcher
// lookup. Niche providers (Pulumi ESC, Passbolt, Keeper, Onboardbase,
// Oracle Cloud Vault, Alibaba KMS, custom webhook) ship as YAML templates
// only (Phase H Unit 20) and are not in this set.
var validSecretStoreProviders = map[SecretStoreProvider]bool{
	SecretStoreProviderVault:       true,
	SecretStoreProviderAWS:         true,
	SecretStoreProviderAWSPS:       true,
	SecretStoreProviderAzure:       true,
	SecretStoreProviderGCP:         true,
	SecretStoreProviderKubernetes:  true,
	SecretStoreProviderAkeyless:    true,
	SecretStoreProviderDoppler:     true,
	SecretStoreProviderOnePassword: true,
	SecretStoreProviderBitwarden:   true,
	SecretStoreProviderConjur:      true,
	SecretStoreProviderInfisical:   true,
}

// Validate checks the SecretStoreInput and returns field-level errors.
// Per-provider field validation is delegated to the registered provider
// validator. When no validator is registered for the named provider, the
// dispatcher falls through to a single warning error so the wizard
// surface still rejects unimplemented providers cleanly rather than
// silently emitting a half-formed YAML.
func (s *SecretStoreInput) Validate() []FieldError {
	var errs []FieldError

	if s.Scope != StoreScopeNamespaced && s.Scope != StoreScopeCluster {
		errs = append(errs, FieldError{Field: "scope", Message: "must be namespaced or cluster"})
	}

	if !dnsLabelRegex.MatchString(s.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if s.Scope == StoreScopeNamespaced {
		if s.Namespace == "" {
			errs = append(errs, FieldError{Field: "namespace", Message: "is required for namespaced SecretStore"})
		} else if !dnsLabelRegex.MatchString(s.Namespace) {
			errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
		}
	}

	if s.Provider == "" {
		errs = append(errs, FieldError{Field: "provider", Message: "is required"})
		return errs
	}
	if !validSecretStoreProviders[s.Provider] {
		errs = append(errs, FieldError{Field: "provider", Message: fmt.Sprintf("unknown provider %q (use a YAML template via the editor for niche providers)", s.Provider)})
		return errs
	}

	if s.ProviderSpec == nil {
		errs = append(errs, FieldError{Field: "providerSpec", Message: "is required"})
		return errs
	}

	if v, ok := lookupProviderValidator(s.Provider); ok {
		errs = append(errs, v(s.ProviderSpec)...)
	} else {
		// Phase H Unit 18 ships the registry empty; per-provider validators
		// land in Unit 19. Until then every recognized provider key falls
		// through here so the wizard surface is honest about what's not
		// yet implemented.
		errs = append(errs, FieldError{
			Field:   "provider",
			Message: fmt.Sprintf("provider %q wizard not yet implemented — use the YAML editor", s.Provider),
		})
	}

	return errs
}

// ToSecretStore returns a map representation suitable for YAML marshaling.
// kind is SecretStore or ClusterSecretStore based on Scope.
func (s *SecretStoreInput) ToSecretStore() map[string]any {
	kind := "SecretStore"
	if s.Scope == StoreScopeCluster {
		kind = "ClusterSecretStore"
	}

	metadata := map[string]any{
		"name": s.Name,
	}
	if s.Scope == StoreScopeNamespaced {
		metadata["namespace"] = s.Namespace
	}

	spec := map[string]any{
		"provider": map[string]any{
			string(s.Provider): s.ProviderSpec,
		},
	}
	if s.RefreshInterval != "" {
		spec["refreshInterval"] = s.RefreshInterval
	}

	return map[string]any{
		"apiVersion": "external-secrets.io/v1",
		"kind":       kind,
		"metadata":   metadata,
		"spec":       spec,
	}
}

// ToYAML implements WizardInput.
func (s *SecretStoreInput) ToYAML() (string, error) {
	y, err := sigsyaml.Marshal(s.ToSecretStore())
	if err != nil {
		return "", err
	}
	return string(y), nil
}
