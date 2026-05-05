package wizard

import (
	"strings"
)

// init registers the GCP Secret Manager provider validator with the SecretStore
// wizard dispatcher. Lives in this file so the validator ships and registers as
// one unit — adding a provider is a single-file edit + one line in
// READY_SECRET_STORE_PROVIDERS on the frontend.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderGCP, validateGCPSMSpec)
}

// orderedGCPSMAuthMethods is the canonical ordered list of auth methods the
// wizard surface supports in v1.
//
// workloadIdentityFederation (credConfig / AWS token exchange) is accessible
// via the YAML editor but not driven by guided fields here — per the plan's
// L7.2 culling pass (niche multi-cloud federation path).
//
// Ordering matters: pickGCPSMAuthMethod iterates this slice to build the
// "present" list so the multi-method error message is deterministic rather
// than random-map-order.
var orderedGCPSMAuthMethods = []string{"workloadIdentity", "secretRef"}

// validateGCPSMSpec validates a SecretStoreInput.ProviderSpec for the GCP
// Secret Manager provider. The spec mirrors ESO's spec.provider.gcpsm shape —
// projectID (required), location (optional), plus an auth block with at most
// one method populated. An empty auth block is also accepted — that means
// "use the in-cluster default credentials" (GKE metadata server / node
// identity), which is a valid ESO configuration.
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly
// when surfacing errors to the frontend.
func validateGCPSMSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	projectID, _ := spec["projectID"].(string)
	if strings.TrimSpace(projectID) == "" {
		errs = append(errs, FieldError{Field: "projectID", Message: "is required (GCP project ID)"})
	}

	if loc, ok := spec["location"]; ok {
		s, _ := loc.(string)
		if s == "" {
			errs = append(errs, FieldError{Field: "location", Message: "must not be empty when set"})
		}
	}

	// auth is optional in ESO — omitting it falls back to the node / pod
	// identity (GKE workload identity default or Application Default Credentials).
	// When auth is present it must be valid; we parse it regardless.
	authRaw, hasAuth := spec["auth"].(map[string]any)
	if !hasAuth {
		// No auth block — valid (default-credentials path).
		return errs
	}

	method, methodErrs := pickGCPSMAuthMethod(authRaw)
	errs = append(errs, methodErrs...)
	if method == "" {
		// Either empty block (default-credentials) or multi-method error already
		// appended. Return early to avoid spurious sub-errors on nil sub-blocks.
		return errs
	}

	switch method {
	case "workloadIdentity":
		errs = append(errs, validateGCPSMAuthWorkloadIdentity(authRaw)...)
	case "secretRef":
		errs = append(errs, validateGCPSMAuthSecretRef(authRaw)...)
	}

	return errs
}

// pickGCPSMAuthMethod returns the single auth method present in the auth
// block, or "" with no error when the block is empty (default-credentials
// path). Multiple methods produce an error so the wizard rejects ambiguity.
//
// Iterates orderedGCPSMAuthMethods (not a map) so the multi-method error
// message lists methods in a deterministic, readable order.
func pickGCPSMAuthMethod(auth map[string]any) (string, []FieldError) {
	var present []string
	for _, method := range orderedGCPSMAuthMethods {
		if _, ok := auth[method]; ok {
			present = append(present, method)
		}
	}
	switch len(present) {
	case 0:
		// Empty block — valid default-credentials path; caller handles it.
		return "", nil
	case 1:
		return present[0], nil
	default:
		return "", []FieldError{{
			Field: "auth",
			Message: "only one auth method may be set; got " + strings.Join(present, ", ") +
				" (workloadIdentity and secretRef are mutually exclusive)",
		}}
	}
}

// validateGCPSMAuthWorkloadIdentity validates the workloadIdentity auth block.
//
// ESO requires serviceAccountRef.name; clusterLocation, clusterName, and
// clusterProjectID are optional (fetched from the GKE metadata server when
// omitted). The wizard enforces serviceAccountRef.name since omitting it
// produces an ambiguous ESO error at runtime.
func validateGCPSMAuthWorkloadIdentity(auth map[string]any) []FieldError {
	var errs []FieldError
	wi, _ := auth["workloadIdentity"].(map[string]any)
	if wi == nil {
		return []FieldError{{Field: "auth.workloadIdentity", Message: "is required"}}
	}

	saRef, _ := wi["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		errs = append(errs, FieldError{
			Field:   "auth.workloadIdentity.serviceAccountRef",
			Message: "is required",
		})
	} else {
		name, _ := saRef["name"].(string)
		if strings.TrimSpace(name) == "" {
			errs = append(errs, FieldError{
				Field:   "auth.workloadIdentity.serviceAccountRef.name",
				Message: "is required (Kubernetes ServiceAccount name to impersonate)",
			})
		} else if !dnsLabelRegex.MatchString(name) {
			errs = append(errs, FieldError{
				Field:   "auth.workloadIdentity.serviceAccountRef.name",
				Message: "must be a valid DNS label",
			})
		}
	}

	// Optional string fields — reject explicitly-empty values so the wizard
	// avoids sending confusing empty strings to ESO.
	for _, field := range []struct {
		key   string
		label string
	}{
		{"clusterLocation", "auth.workloadIdentity.clusterLocation"},
		{"clusterName", "auth.workloadIdentity.clusterName"},
		{"clusterProjectID", "auth.workloadIdentity.clusterProjectID"},
	} {
		if v, ok := wi[field.key]; ok {
			s, _ := v.(string)
			if s == "" {
				errs = append(errs, FieldError{
					Field:   field.label,
					Message: "must not be empty when set",
				})
			}
		}
	}

	return errs
}

// validateGCPSMAuthSecretRef validates the secretRef auth block.
//
// ESO expects auth.secretRef.secretAccessKeySecretRef.{name, key} — the
// Kubernetes Secret containing the GCP Service Account JSON key file.
func validateGCPSMAuthSecretRef(auth map[string]any) []FieldError {
	var errs []FieldError
	ref, _ := auth["secretRef"].(map[string]any)
	if ref == nil {
		return []FieldError{{Field: "auth.secretRef", Message: "is required"}}
	}

	sakRef, _ := ref["secretAccessKeySecretRef"].(map[string]any)
	if sakRef == nil {
		return []FieldError{{
			Field:   "auth.secretRef.secretAccessKeySecretRef",
			Message: "is required (Kubernetes Secret containing the GCP SA JSON key)",
		}}
	}

	name, _ := sakRef["name"].(string)
	if strings.TrimSpace(name) == "" {
		errs = append(errs, FieldError{
			Field:   "auth.secretRef.secretAccessKeySecretRef.name",
			Message: "is required",
		})
	} else if !dnsLabelRegex.MatchString(name) {
		errs = append(errs, FieldError{
			Field:   "auth.secretRef.secretAccessKeySecretRef.name",
			Message: "must be a valid DNS label",
		})
	}

	key, _ := sakRef["key"].(string)
	if strings.TrimSpace(key) == "" {
		errs = append(errs, FieldError{
			Field:   "auth.secretRef.secretAccessKeySecretRef.key",
			Message: "is required (key within the Secret holding the SA JSON)",
		})
	}

	return errs
}
