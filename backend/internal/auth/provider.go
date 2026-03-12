package auth

import "context"

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
}

// AuthProvider is the interface for authentication backends.
type AuthProvider interface {
	// Authenticate validates credentials and returns the authenticated user.
	Authenticate(ctx context.Context, credentials Credentials) (*User, error)
	// Type returns the provider type identifier (e.g., "local", "oidc", "ldap").
	Type() string
}

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
