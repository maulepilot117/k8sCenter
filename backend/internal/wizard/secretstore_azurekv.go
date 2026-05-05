package wizard

import (
	"strings"
)

// init registers the Azure Key Vault provider validator with the SecretStore
// wizard dispatcher. Lives in this file so the validator ships and registers
// as one unit — adding a provider is a single-file edit + one line in
// READY_SECRET_STORE_PROVIDERS on the frontend.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderAzure, validateAzureKVSpec)
}

// orderedAzureKVAuthTypes is the canonical ordered list of auth types the
// wizard supports in v1. Ordering matters for the "invalid authType" error
// message so it is deterministic.
var orderedAzureKVAuthTypes = []string{
	"ManagedIdentity",
	"ServicePrincipal",
	"WorkloadIdentity",
}

// validateAzureKVSpec validates a SecretStoreInput.ProviderSpec for the Azure
// Key Vault provider. The spec mirrors ESO's spec.provider.azurekv shape —
// vaultUrl, tenantId (required for SP and WI), authType (discriminator), and
// per-type auth fields.
//
// Azure's auth discriminator differs from Vault: it uses a top-level
// `authType` string rather than a nested `auth.<method>` sub-block. All auth
// credentials live as sibling fields of `authType` at the top level of the
// spec. This function validates that shape directly.
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly
// when surfacing errors to the frontend.
func validateAzureKVSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	// vaultUrl is required and must use https.
	vaultURL, _ := spec["vaultUrl"].(string)
	if strings.TrimSpace(vaultURL) == "" {
		errs = append(errs, FieldError{Field: "vaultUrl", Message: "is required"})
	} else if !strings.HasPrefix(strings.ToLower(vaultURL), "https://") {
		errs = append(errs, FieldError{Field: "vaultUrl", Message: "must use https scheme"})
	}

	// authType is required and must be one of the three supported values.
	authType, _ := spec["authType"].(string)
	if authType == "" {
		errs = append(errs, FieldError{
			Field:   "authType",
			Message: "is required (one of ManagedIdentity, ServicePrincipal, WorkloadIdentity)",
		})
		return errs
	}
	if !isValidAzureAuthType(authType) {
		errs = append(errs, FieldError{
			Field:   "authType",
			Message: "must be one of ManagedIdentity, ServicePrincipal, WorkloadIdentity",
		})
		return errs
	}

	// Per-type validation.
	switch authType {
	case "ManagedIdentity":
		errs = append(errs, validateAzureKVManagedIdentity(spec)...)
	case "ServicePrincipal":
		errs = append(errs, validateAzureKVServicePrincipal(spec)...)
	case "WorkloadIdentity":
		errs = append(errs, validateAzureKVWorkloadIdentity(spec)...)
	}

	return errs
}

// isValidAzureAuthType returns true when s is one of the supported auth types.
func isValidAzureAuthType(s string) bool {
	for _, v := range orderedAzureKVAuthTypes {
		if v == s {
			return true
		}
	}
	return false
}

// validateAzureKVManagedIdentity validates fields specific to the
// ManagedIdentity auth type.
//
//   - tenantId: optional (AKS sets it automatically from the cluster identity).
//   - identityId: optional client ID for multi-identity pods; blank means
//     "use the AKS-bound managed identity".
//
// Neither field is required — ESO defaults both from the pod's MI binding.
// identityId is rejected when present but whitespace-only.
func validateAzureKVManagedIdentity(spec map[string]any) []FieldError {
	var errs []FieldError
	if identityID, ok := spec["identityId"].(string); ok {
		// Key is present; reject whitespace-only values.
		if strings.TrimSpace(identityID) == "" {
			errs = append(errs, FieldError{Field: "identityId", Message: "must not be empty when set"})
		}
	}
	return errs
}

// validateAzureKVServicePrincipal validates fields specific to the
// ServicePrincipal auth type.
//
// Required:
//   - tenantId (string at spec root)
//   - authSecretRef.clientId.{name, key}
//   - authSecretRef.clientSecret.{name, key}
func validateAzureKVServicePrincipal(spec map[string]any) []FieldError {
	var errs []FieldError

	// tenantId is required for ServicePrincipal.
	tenantID, _ := spec["tenantId"].(string)
	if strings.TrimSpace(tenantID) == "" {
		errs = append(errs, FieldError{Field: "tenantId", Message: "is required for ServicePrincipal"})
	}

	// authSecretRef must be present and contain clientId + clientSecret.
	authSecretRef, _ := spec["authSecretRef"].(map[string]any)
	if authSecretRef == nil {
		errs = append(errs, FieldError{Field: "authSecretRef", Message: "is required for ServicePrincipal"})
		return errs
	}

	clientID, _ := authSecretRef["clientId"].(map[string]any)
	if clientID == nil {
		errs = append(errs, FieldError{Field: "authSecretRef.clientId", Message: "is required"})
	} else {
		errs = append(errs, validateAzureKVSecretRef(clientID, "authSecretRef.clientId")...)
	}

	clientSecret, _ := authSecretRef["clientSecret"].(map[string]any)
	if clientSecret == nil {
		errs = append(errs, FieldError{Field: "authSecretRef.clientSecret", Message: "is required"})
	} else {
		errs = append(errs, validateAzureKVSecretRef(clientSecret, "authSecretRef.clientSecret")...)
	}

	return errs
}

// validateAzureKVWorkloadIdentity validates fields specific to the
// WorkloadIdentity auth type.
//
// Required:
//   - tenantId (string at spec root)
//   - serviceAccountRef.name (string)
//
// ESO's AzureKVProvider has no plain-string clientId field at the spec root
// for WorkloadIdentity — the only clientId is inside authSecretRef (a
// SecretKeySelector used by ServicePrincipal). Do not add clientId here.
func validateAzureKVWorkloadIdentity(spec map[string]any) []FieldError {
	var errs []FieldError

	// tenantId is required for WorkloadIdentity.
	tenantID, _ := spec["tenantId"].(string)
	if strings.TrimSpace(tenantID) == "" {
		errs = append(errs, FieldError{Field: "tenantId", Message: "is required for WorkloadIdentity"})
	}

	// serviceAccountRef.name is required.
	saRef, _ := spec["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		errs = append(errs, FieldError{Field: "serviceAccountRef", Message: "is required for WorkloadIdentity"})
	} else {
		saName, _ := saRef["name"].(string)
		if strings.TrimSpace(saName) == "" {
			errs = append(errs, FieldError{Field: "serviceAccountRef.name", Message: "is required"})
		} else if !dnsLabelRegex.MatchString(saName) {
			errs = append(errs, FieldError{Field: "serviceAccountRef.name", Message: "must be a valid DNS label"})
		}
	}

	return errs
}

// validateAzureKVSecretRef validates an ESO secretRef sub-block (the
// Azure-flavored SecretKeySelector: name + key). Identical constraint to
// Vault's validateVaultSecretRef but scoped to Azure paths.
func validateAzureKVSecretRef(ref map[string]any, prefix string) []FieldError {
	var errs []FieldError
	if name, _ := ref["name"].(string); name == "" {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(name) {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "must be a valid DNS label"})
	}
	if key, _ := ref["key"].(string); key == "" {
		errs = append(errs, FieldError{Field: prefix + ".key", Message: "is required"})
	}
	return errs
}
