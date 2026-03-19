package auth

import (
	"context"
	"errors"
)

// User represents an authenticated user with Kubernetes identity mappings.
type User struct {
	ID                 string   `json:"id"`
	Username           string   `json:"username"`
	Provider           string   `json:"provider"` // "local", "oidc", "ldap"
	KubernetesUsername string   `json:"kubernetesUsername"`
	KubernetesGroups   []string `json:"kubernetesGroups"`
	Roles              []string `json:"roles"`
}

// Credentials holds login credentials for authentication.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Provider string `json:"provider,omitempty"` // optional: "local" (default), "ldap", or LDAP provider ID
}

// AuthProvider is the interface for credential-based authentication backends (local, LDAP).
type AuthProvider interface {
	// Authenticate validates credentials and returns the authenticated user.
	Authenticate(ctx context.Context, credentials Credentials) (*User, error)
	// Type returns the provider type identifier (e.g., "local", "ldap").
	Type() string
}

// UserLookup is implemented by providers that can look up users by ID (for token refresh).
type UserLookup interface {
	GetUserByID(id string) (*User, error)
}

// ProviderInfo describes a configured auth provider for the login page.
type ProviderInfo struct {
	ID          string `json:"id"`
	Type        string `json:"type"`        // "local", "oidc", "ldap"
	DisplayName string `json:"displayName"`
	LoginURL    string `json:"loginURL,omitempty"` // only for OIDC (redirect-based)
}

// Sentinel errors for the auth package. Use errors.Is() for comparison.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
	ErrDuplicateUser      = errors.New("user already exists")
	ErrSetupCompleted     = errors.New("setup already completed")
	ErrPasswordInvalid    = errors.New("password must be 8-128 characters")
	ErrLastAdmin          = errors.New("cannot delete the last admin user")
	ErrSelfDelete         = errors.New("cannot delete your own account")
)

// contextKey is an unexported type for context keys to prevent collisions.
type contextKey int

const userContextKey contextKey = 0

// UserFromContext extracts the authenticated user from the request context.
func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey).(*User)
	return u, ok
}

// ContextWithUser returns a new context carrying the authenticated user.
func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}
