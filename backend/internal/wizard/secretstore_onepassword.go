package wizard

import (
	"strings"
)

// init registers the 1Password Connect provider validator with the SecretStore
// wizard dispatcher. The ESO provider key is "onepassword" — this is the
// Connect-based provider (auth via connectTokenSecretRef + connectHost +
// vaults map). ESO does NOT have a separate "onepasswordsdk" provider key in
// v1beta1; the U18 enum constant SecretStoreProviderOnePassword was
// incorrectly set to "onepasswordsdk" and is corrected in secretstore.go as
// part of this unit.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderOnePassword, validate1PasswordSpec)
}

// validate1PasswordSpec validates a SecretStoreInput.ProviderSpec for the
// 1Password Connect provider. The spec mirrors ESO's
// spec.provider.onepassword shape:
//
//	connectHost   string               — required HTTPS URL of the Connect server
//	auth.secretRef.connectTokenSecretRef.{name, key} — required Connect token ref
//	vaults        map[string]int       — required; at least one entry
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly.
func validate1PasswordSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	// --- connectHost -------------------------------------------------------
	host, _ := spec["connectHost"].(string)
	if strings.TrimSpace(host) == "" {
		errs = append(errs, FieldError{
			Field:   "connectHost",
			Message: "is required (HTTPS URL of the 1Password Connect server)",
		})
	} else if !strings.HasPrefix(strings.ToLower(host), "https://") {
		errs = append(errs, FieldError{
			Field:   "connectHost",
			Message: "must use https scheme",
		})
	}

	// --- auth.secretRef.connectTokenSecretRef ------------------------------
	authRaw, hasAuth := spec["auth"].(map[string]any)
	if !hasAuth {
		errs = append(errs, FieldError{
			Field:   "auth",
			Message: "is required (must contain secretRef.connectTokenSecretRef)",
		})
		// Skip auth sub-validation — can't walk further.
		errs = append(errs, validate1PasswordVaults(spec)...)
		return errs
	}

	secretRefRaw, hasSecretRef := authRaw["secretRef"].(map[string]any)
	if !hasSecretRef {
		errs = append(errs, FieldError{
			Field:   "auth.secretRef",
			Message: "is required",
		})
	} else {
		tokenRef, _ := secretRefRaw["connectTokenSecretRef"].(map[string]any)
		if tokenRef == nil {
			errs = append(errs, FieldError{
				Field:   "auth.secretRef.connectTokenSecretRef",
				Message: "is required",
			})
		} else {
			if name, _ := tokenRef["name"].(string); strings.TrimSpace(name) == "" {
				errs = append(errs, FieldError{
					Field:   "auth.secretRef.connectTokenSecretRef.name",
					Message: "is required",
				})
			} else if !dnsLabelRegex.MatchString(name) {
				errs = append(errs, FieldError{
					Field:   "auth.secretRef.connectTokenSecretRef.name",
					Message: "must be a valid DNS label",
				})
			}
			if key, _ := tokenRef["key"].(string); strings.TrimSpace(key) == "" {
				errs = append(errs, FieldError{
					Field:   "auth.secretRef.connectTokenSecretRef.key",
					Message: "is required",
				})
			}
		}
	}

	// --- vaults ------------------------------------------------------------
	errs = append(errs, validate1PasswordVaults(spec)...)

	return errs
}

// validate1PasswordVaults validates the vaults field. ESO requires a
// non-empty map[string]int where keys are vault names and values are search
// priority integers (lower = higher priority). The wizard requires at least
// one entry so the YAML preview is immediately usable.
func validate1PasswordVaults(spec map[string]any) []FieldError {
	raw, hasVaults := spec["vaults"]
	if !hasVaults {
		return []FieldError{{
			Field:   "vaults",
			Message: "is required (at least one vault name → priority entry)",
		}}
	}

	switch v := raw.(type) {
	case map[string]any:
		if len(v) == 0 {
			return []FieldError{{
				Field:   "vaults",
				Message: "must contain at least one entry",
			}}
		}
		// Validate that all values are numeric (int or float64 from JSON decode).
		for vaultName, priority := range v {
			if vaultName == "" {
				return []FieldError{{
					Field:   "vaults",
					Message: "vault name must not be empty",
				}}
			}
			switch priority.(type) {
			case int, int64, float64:
				// ok
			default:
				return []FieldError{{
					Field:   "vaults",
					Message: "vault priority must be an integer",
				}}
			}
		}
	case map[string]int:
		if len(v) == 0 {
			return []FieldError{{
				Field:   "vaults",
				Message: "must contain at least one entry",
			}}
		}
	default:
		return []FieldError{{
			Field:   "vaults",
			Message: "must be a map of vault name to priority integer",
		}}
	}

	return nil
}
