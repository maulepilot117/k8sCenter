package auth

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/kubecenter/kubecenter/internal/config"
	"golang.org/x/oauth2"
)

// OIDCProviderConfig is an alias for config.OIDCConfig.
// Kept as a type alias for backward compatibility with existing code
// that references auth.OIDCProviderConfig.
type OIDCProviderConfig = config.OIDCConfig

// OIDCProvider wraps the go-oidc provider and oauth2 config for a single OIDC identity provider.
type OIDCProvider struct {
	Config       OIDCProviderConfig
	provider     *oidc.Provider
	oauth2Config oauth2.Config
	verifier     *oidc.IDTokenVerifier
	httpClient   *http.Client // custom TLS client for token exchange
	stateStore   *OIDCStateStore
	logger       *slog.Logger
}

// oidcClaims captures common OIDC claims for user mapping.
type oidcClaims struct {
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Subject           string `json:"sub"`
	// Groups is extracted separately because it can be string or []string.
}

// NewOIDCProvider creates an OIDC provider by performing discovery against the issuer URL.
func NewOIDCProvider(ctx context.Context, config OIDCProviderConfig, stateStore *OIDCStateStore, logger *slog.Logger) (*OIDCProvider, error) {
	if config.IssuerURL == "" {
		return nil, fmt.Errorf("OIDC provider %q: issuerURL is required", config.ID)
	}
	if config.ClientID == "" {
		return nil, fmt.Errorf("OIDC provider %q: clientID is required", config.ID)
	}
	if config.RedirectURL == "" {
		return nil, fmt.Errorf("OIDC provider %q: redirectURL is required", config.ID)
	}

	// Apply defaults
	if config.UsernameClaim == "" {
		config.UsernameClaim = "email"
	}
	if config.GroupsClaim == "" {
		config.GroupsClaim = "groups"
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}

	// Build HTTP client with optional custom TLS
	httpClient, err := buildOIDCHTTPClient(config)
	if err != nil {
		return nil, fmt.Errorf("OIDC provider %q: building HTTP client: %w", config.ID, err)
	}

	// Use custom HTTP client for OIDC discovery
	discoveryCtx := oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(discoveryCtx, config.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC provider %q: discovery failed: %w", config.ID, err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID:             config.ClientID,
		SupportedSigningAlgs: []string{"RS256", "ES256"},
	})

	oauth2Config := oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  config.RedirectURL,
		Scopes:       config.Scopes,
	}

	return &OIDCProvider{
		Config:       config,
		provider:     provider,
		oauth2Config: oauth2Config,
		verifier:     verifier,
		httpClient:   httpClient,
		stateStore:   stateStore,
		logger:       logger.With("oidc_provider", config.ID),
	}, nil
}

// LoginRedirect generates OIDC authorization URL with state, nonce, and PKCE.
// The caller should redirect the user's browser to the returned URL.
func (p *OIDCProvider) LoginRedirect() (redirectURL string, err error) {
	// Generate state parameter (CSRF protection)
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	// Generate nonce (ID token anti-replay)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	// Generate PKCE verifier
	verifier := oauth2.GenerateVerifier()

	// Store flow state server-side
	p.stateStore.Store(OIDCFlowState{
		State:        state,
		Nonce:        nonce,
		PKCEVerifier: verifier,
		ProviderID:   p.Config.ID,
		CreatedAt:    time.Now(),
	})

	// Build authorization URL with PKCE + nonce
	url := p.oauth2Config.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oidc.Nonce(nonce),
	)

	return url, nil
}

// HandleCallback exchanges the authorization code for tokens, verifies the ID token,
// and maps claims to a k8sCenter User.
func (p *OIDCProvider) HandleCallback(ctx context.Context, code string, flowState *OIDCFlowState) (*User, error) {
	// Inject the custom HTTP client (with TLS config) into the context for token exchange.
	// Without this, Exchange falls back to http.DefaultClient, ignoring CACertPath/TLSInsecure.
	exchangeCtx := oidc.ClientContext(ctx, p.httpClient)

	// Exchange code for tokens with PKCE verifier
	oauth2Token, err := p.oauth2Config.Exchange(exchangeCtx, code, oauth2.VerifierOption(flowState.PKCEVerifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Extract ID token from OAuth2 token response
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token in token response")
	}

	// Verify ID token (signature, issuer, audience, expiry)
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("ID token verification failed: %w", err)
	}

	// Validate nonce (go-oidc does NOT validate this automatically)
	if idToken.Nonce != flowState.Nonce {
		return nil, fmt.Errorf("ID token nonce mismatch")
	}

	// Extract claims
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("extracting claims: %w", err)
	}

	// Reject unverified emails when using email as the username claim (identity spoofing prevention)
	if p.Config.UsernameClaim == "email" && claims.Email != "" && !claims.EmailVerified {
		return nil, fmt.Errorf("email address not verified by identity provider")
	}

	// Extract groups claim (can be string or []string depending on provider)
	groups := p.extractGroups(idToken)

	// Map claims to k8sCenter user
	user := p.mapClaimsToUser(&claims, groups, idToken.Subject)
	if user == nil {
		return nil, fmt.Errorf("failed to map OIDC claims to user")
	}

	// Validate email domain if configured
	if len(p.Config.AllowedDomains) > 0 {
		if !p.isAllowedDomain(claims.Email) {
			return nil, fmt.Errorf("email domain not allowed")
		}
	}

	return user, nil
}

// extractGroups reads the groups claim from the ID token.
// Handles both string and []string formats (provider inconsistency per k8s issue #33290).
func (p *OIDCProvider) extractGroups(idToken *oidc.IDToken) []string {
	// Extract raw claims map to handle dynamic types
	var rawClaims map[string]any
	if err := idToken.Claims(&rawClaims); err != nil {
		p.logger.Warn("failed to extract raw claims for groups", "error", err)
		return nil
	}

	groupsRaw, ok := rawClaims[p.Config.GroupsClaim]
	if !ok {
		return nil
	}

	switch v := groupsRaw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		groups := make([]string, 0, len(v))
		for _, g := range v {
			if s, ok := g.(string); ok && s != "" {
				groups = append(groups, s)
			}
		}
		return groups
	default:
		p.logger.Warn("unexpected groups claim type", "type", fmt.Sprintf("%T", groupsRaw))
		return nil
	}
}

// mapClaimsToUser builds a k8sCenter User from OIDC claims.
func (p *OIDCProvider) mapClaimsToUser(claims *oidcClaims, groups []string, subject string) *User {
	// Determine Kubernetes username from configured claim
	var k8sUsername string
	switch p.Config.UsernameClaim {
	case "email":
		k8sUsername = claims.Email
	case "preferred_username":
		k8sUsername = claims.PreferredUsername
	case "name":
		k8sUsername = claims.Name
	case "sub":
		k8sUsername = subject
	default:
		k8sUsername = claims.Email // fallback
	}

	if k8sUsername == "" {
		// Fallback chain: email → preferred_username → subject
		k8sUsername = claims.Email
		if k8sUsername == "" {
			k8sUsername = claims.PreferredUsername
		}
		if k8sUsername == "" {
			k8sUsername = subject
		}
	}

	// Apply groups prefix
	k8sGroups := make([]string, 0, len(groups)+1)
	k8sGroups = append(k8sGroups, "k8scenter:users") // all authenticated users
	for _, g := range groups {
		k8sGroups = append(k8sGroups, p.Config.GroupsPrefix+g)
	}

	// Determine display username
	displayName := claims.PreferredUsername
	if displayName == "" {
		displayName = claims.Email
	}
	if displayName == "" {
		displayName = subject
	}

	return &User{
		ID:                 fmt.Sprintf("oidc:%s:%s", p.Config.ID, subject),
		Username:           displayName,
		Provider:           "oidc",
		KubernetesUsername: k8sUsername,
		KubernetesGroups:   k8sGroups,
		Roles:              []string{"user"}, // OIDC users get "user" role by default
	}
}

// isAllowedDomain checks if the email domain is in the allowed list.
func (p *OIDCProvider) isAllowedDomain(email string) bool {
	if len(p.Config.AllowedDomains) == 0 {
		return true
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return false
	}
	domain := strings.ToLower(parts[1])
	for _, allowed := range p.Config.AllowedDomains {
		if strings.ToLower(allowed) == domain {
			return true
		}
	}
	return false
}

// buildOIDCHTTPClient creates an HTTP client with optional custom TLS configuration.
func buildOIDCHTTPClient(config OIDCProviderConfig) (*http.Client, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if config.TLSInsecure {
		tlsConfig.InsecureSkipVerify = true
	}

	if config.CACertPath != "" {
		caCert, err := os.ReadFile(config.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert from %s", config.CACertPath)
		}
		tlsConfig.RootCAs = pool
	}

	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}, nil
}
