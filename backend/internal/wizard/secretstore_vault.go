package wizard

import (
	"fmt"
	"strings"
)

// init registers the Vault provider validator with the SecretStore wizard
// dispatcher. Lives in this file so the validator ships and registers as one
// unit — adding a provider is a single-file edit + one line in
// READY_SECRET_STORE_PROVIDERS on the frontend.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderVault, validateVaultSpec)
}

// orderedVaultAuthMethods is the canonical ordered list of auth methods the
// wizard surface supports in v1. ESO additionally supports userPass, ldap,
// iam, and gcp on Vault — those are accessible via the YAML editor but not
// driven by guided fields here (per the plan's L7.2 culling pass).
//
// Ordering matters: pickVaultAuthMethod iterates this slice to build the
// "present" list so the multi-method error message is deterministic rather
// than random-map-order.
var orderedVaultAuthMethods = []string{"token", "kubernetes", "appRole", "jwt", "cert"}

// validVaultKVVersions lists the spec.provider.vault.version values ESO accepts.
// "v2" is the upstream default; the wizard emits whatever the user picks
// rather than relying on the controller default so the YAML preview is
// self-explanatory.
var validVaultKVVersions = map[string]bool{
	"v1": true,
	"v2": true,
}

// validateVaultSpec validates a SecretStoreInput.ProviderSpec for the Vault
// provider. The spec mirrors ESO's spec.provider.vault shape — server, path,
// version, namespace, caBundle/caProvider, plus an auth block with exactly
// one method populated.
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly
// when surfacing errors to the frontend.
func validateVaultSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	server, _ := spec["server"].(string)
	if strings.TrimSpace(server) == "" {
		errs = append(errs, FieldError{Field: "server", Message: "is required"})
	} else {
		// validateVaultServerURL accepts private and in-cluster addresses —
		// only non-HTTPS schemes are rejected (homelab Vault is common).
		errs = append(errs, validateVaultServerURL(server)...)
	}

	if v, ok := spec["version"]; ok {
		s, _ := v.(string)
		if !validVaultKVVersions[s] {
			errs = append(errs, FieldError{Field: "version", Message: "must be v1 or v2"})
		}
	}

	if p, ok := spec["path"]; ok {
		s, _ := p.(string)
		if s == "" {
			errs = append(errs, FieldError{Field: "path", Message: "must not be empty when set"})
		} else if strings.HasPrefix(s, "/") {
			errs = append(errs, FieldError{Field: "path", Message: "must not start with a slash"})
		}
	}

	if ns, ok := spec["namespace"]; ok {
		s, _ := ns.(string)
		if s == "" {
			errs = append(errs, FieldError{Field: "namespace", Message: "must not be empty when set"})
		}
	}

	authRaw, hasAuth := spec["auth"].(map[string]any)
	if !hasAuth {
		errs = append(errs, FieldError{Field: "auth", Message: "is required (one of token, kubernetes, appRole, jwt, cert)"})
		return errs
	}

	method, methodErrs := pickVaultAuthMethod(authRaw)
	errs = append(errs, methodErrs...)
	if method == "" {
		return errs
	}

	switch method {
	case "token":
		errs = append(errs, validateVaultAuthToken(authRaw)...)
	case "kubernetes":
		errs = append(errs, validateVaultAuthKubernetes(authRaw)...)
	case "appRole":
		errs = append(errs, validateVaultAuthAppRole(authRaw)...)
	case "jwt":
		errs = append(errs, validateVaultAuthJWT(authRaw)...)
	case "cert":
		errs = append(errs, validateVaultAuthCert(authRaw)...)
	}

	return errs
}

// validateVaultServerURL accepts both public and private addresses for
// Vault (homelab + in-cluster reach are common) but rejects non-HTTPS
// schemes. Mirrors validateHTTPSPublicURL but without the public-IP gate.
func validateVaultServerURL(raw string) []FieldError {
	if !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return []FieldError{{Field: "server", Message: "must use https scheme"}}
	}
	return nil
}

// pickVaultAuthMethod returns the single auth method present in the auth
// block. Multiple methods or no method both produce errors so the wizard
// rejects ambiguity rather than letting the controller pick silently.
//
// Iterates orderedVaultAuthMethods (not a map) so the multi-method error
// message lists methods in a deterministic, readable order.
func pickVaultAuthMethod(auth map[string]any) (string, []FieldError) {
	var present []string
	for _, method := range orderedVaultAuthMethods {
		if _, ok := auth[method]; ok {
			present = append(present, method)
		}
	}
	switch len(present) {
	case 0:
		return "", []FieldError{{Field: "auth", Message: "exactly one of token, kubernetes, appRole, jwt, cert must be set"}}
	case 1:
		return present[0], nil
	default:
		return "", []FieldError{{Field: "auth", Message: fmt.Sprintf("only one auth method may be set; got %d (%s)", len(present), strings.Join(present, ", "))}}
	}
}

// validateVaultSecretRef validates an ESO secretRef sub-block at the given
// field prefix. ESO's SecretKeySelector requires `name` and `key`; namespace
// is optional and only meaningful for ClusterSecretStore.
func validateVaultSecretRef(spec map[string]any, prefix string) []FieldError {
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

func validateVaultAuthToken(auth map[string]any) []FieldError {
	ref, _ := auth["token"].(map[string]any)
	tokenRef, _ := ref["tokenSecretRef"].(map[string]any)
	if tokenRef == nil {
		// ESO requires the canonical nesting: auth.token.tokenSecretRef.{name,key}.
		// Flat shapes ({token: {name, key}}) are not accepted.
		return []FieldError{{Field: "auth.token.tokenSecretRef", Message: "is required"}}
	}
	return validateVaultSecretRef(tokenRef, "auth.token.tokenSecretRef")
}

func validateVaultAuthKubernetes(auth map[string]any) []FieldError {
	var errs []FieldError
	k8s, _ := auth["kubernetes"].(map[string]any)
	if k8s == nil {
		return []FieldError{{Field: "auth.kubernetes", Message: "is required"}}
	}
	if mp, _ := k8s["mountPath"].(string); strings.TrimSpace(mp) == "" {
		errs = append(errs, FieldError{Field: "auth.kubernetes.mountPath", Message: "is required (e.g. \"kubernetes\")"})
	}
	if role, _ := k8s["role"].(string); strings.TrimSpace(role) == "" {
		errs = append(errs, FieldError{Field: "auth.kubernetes.role", Message: "is required"})
	}
	// Either serviceAccountRef OR secretRef is conventional but not strictly
	// required by ESO (the controller can use its own SA). We don't enforce.
	return errs
}

// validateVaultAuthAppRole validates the AppRole auth block.
//
// v1 API/UI gap: auth.appRole.roleRef.* is accepted and validated by this
// function, but the wizard form only surfaces the roleId field. Direct API
// callers (YAML editor, curl) may use roleRef; the guided form does not.
func validateVaultAuthAppRole(auth map[string]any) []FieldError {
	var errs []FieldError
	ar, _ := auth["appRole"].(map[string]any)
	if ar == nil {
		return []FieldError{{Field: "auth.appRole", Message: "is required"}}
	}
	if path, _ := ar["path"].(string); strings.TrimSpace(path) == "" {
		errs = append(errs, FieldError{Field: "auth.appRole.path", Message: "is required (e.g. \"approle\")"})
	}
	// roleId or roleRef must be present.
	_, hasRoleID := ar["roleId"].(string)
	_, hasRoleRef := ar["roleRef"].(map[string]any)
	if !hasRoleID && !hasRoleRef {
		errs = append(errs, FieldError{Field: "auth.appRole.roleId", Message: "must specify roleId or roleRef"})
	}
	if hasRoleID && hasRoleRef {
		errs = append(errs, FieldError{Field: "auth.appRole.roleId", Message: "must not specify both roleId and roleRef"})
	}
	if hasRoleRef {
		errs = append(errs, validateVaultSecretRef(ar["roleRef"].(map[string]any), "auth.appRole.roleRef")...)
	}
	secretRef, _ := ar["secretRef"].(map[string]any)
	if secretRef == nil {
		errs = append(errs, FieldError{Field: "auth.appRole.secretRef", Message: "is required"})
	} else {
		errs = append(errs, validateVaultSecretRef(secretRef, "auth.appRole.secretRef")...)
	}
	return errs
}

// validateVaultAuthJWT validates the JWT/OIDC auth block.
//
// v1 API/UI gap: auth.jwt.kubernetesServiceAccountToken.* is accepted and
// validated via the hasKsat path, but the wizard form only surfaces the
// secretRef path. Direct API callers (YAML editor, curl) may use the SA token
// approach; the guided form does not surface it.
func validateVaultAuthJWT(auth map[string]any) []FieldError {
	var errs []FieldError
	j, _ := auth["jwt"].(map[string]any)
	if j == nil {
		return []FieldError{{Field: "auth.jwt", Message: "is required"}}
	}
	if path, _ := j["path"].(string); strings.TrimSpace(path) == "" {
		errs = append(errs, FieldError{Field: "auth.jwt.path", Message: "is required (e.g. \"jwt\")"})
	}
	// ESO accepts: secretRef OR kubernetesServiceAccountToken. Wizard
	// requires one source so the YAML preview produces a working ES.
	_, hasSecretRef := j["secretRef"].(map[string]any)
	_, hasKsat := j["kubernetesServiceAccountToken"].(map[string]any)
	if !hasSecretRef && !hasKsat {
		errs = append(errs, FieldError{Field: "auth.jwt.secretRef", Message: "must specify secretRef or kubernetesServiceAccountToken"})
	}
	if hasSecretRef {
		errs = append(errs, validateVaultSecretRef(j["secretRef"].(map[string]any), "auth.jwt.secretRef")...)
	}
	return errs
}

func validateVaultAuthCert(auth map[string]any) []FieldError {
	var errs []FieldError
	c, _ := auth["cert"].(map[string]any)
	if c == nil {
		return []FieldError{{Field: "auth.cert", Message: "is required"}}
	}
	if cl, _ := c["clientCert"].(map[string]any); cl == nil {
		errs = append(errs, FieldError{Field: "auth.cert.clientCert", Message: "is required"})
	} else {
		errs = append(errs, validateVaultSecretRef(cl, "auth.cert.clientCert")...)
	}
	if sr, _ := c["secretRef"].(map[string]any); sr == nil {
		errs = append(errs, FieldError{Field: "auth.cert.secretRef", Message: "is required"})
	} else {
		errs = append(errs, validateVaultSecretRef(sr, "auth.cert.secretRef")...)
	}
	return errs
}
