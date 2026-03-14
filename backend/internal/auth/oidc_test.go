package auth

import (
	"testing"
	"time"
)

func TestOIDCProvider_IsAllowedDomain(t *testing.T) {
	p := &OIDCProvider{
		Config: OIDCProviderConfig{
			AllowedDomains: []string{"example.com", "Corp.NET"},
		},
	}

	tests := []struct {
		email   string
		allowed bool
	}{
		{"user@example.com", true},
		{"user@EXAMPLE.COM", true},
		{"user@corp.net", true},
		{"user@evil.com", false},
		{"invalid-email", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			if got := p.isAllowedDomain(tt.email); got != tt.allowed {
				t.Errorf("isAllowedDomain(%q) = %v, want %v", tt.email, got, tt.allowed)
			}
		})
	}
}

func TestOIDCProvider_IsAllowedDomainNoDomains(t *testing.T) {
	p := &OIDCProvider{
		Config: OIDCProviderConfig{
			AllowedDomains: nil,
		},
	}

	if !p.isAllowedDomain("anyone@anything.com") {
		t.Error("expected all domains allowed when no restrictions configured")
	}
}

func TestOIDCProvider_MapClaimsToUser(t *testing.T) {
	tests := []struct {
		name          string
		config        OIDCProviderConfig
		claims        *oidcClaims
		groups        []string
		subject       string
		wantUsername   string
		wantK8sUser   string
		wantGroupLen  int
	}{
		{
			name: "email claim",
			config: OIDCProviderConfig{
				ID:            "test",
				UsernameClaim: "email",
				GroupsPrefix:  "oidc:",
			},
			claims:       &oidcClaims{Email: "user@example.com", PreferredUsername: "testuser"},
			groups:       []string{"devs", "admins"},
			subject:      "sub-123",
			wantUsername:  "testuser",
			wantK8sUser:  "user@example.com",
			wantGroupLen: 3, // k8scenter:users + oidc:devs + oidc:admins
		},
		{
			name: "preferred_username claim",
			config: OIDCProviderConfig{
				ID:            "test",
				UsernameClaim: "preferred_username",
			},
			claims:       &oidcClaims{Email: "user@example.com", PreferredUsername: "jdoe"},
			groups:       nil,
			subject:      "sub-456",
			wantUsername:  "jdoe",
			wantK8sUser:  "jdoe",
			wantGroupLen: 1, // k8scenter:users only
		},
		{
			name: "fallback to subject when email empty",
			config: OIDCProviderConfig{
				ID:            "test",
				UsernameClaim: "email",
			},
			claims:       &oidcClaims{},
			groups:       nil,
			subject:      "sub-789",
			wantUsername:  "sub-789",
			wantK8sUser:  "sub-789",
			wantGroupLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &OIDCProvider{Config: tt.config}
			user := p.mapClaimsToUser(tt.claims, tt.groups, tt.subject)

			if user.Username != tt.wantUsername {
				t.Errorf("Username = %q, want %q", user.Username, tt.wantUsername)
			}
			if user.KubernetesUsername != tt.wantK8sUser {
				t.Errorf("KubernetesUsername = %q, want %q", user.KubernetesUsername, tt.wantK8sUser)
			}
			if len(user.KubernetesGroups) != tt.wantGroupLen {
				t.Errorf("KubernetesGroups len = %d, want %d (groups: %v)", len(user.KubernetesGroups), tt.wantGroupLen, user.KubernetesGroups)
			}
			if user.Provider != "oidc" {
				t.Errorf("Provider = %q, want %q", user.Provider, "oidc")
			}
		})
	}
}

func TestOIDCStateStore_StoreAndConsume(t *testing.T) {
	store := NewOIDCStateStore()

	store.Store(OIDCFlowState{
		State:        "test-state-123",
		Nonce:        "test-nonce",
		PKCEVerifier: "test-verifier",
		ProviderID:   "google",
		CreatedAt:    time.Now(),
	})

	got, err := store.Consume("test-state-123")
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
	if got.Nonce != "test-nonce" {
		t.Errorf("Nonce = %q, want %q", got.Nonce, "test-nonce")
	}
	if got.PKCEVerifier != "test-verifier" {
		t.Errorf("PKCEVerifier = %q, want %q", got.PKCEVerifier, "test-verifier")
	}

	// Should fail on second consume (single-use)
	_, err = store.Consume("test-state-123")
	if err == nil {
		t.Fatal("expected error on second consume (single-use)")
	}
}

func TestOIDCStateStore_UnknownState(t *testing.T) {
	store := NewOIDCStateStore()

	_, err := store.Consume("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown state")
	}
}

func TestOIDCStateStore_ExpiredState(t *testing.T) {
	store := NewOIDCStateStore()

	store.Store(OIDCFlowState{
		State:     "expired-state",
		CreatedAt: time.Now().Add(-10 * time.Minute), // well past 5-min TTL
	})

	_, err := store.Consume("expired-state")
	if err == nil {
		t.Fatal("expected error for expired state")
	}
}

func TestRegistryGetUserByID(t *testing.T) {
	registry := NewProviderRegistry()
	local := NewLocalProvider(nil)
	registry.RegisterCredential("local", "Local", local)

	// Unknown provider should error
	_, err := registry.GetUserByID("unknown", "user-1")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRegistryListProviders(t *testing.T) {
	registry := NewProviderRegistry()
	local := NewLocalProvider(nil)
	registry.RegisterCredential("local", "Local Accounts", local)

	providers := registry.ListProviders()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].ID != "local" {
		t.Errorf("provider ID = %q, want %q", providers[0].ID, "local")
	}
	if providers[0].Type != "local" {
		t.Errorf("provider Type = %q, want %q", providers[0].Type, "local")
	}
}

func TestLDAPExtractCNFromDN(t *testing.T) {
	tests := []struct {
		dn   string
		want string
	}{
		{"CN=Developers,OU=Groups,DC=corp,DC=com", "Developers"},
		{"cn=admins,dc=example,dc=com", "admins"},
		{"OU=Users,DC=corp,DC=com", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.dn, func(t *testing.T) {
			if got := extractCNFromDN(tt.dn); got != tt.want {
				t.Errorf("extractCNFromDN(%q) = %q, want %q", tt.dn, got, tt.want)
			}
		})
	}
}

func TestLDAPUsernameAllowlist(t *testing.T) {
	tests := []struct {
		username string
		valid    bool
	}{
		{"admin", true},
		{"user.name", true},
		{"user@domain.com", true},
		{"user-name_123", true},
		{"admin)(cn=*", false},  // LDAP injection attempt
		{"user*", false},
		{"user\x00name", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			if got := usernameAllowlist.MatchString(tt.username); got != tt.valid {
				t.Errorf("usernameAllowlist.MatchString(%q) = %v, want %v", tt.username, got, tt.valid)
			}
		})
	}
}
