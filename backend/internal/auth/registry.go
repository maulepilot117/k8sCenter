package auth

import (
	"fmt"
	"sync"
)

// ProviderRegistry manages multiple authentication providers.
// It supports credential-based providers (local, LDAP) and redirect-based
// providers (OIDC). All providers are looked up by a unique ID string.
type ProviderRegistry struct {
	mu            sync.RWMutex
	credProviders map[string]AuthProvider   // "local", "ldap-corp", etc.
	oidcProviders map[string]*OIDCProvider  // "google", "keycloak", etc.
	userLookups   map[string]UserLookup     // providers that support GetUserByID
	providerOrder []ProviderInfo            // for stable ordering in ListProviders
}

// NewProviderRegistry creates an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		credProviders: make(map[string]AuthProvider),
		oidcProviders: make(map[string]*OIDCProvider),
		userLookups:   make(map[string]UserLookup),
	}
}

// RegisterCredential adds a credential-based provider (local, LDAP).
// If the provider also implements UserLookup, it is registered for refresh lookups.
func (r *ProviderRegistry) RegisterCredential(id, displayName string, provider AuthProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.credProviders[id] = provider
	if ul, ok := provider.(UserLookup); ok {
		r.userLookups[id] = ul
	}
	r.providerOrder = append(r.providerOrder, ProviderInfo{
		ID:          id,
		Type:        provider.Type(),
		DisplayName: displayName,
	})
}

// RegisterOIDC adds a redirect-based OIDC provider.
func (r *ProviderRegistry) RegisterOIDC(id string, provider *OIDCProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.oidcProviders[id] = provider
	r.providerOrder = append(r.providerOrder, ProviderInfo{
		ID:          id,
		Type:        "oidc",
		DisplayName: provider.Config.DisplayName,
		LoginURL:    fmt.Sprintf("/api/v1/auth/oidc/%s/login", id),
	})
}

// GetCredential returns a credential-based provider by ID.
func (r *ProviderRegistry) GetCredential(id string) (AuthProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.credProviders[id]
	return p, ok
}

// GetOIDC returns an OIDC provider by ID.
func (r *ProviderRegistry) GetOIDC(id string) (*OIDCProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.oidcProviders[id]
	return p, ok
}

// ListProviders returns info about all configured providers in registration order.
func (r *ProviderRegistry) ListProviders() []ProviderInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ProviderInfo, len(r.providerOrder))
	copy(result, r.providerOrder)
	return result
}

// GetUserByID looks up a user across providers for token refresh.
// For OIDC users (no local store), it returns nil — the caller should
// reconstruct the user from the JWT claims stored in the session.
func (r *ProviderRegistry) GetUserByID(providerType, userID string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Try direct provider lookup first
	if ul, ok := r.userLookups[providerType]; ok {
		return ul.GetUserByID(userID)
	}

	// For OIDC providers, there's no local user store.
	// Return a sentinel error so the caller knows to use JWT claims.
	if _, ok := r.oidcProviders[providerType]; ok {
		return nil, ErrOIDCUserLookup
	}

	// Also check if providerType is a generic type (e.g., "oidc")
	// and any OIDC provider is registered
	if providerType == "oidc" && len(r.oidcProviders) > 0 {
		return nil, ErrOIDCUserLookup
	}

	return nil, fmt.Errorf("unknown auth provider: %s", providerType)
}

// ErrOIDCUserLookup is returned when trying to look up an OIDC user by ID.
// OIDC users have no local store; the caller should reconstruct from JWT claims.
var ErrOIDCUserLookup = fmt.Errorf("OIDC users cannot be looked up by ID")
