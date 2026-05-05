package wizard

import (
	"strings"
)

// init registers the Doppler provider validator with the SecretStore wizard
// dispatcher. Lives in this file so the validator ships and registers as one
// unit — adding a provider is a single-file edit + one line in
// READY_SECRET_STORE_PROVIDERS on the frontend.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderDoppler, validateDopplerSpec)
}

// validateDopplerSpec validates a SecretStoreInput.ProviderSpec for the Doppler
// provider. The spec mirrors ESO's spec.provider.doppler shape — auth (with
// exactly one of secretRef or oidcConfig), project, and config.
//
// Auth methods:
//   - secretRef: a DopplerToken SecretRef pointing to a Kubernetes Secret.
//   - oidcConfig: OIDC via Kubernetes ServiceAccount token (identity + serviceAccountRef).
//
// project and config are required for both auth methods. With secretRef the
// service token already encodes the scope, but the wizard requires explicit
// values to produce self-documenting, reviewable YAML regardless of auth method.
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly
// when surfacing errors to the frontend.
func validateDopplerSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	authRaw, hasAuth := spec["auth"].(map[string]any)
	if !hasAuth {
		errs = append(errs, FieldError{Field: "auth", Message: "is required (one of secretRef, oidcConfig)"})
		return errs
	}

	method, methodErrs := pickDopplerAuthMethod(authRaw)
	errs = append(errs, methodErrs...)
	if method == "" {
		return errs
	}

	switch method {
	case "secretRef":
		errs = append(errs, validateDopplerAuthSecretRef(authRaw)...)
	case "oidcConfig":
		errs = append(errs, validateDopplerAuthOIDC(authRaw)...)
	}

	// project is required when present and non-empty is set; the wizard
	// enforces it unconditionally for explicit, reviewable YAML output.
	project, _ := spec["project"].(string)
	if strings.TrimSpace(project) == "" {
		errs = append(errs, FieldError{Field: "project", Message: "is required"})
	}

	config, _ := spec["config"].(string)
	if strings.TrimSpace(config) == "" {
		errs = append(errs, FieldError{Field: "config", Message: "is required"})
	}

	return errs
}

// pickDopplerAuthMethod returns the single auth method present in the auth
// block. ESO requires exactly one of secretRef or oidcConfig.
// Multiple methods or no method both produce errors.
func pickDopplerAuthMethod(auth map[string]any) (string, []FieldError) {
	// orderedDopplerAuthMethods defines the fixed iteration order for a
	// deterministic error message when multiple methods are set.
	const (
		methodSecretRef  = "secretRef"
		methodOIDCConfig = "oidcConfig"
	)
	orderedMethods := []string{methodSecretRef, methodOIDCConfig}

	var present []string
	for _, m := range orderedMethods {
		if _, ok := auth[m]; ok {
			present = append(present, m)
		}
	}
	switch len(present) {
	case 0:
		return "", []FieldError{{Field: "auth", Message: "exactly one of secretRef, oidcConfig must be set"}}
	case 1:
		return present[0], nil
	default:
		return "", []FieldError{{Field: "auth", Message: "only one auth method may be set; got secretRef and oidcConfig"}}
	}
}

// validateDopplerAuthSecretRef validates the auth.secretRef block.
// ESO's DopplerAuthSecretRef requires auth.secretRef.dopplerToken.{name, key}.
func validateDopplerAuthSecretRef(auth map[string]any) []FieldError {
	sr, _ := auth["secretRef"].(map[string]any)
	if sr == nil {
		return []FieldError{{Field: "auth.secretRef", Message: "is required"}}
	}
	tokenRef, _ := sr["dopplerToken"].(map[string]any)
	if tokenRef == nil {
		return []FieldError{{Field: "auth.secretRef.dopplerToken", Message: "is required"}}
	}
	return validateDopplerSecretRef(tokenRef, "auth.secretRef.dopplerToken")
}

// validateDopplerAuthOIDC validates the auth.oidcConfig block.
// ESO's DopplerOIDCAuth requires identity and serviceAccountRef.name.
func validateDopplerAuthOIDC(auth map[string]any) []FieldError {
	var errs []FieldError
	oidc, _ := auth["oidcConfig"].(map[string]any)
	if oidc == nil {
		return []FieldError{{Field: "auth.oidcConfig", Message: "is required"}}
	}
	if identity, _ := oidc["identity"].(string); strings.TrimSpace(identity) == "" {
		errs = append(errs, FieldError{Field: "auth.oidcConfig.identity", Message: "is required"})
	}
	saRef, _ := oidc["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		errs = append(errs, FieldError{Field: "auth.oidcConfig.serviceAccountRef", Message: "is required"})
	} else {
		if name, _ := saRef["name"].(string); strings.TrimSpace(name) == "" {
			errs = append(errs, FieldError{Field: "auth.oidcConfig.serviceAccountRef.name", Message: "is required"})
		} else if !dnsLabelRegex.MatchString(name) {
			errs = append(errs, FieldError{Field: "auth.oidcConfig.serviceAccountRef.name", Message: "must be a valid DNS label"})
		}
	}
	return errs
}

// validateDopplerSecretRef validates an ESO SecretKeySelector at the given
// field prefix. Requires name and key; namespace is optional.
func validateDopplerSecretRef(spec map[string]any, prefix string) []FieldError {
	var errs []FieldError
	if spec == nil {
		errs = append(errs, FieldError{Field: prefix, Message: "is required"})
		return errs
	}
	if name, _ := spec["name"].(string); name == "" {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(name) {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "must be a valid DNS label"})
	}
	if key, _ := spec["key"].(string); key == "" {
		errs = append(errs, FieldError{Field: prefix + ".key", Message: "is required"})
	}
	return errs
}
