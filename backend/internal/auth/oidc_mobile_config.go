package auth

// MobileAuthConfig is the OIDC-provider config a mobile client needs to
// construct its own authorization URL with client-generated PKCE + state +
// nonce. The provider's resolved authorization endpoint is read from the
// OIDC discovery doc cached on the [OIDCProvider]; clientSecret is
// intentionally excluded — it stays server-side and is used only during
// the body-mode token exchange in [OIDCProvider.ExchangeMobile].
//
// Mobile clients combine these fields with their own:
//   - redirect_uri: https://<UNIVERSAL_LINK_HOST>/m/auth/callback
//   - code_challenge: base64url(sha256(verifier)) — see lib/auth/pkce.dart
//   - state, nonce: random 32-char hex
//
// JSON wire key is `clientId` (lowercase d) to align with the rest of the
// k8sCenter API surface convention (`displayName`, `loginURL`, etc.).
// The Go field name stays `ClientID` per Go style; only the JSON tag
// follows the wire convention. See issue #282.
type MobileAuthConfig struct {
	AuthorizationEndpoint string   `json:"authorizationEndpoint"`
	ClientID              string   `json:"clientId"`
	Scopes                []string `json:"scopes"`
}

// MobileAuthConfig returns the data needed by a mobile client to build
// the IdP authorization URL. Safe to expose to unauthenticated callers —
// the same data is otherwise visible to anyone observing the IdP's
// `/.well-known/openid-configuration` plus the operator's registered
// public client id.
func (p *OIDCProvider) MobileAuthConfig() MobileAuthConfig {
	return MobileAuthConfig{
		AuthorizationEndpoint: p.oauth2Config.Endpoint.AuthURL,
		ClientID:              p.oauth2Config.ClientID,
		Scopes:                p.oauth2Config.Scopes,
	}
}
