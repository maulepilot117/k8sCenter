package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// ExchangeMobile is the mobile counterpart to [OIDCProvider.HandleCallback].
//
// Mobile clients generate the PKCE verifier and nonce client-side per
// RFC 8252 (Native OAuth Apps). The orchestrator opens the IdP authorization
// URL inside a Custom-Tab / SFSafariViewController, intercepts the redirect
// via Universal Link / App Link, validates `state` against its own CSRF
// token, and POSTs `{code, state, codeVerifier, nonce}` to the body-mode
// exchange endpoint. This method runs the equivalent of HandleCallback —
// token exchange, ID-token verification, claim mapping — but sources the
// verifier and nonce from method parameters instead of the server-side
// [OIDCFlowState] store.
//
// State is NOT re-validated here; it is the mobile client's CSRF token and
// only meaningful to the client. The expectedNonce IS validated against
// the ID token's `nonce` claim — closing the ID-token-replay window that
// PKCE alone does not cover.
func (p *OIDCProvider) ExchangeMobile(ctx context.Context, code, codeVerifier, expectedNonce string) (*User, error) {
	if code == "" {
		return nil, fmt.Errorf("code required")
	}
	if codeVerifier == "" {
		return nil, fmt.Errorf("codeVerifier required")
	}
	if expectedNonce == "" {
		return nil, fmt.Errorf("nonce required")
	}

	// Inject the provider-specific HTTP client (custom CA, TLS overrides)
	// into the exchange context. Without this, Exchange falls back to
	// http.DefaultClient and silently ignores CACertPath / TLSInsecure.
	exchangeCtx := oidc.ClientContext(ctx, p.httpClient)

	oauth2Token, err := p.oauth2Config.Exchange(exchangeCtx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token in token response")
	}

	// Full ID token verification: signature, issuer, audience, expiry. Do
	// NOT decode the JWT manually — Verify() is the only path that runs
	// the cryptographic checks.
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("ID token verification failed: %w", err)
	}

	// Validate nonce. go-oidc does not validate this automatically.
	if idToken.Nonce != expectedNonce {
		return nil, fmt.Errorf("oidc id token nonce mismatch")
	}

	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("extracting claims: %w", err)
	}

	// Reject unverified emails when using email as the username claim.
	// Identity-spoofing prevention — same rule as HandleCallback.
	if p.Config.UsernameClaim == "email" && claims.Email != "" && !claims.EmailVerified {
		return nil, fmt.Errorf("email address not verified by identity provider")
	}

	groups := p.extractGroups(idToken)

	user := p.mapClaimsToUser(&claims, groups, idToken.Subject)
	if user == nil {
		return nil, fmt.Errorf("failed to map OIDC claims to user")
	}

	if len(p.Config.AllowedDomains) > 0 && !p.isAllowedDomain(claims.Email) {
		return nil, fmt.Errorf("email domain not allowed")
	}

	return user, nil
}
