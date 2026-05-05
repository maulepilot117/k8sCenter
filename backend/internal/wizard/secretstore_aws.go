package wizard

import (
	"fmt"
	"strings"
)

// init registers the AWS Secrets Manager provider validator with the
// SecretStore wizard dispatcher. The same validator handles both
// SecretStoreProviderAWS (Secrets Manager) and the synthetic AWSPS key after
// the ToSecretStore remap injects service: ParameterStore. We only register
// for SecretStoreProviderAWS here; AWSPS validation lands in its own sub-PR.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderAWS, validateAWSSpec)
}

// orderedAWSAuthMethods is the canonical ordered list of auth methods the
// wizard surface supports for AWS in v1. The ESO AWS provider also accepts
// implicit SDK-default credentials (no auth block), but the wizard requires
// an explicit choice so the YAML preview is self-documenting.
//
// Ordering matters: pickAWSAuthMethod iterates this slice to build the
// "present" list so the multi-method error message is deterministic rather
// than random-map-order.
var orderedAWSAuthMethods = []string{"jwt", "secretRef"}

// validateAWSSpec validates a SecretStoreInput.ProviderSpec for the AWS
// Secrets Manager provider. The spec mirrors ESO's spec.provider.aws shape —
// region (required), role (optional assume-role ARN), and an auth block
// with exactly one method populated.
//
// service is intentionally not surfaced here: when the wizard sends provider
// "aws" (Secrets Manager), ESO defaults to SecretsManager when service is
// absent. The "awsps" UX key injects service: ParameterStore upstream in
// ToSecretStore; it will have its own validator sub-PR.
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly
// when surfacing errors to the frontend.
func validateAWSSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	region, _ := spec["region"].(string)
	if strings.TrimSpace(region) == "" {
		errs = append(errs, FieldError{Field: "region", Message: "is required (e.g. \"us-east-1\")"})
	}

	// role is optional — an assume-role ARN. ESO controller validates the
	// ARN format at runtime; we only reject obviously empty strings when set.
	if v, ok := spec["role"]; ok {
		if s, _ := v.(string); strings.TrimSpace(s) == "" {
			errs = append(errs, FieldError{Field: "role", Message: "must not be empty when set"})
		}
	}

	authRaw, hasAuth := spec["auth"].(map[string]any)
	if !hasAuth {
		errs = append(errs, FieldError{Field: "auth", Message: "is required (one of jwt, secretRef)"})
		return errs
	}

	method, methodErrs := pickAWSAuthMethod(authRaw)
	errs = append(errs, methodErrs...)
	if method == "" {
		return errs
	}

	switch method {
	case "jwt":
		errs = append(errs, validateAWSAuthJWT(authRaw)...)
	case "secretRef":
		errs = append(errs, validateAWSAuthSecretRef(authRaw)...)
	}

	return errs
}

// pickAWSAuthMethod returns the single auth method present in the auth block.
// Multiple methods or no method both produce errors so the wizard rejects
// ambiguity rather than letting the controller pick silently.
//
// Iterates orderedAWSAuthMethods (not a map) so the multi-method error
// message lists methods in a deterministic, readable order.
func pickAWSAuthMethod(auth map[string]any) (string, []FieldError) {
	var present []string
	for _, method := range orderedAWSAuthMethods {
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

// validateAWSSecretKeyRef validates an ESO SecretKeySelector sub-block at the
// given field prefix. Requires name and key; namespace is optional (only
// meaningful for ClusterSecretStore).
func validateAWSSecretKeyRef(spec map[string]any, prefix string) []FieldError {
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
	if ns, ok := spec["namespace"]; ok {
		if s, _ := ns.(string); s != "" && !dnsLabelRegex.MatchString(s) {
			errs = append(errs, FieldError{Field: prefix + ".namespace", Message: "must be a valid DNS label"})
		}
	}
	return errs
}

// validateAWSAuthJWT validates the IAM workload-identity auth block.
// ESO maps this to spec.provider.aws.auth.jwt.serviceAccountRef.
//
// The wizard surfaces only the serviceAccountRef path (the most common
// in-cluster pattern). Direct API callers may pass additional JWT fields
// via the YAML editor.
func validateAWSAuthJWT(auth map[string]any) []FieldError {
	j, _ := auth["jwt"].(map[string]any)
	if j == nil {
		return []FieldError{{Field: "auth.jwt", Message: "is required"}}
	}
	saRef, _ := j["serviceAccountRef"].(map[string]any)
	if saRef == nil {
		return []FieldError{{Field: "auth.jwt.serviceAccountRef", Message: "is required"}}
	}
	name, _ := saRef["name"].(string)
	if strings.TrimSpace(name) == "" {
		return []FieldError{{Field: "auth.jwt.serviceAccountRef.name", Message: "is required"}}
	}
	if !dnsLabelRegex.MatchString(name) {
		return []FieldError{{Field: "auth.jwt.serviceAccountRef.name", Message: "must be a valid DNS label"}}
	}
	return nil
}

// validateAWSAuthSecretRef validates the static-credentials auth block.
// ESO maps this to spec.provider.aws.auth.secretRef with two sub-keys:
// accessKeyIDSecretRef and secretAccessKeySecretRef, both required.
func validateAWSAuthSecretRef(auth map[string]any) []FieldError {
	var errs []FieldError
	sr, _ := auth["secretRef"].(map[string]any)
	if sr == nil {
		return []FieldError{{Field: "auth.secretRef", Message: "is required"}}
	}

	akID, _ := sr["accessKeyIDSecretRef"].(map[string]any)
	if akID == nil {
		errs = append(errs, FieldError{Field: "auth.secretRef.accessKeyIDSecretRef", Message: "is required"})
	} else {
		errs = append(errs, validateAWSSecretKeyRef(akID, "auth.secretRef.accessKeyIDSecretRef")...)
	}

	sakKey, _ := sr["secretAccessKeySecretRef"].(map[string]any)
	if sakKey == nil {
		errs = append(errs, FieldError{Field: "auth.secretRef.secretAccessKeySecretRef", Message: "is required"})
	} else {
		errs = append(errs, validateAWSSecretKeyRef(sakKey, "auth.secretRef.secretAccessKeySecretRef")...)
	}

	return errs
}
