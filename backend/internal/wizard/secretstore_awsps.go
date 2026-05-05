package wizard

import (
	"fmt"
	"strings"
)

// init registers the AWS Parameter Store provider validator with the
// SecretStore wizard dispatcher. Lives in this file so the validator ships and
// registers as one unit — adding a provider is a single-file edit + one line
// in READY_SECRET_STORE_PROVIDERS on the frontend.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderAWSPS, validateAWSPSSpec)
}

// orderedAWSPSAuthMethods is the canonical ordered list of auth methods the
// wizard surface supports in v1.
//
// Ordering matters: pickAWSPSAuthMethod iterates this slice to build the
// "present" list so the multi-method error message is deterministic rather
// than random-map-order.
var orderedAWSPSAuthMethods = []string{"jwt", "secretRef"}

// validateAWSPSSpec validates a SecretStoreInput.ProviderSpec for the AWS
// Parameter Store provider. The spec mirrors ESO's spec.provider.aws shape
// for ParameterStore — region (required), optional role ARN, and an auth
// block with exactly one method populated (IAM workload identity via jwt, or
// static credentials via secretRef).
//
// Note: the "service: ParameterStore" field is NOT part of the wizard spec
// here; it is injected automatically by ToSecretStore when provider == awsps.
// Including it in providerSpec would produce a duplicate key in the emitted
// YAML.
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly
// when surfacing errors to the frontend.
func validateAWSPSSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	region, _ := spec["region"].(string)
	if strings.TrimSpace(region) == "" {
		errs = append(errs, FieldError{Field: "region", Message: "is required"})
	}

	if role, ok := spec["role"]; ok {
		r, _ := role.(string)
		if strings.TrimSpace(r) == "" {
			errs = append(errs, FieldError{Field: "role", Message: "must not be empty when set"})
		}
	}

	authRaw, hasAuth := spec["auth"].(map[string]any)
	if !hasAuth {
		errs = append(errs, FieldError{Field: "auth", Message: "is required (one of jwt, secretRef)"})
		return errs
	}

	method, methodErrs := pickAWSPSAuthMethod(authRaw)
	errs = append(errs, methodErrs...)
	if method == "" {
		return errs
	}

	switch method {
	case "jwt":
		errs = append(errs, validateAWSPSAuthJWT(authRaw)...)
	case "secretRef":
		errs = append(errs, validateAWSPSAuthSecretRef(authRaw)...)
	}

	return errs
}

// pickAWSPSAuthMethod returns the single auth method present in the auth
// block. Multiple methods or no method both produce errors so the wizard
// rejects ambiguity rather than letting the controller pick silently.
//
// Iterates orderedAWSPSAuthMethods (not a map) so the multi-method error
// message lists methods in a deterministic, readable order.
func pickAWSPSAuthMethod(auth map[string]any) (string, []FieldError) {
	var present []string
	for _, method := range orderedAWSPSAuthMethods {
		if _, ok := auth[method]; ok {
			present = append(present, method)
		}
	}
	switch len(present) {
	case 0:
		return "", []FieldError{{Field: "auth", Message: "exactly one of jwt, secretRef must be set"}}
	case 1:
		return present[0], nil
	default:
		return "", []FieldError{{Field: "auth", Message: fmt.Sprintf("only one auth method may be set; got %d (%s)", len(present), strings.Join(present, ", "))}}
	}
}

// validateAWSPSAuthJWT validates IAM workload identity auth via a Kubernetes
// service account JWT. ESO maps this to IRSA (IAM Roles for Service Accounts).
// The auth.jwt block requires serviceAccountRef.name + role.
func validateAWSPSAuthJWT(auth map[string]any) []FieldError {
	var errs []FieldError
	j, _ := auth["jwt"].(map[string]any)
	if j == nil {
		return []FieldError{{Field: "auth.jwt", Message: "is required"}}
	}

	saRef, _ := j["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		errs = append(errs, FieldError{Field: "auth.jwt.serviceAccountRef", Message: "is required"})
	} else {
		if name, _ := saRef["name"].(string); strings.TrimSpace(name) == "" {
			errs = append(errs, FieldError{Field: "auth.jwt.serviceAccountRef.name", Message: "is required"})
		} else if !dnsLabelRegex.MatchString(name) {
			errs = append(errs, FieldError{Field: "auth.jwt.serviceAccountRef.name", Message: "must be a valid DNS label"})
		}
	}

	if role, _ := j["role"].(string); strings.TrimSpace(role) == "" {
		errs = append(errs, FieldError{Field: "auth.jwt.role", Message: "is required (IAM role ARN)"})
	}

	return errs
}

// validateAWSPSAuthSecretRef validates static credential auth via two Kubernetes
// Secrets: one for the Access Key ID and one for the Secret Access Key.
func validateAWSPSAuthSecretRef(auth map[string]any) []FieldError {
	var errs []FieldError
	sr, _ := auth["secretRef"].(map[string]any)
	if sr == nil {
		return []FieldError{{Field: "auth.secretRef", Message: "is required"}}
	}

	akRef, _ := sr["accessKeyIDSecretRef"].(map[string]any)
	if akRef == nil {
		errs = append(errs, FieldError{Field: "auth.secretRef.accessKeyIDSecretRef", Message: "is required"})
	} else {
		errs = append(errs, validateAWSPSSecretKeyRef(akRef, "auth.secretRef.accessKeyIDSecretRef")...)
	}

	sakRef, _ := sr["secretAccessKeySecretRef"].(map[string]any)
	if sakRef == nil {
		errs = append(errs, FieldError{Field: "auth.secretRef.secretAccessKeySecretRef", Message: "is required"})
	} else {
		errs = append(errs, validateAWSPSSecretKeyRef(sakRef, "auth.secretRef.secretAccessKeySecretRef")...)
	}

	return errs
}

// validateAWSPSSecretKeyRef validates an ESO SecretKeySelector sub-block at
// the given field prefix. Requires name and key; namespace is optional.
func validateAWSPSSecretKeyRef(ref map[string]any, prefix string) []FieldError {
	var errs []FieldError
	if ref == nil {
		errs = append(errs, FieldError{Field: prefix, Message: "is required"})
		return errs
	}
	if name, _ := ref["name"].(string); name == "" {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(name) {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "must be a valid DNS label"})
	}
	if key, _ := ref["key"].(string); key == "" {
		errs = append(errs, FieldError{Field: prefix + ".key", Message: "is required"})
	}
	if ns, ok := ref["namespace"]; ok {
		if s, _ := ns.(string); s != "" && !dnsLabelRegex.MatchString(s) {
			errs = append(errs, FieldError{Field: prefix + ".namespace", Message: "must be a valid DNS label"})
		}
	}
	return errs
}
