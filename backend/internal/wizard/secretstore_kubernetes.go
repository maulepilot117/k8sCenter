package wizard

import (
	"strings"
)

// init registers the Kubernetes provider validator with the SecretStore wizard
// dispatcher. Lives in this file so the validator ships and registers as one
// unit — adding a provider is a single-file edit + one line in
// READY_SECRET_STORE_PROVIDERS on the frontend.
func init() {
	RegisterSecretStoreProvider(SecretStoreProviderKubernetes, validateKubernetesSpec)
}

// orderedKubernetesAuthMethods is the canonical ordered list of auth methods
// the wizard surface supports in v1. ESO additionally accepts other forms;
// those are accessible via the YAML editor.
//
// Ordering matters: pickKubernetesAuthMethod iterates this slice so the
// multi-method error message is deterministic rather than random-map-order.
var orderedKubernetesAuthMethods = []string{"serviceAccount", "token", "cert"}

// validateKubernetesSpec validates a SecretStoreInput.ProviderSpec for the
// Kubernetes (cross-namespace) provider. The spec mirrors ESO's
// spec.provider.kubernetes shape:
//
//   - remoteNamespace — the namespace to read Secrets from. Defaults to
//     "default" in ESO when omitted; the wizard always emits it explicitly so
//     the YAML preview is self-explanatory.
//   - server.url — optional, defaults to the in-cluster apiserver.
//   - server.caBundle — optional, base64-encoded CA bundle.
//   - auth — exactly one of serviceAccount / token / cert must be set.
//
// Each FieldError's Field is rooted at the provider-spec level (no
// "providerSpec." prefix) so the dispatcher's caller can prefix uniformly
// when surfacing errors to the frontend.
func validateKubernetesSpec(spec map[string]any) []FieldError {
	var errs []FieldError

	// remoteNamespace: optional string but must not be blank when set.
	if ns, ok := spec["remoteNamespace"]; ok {
		if s, _ := ns.(string); strings.TrimSpace(s) == "" {
			errs = append(errs, FieldError{Field: "remoteNamespace", Message: "must not be empty when set"})
		} else if !dnsLabelRegex.MatchString(s) {
			errs = append(errs, FieldError{Field: "remoteNamespace", Message: "must be a valid DNS label"})
		}
	}

	// server block: optional, but sub-fields are validated when present.
	if serverRaw, ok := spec["server"]; ok {
		srv, _ := serverRaw.(map[string]any)
		if srv == nil {
			errs = append(errs, FieldError{Field: "server", Message: "must be an object"})
		} else {
			errs = append(errs, validateKubernetesServer(srv)...)
		}
	}

	// auth block: required, exactly one method.
	authRaw, hasAuth := spec["auth"].(map[string]any)
	if !hasAuth {
		errs = append(errs, FieldError{Field: "auth", Message: "is required (one of serviceAccount, token, cert)"})
		return errs
	}

	method, methodErrs := pickKubernetesAuthMethod(authRaw)
	errs = append(errs, methodErrs...)
	if method == "" {
		return errs
	}

	switch method {
	case "serviceAccount":
		errs = append(errs, validateKubernetesAuthServiceAccount(authRaw)...)
	case "token":
		errs = append(errs, validateKubernetesAuthToken(authRaw)...)
	case "cert":
		errs = append(errs, validateKubernetesAuthCert(authRaw)...)
	}

	return errs
}

// validateKubernetesServer validates the optional server sub-block.
// url is optional (defaults to in-cluster apiserver).
// caBundle is optional base64 — we only reject blank-when-set.
func validateKubernetesServer(srv map[string]any) []FieldError {
	var errs []FieldError
	if url, ok := srv["url"]; ok {
		u, _ := url.(string)
		if strings.TrimSpace(u) == "" {
			errs = append(errs, FieldError{Field: "server.url", Message: "must not be empty when set"})
		} else if !strings.HasPrefix(strings.ToLower(u), "https://") {
			errs = append(errs, FieldError{Field: "server.url", Message: "must use https scheme"})
		}
	}
	if cab, ok := srv["caBundle"]; ok {
		s, _ := cab.(string)
		if strings.TrimSpace(s) == "" {
			errs = append(errs, FieldError{Field: "server.caBundle", Message: "must not be empty when set"})
		}
	}
	return errs
}

// pickKubernetesAuthMethod returns the single auth method present in the auth
// block. Multiple methods or no method both produce errors.
func pickKubernetesAuthMethod(auth map[string]any) (string, []FieldError) {
	var present []string
	for _, method := range orderedKubernetesAuthMethods {
		if _, ok := auth[method]; ok {
			present = append(present, method)
		}
	}
	switch len(present) {
	case 0:
		return "", []FieldError{{Field: "auth", Message: "exactly one of serviceAccount, token, cert must be set"}}
	case 1:
		return present[0], nil
	default:
		joined := strings.Join(present, ", ")
		return "", []FieldError{{Field: "auth", Message: "only one auth method may be set; got " + joined}}
	}
}

// validateKubernetesAuthServiceAccount validates the serviceAccount auth block.
//
// ESO v1 KubernetesProvider.auth.serviceAccount shape:
//
//	serviceAccount:
//	  name: <name>           # required — the SA in remoteNamespace whose token ESO mounts
//	  audiences: [...]       # optional — list of token audiences
//	  namespace: <ns>        # optional — for ClusterSecretStore cross-namespace binding
func validateKubernetesAuthServiceAccount(auth map[string]any) []FieldError {
	var errs []FieldError
	sa, _ := auth["serviceAccount"].(map[string]any)
	if sa == nil {
		return []FieldError{{Field: "auth.serviceAccount", Message: "is required"}}
	}
	name, _ := sa["name"].(string)
	if strings.TrimSpace(name) == "" {
		errs = append(errs, FieldError{Field: "auth.serviceAccount.name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(name) {
		errs = append(errs, FieldError{Field: "auth.serviceAccount.name", Message: "must be a valid DNS label"})
	}
	// audiences: optional; when set must be a non-empty list.
	if aud, ok := sa["audiences"]; ok {
		if arr, ok2 := aud.([]any); !ok2 || len(arr) == 0 {
			errs = append(errs, FieldError{Field: "auth.serviceAccount.audiences", Message: "must be a non-empty list when set"})
		}
	}
	return errs
}

// validateKubernetesAuthToken validates the token bearer-token auth block.
//
// ESO v1 shape:
//
//	token:
//	  bearerToken:
//	    name: <secret-name>   # required
//	    key:  <key>           # required
//	    namespace: <ns>       # optional (ClusterSecretStore cross-namespace)
func validateKubernetesAuthToken(auth map[string]any) []FieldError {
	t, _ := auth["token"].(map[string]any)
	if t == nil {
		return []FieldError{{Field: "auth.token", Message: "is required"}}
	}
	bearerToken, _ := t["bearerToken"].(map[string]any)
	if bearerToken == nil {
		return []FieldError{{Field: "auth.token.bearerToken", Message: "is required"}}
	}
	return validateKubernetesSecretRef(bearerToken, "auth.token.bearerToken")
}

// validateKubernetesAuthCert validates the client-certificate auth block.
//
// ESO v1 shape:
//
//	cert:
//	  clientCert:
//	    name: <secret-name>  # required
//	    key:  <key>          # required
//	  clientKey:
//	    name: <secret-name>  # required
//	    key:  <key>          # required
func validateKubernetesAuthCert(auth map[string]any) []FieldError {
	var errs []FieldError
	c, _ := auth["cert"].(map[string]any)
	if c == nil {
		return []FieldError{{Field: "auth.cert", Message: "is required"}}
	}
	cc, _ := c["clientCert"].(map[string]any)
	if cc == nil {
		errs = append(errs, FieldError{Field: "auth.cert.clientCert", Message: "is required"})
	} else {
		errs = append(errs, validateKubernetesSecretRef(cc, "auth.cert.clientCert")...)
	}
	ck, _ := c["clientKey"].(map[string]any)
	if ck == nil {
		errs = append(errs, FieldError{Field: "auth.cert.clientKey", Message: "is required"})
	} else {
		errs = append(errs, validateKubernetesSecretRef(ck, "auth.cert.clientKey")...)
	}
	return errs
}

// validateKubernetesSecretRef validates an ESO SecretKeySelector (the shape
// used throughout the Kubernetes provider for all secretRef sub-blocks).
// Requires name and key; namespace is optional.
func validateKubernetesSecretRef(spec map[string]any, prefix string) []FieldError {
	var errs []FieldError
	if spec == nil {
		errs = append(errs, FieldError{Field: prefix, Message: "is required"})
		return errs
	}
	name, _ := spec["name"].(string)
	if strings.TrimSpace(name) == "" {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(name) {
		errs = append(errs, FieldError{Field: prefix + ".name", Message: "must be a valid DNS label"})
	}
	key, _ := spec["key"].(string)
	if strings.TrimSpace(key) == "" {
		errs = append(errs, FieldError{Field: prefix + ".key", Message: "is required"})
	}
	if ns, ok := spec["namespace"]; ok {
		if s, _ := ns.(string); s != "" && !dnsLabelRegex.MatchString(s) {
			errs = append(errs, FieldError{Field: prefix + ".namespace", Message: "must be a valid DNS label"})
		}
	}
	return errs
}
