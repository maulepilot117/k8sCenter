package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AccessTokenLifetime is the duration an access token is valid.
	AccessTokenLifetime = 15 * time.Minute
	// RefreshTokenLifetime is the duration a refresh token is valid for
	// local + LDAP providers.
	RefreshTokenLifetime = 7 * 24 * time.Hour
	// OIDCRefreshTokenLifetime caps refresh tokens issued for OIDC-sourced
	// sessions. The shorter window means IdP-side revocation (account
	// disabled, group removed) takes effect within the hour rather than
	// waiting for the full 7-day rotation cycle.
	//
	// The alternative — re-validating each refresh against the IdP's
	// revocation_endpoint — would require persisting the IdP refresh token,
	// add per-refresh IdP latency, and tie the platform's availability to
	// the IdP's. The 1h cap is a pragmatic middle ground: documented as the
	// session-lifetime guarantee for OIDC users.
	OIDCRefreshTokenLifetime = 1 * time.Hour
	// RefreshTokenBytes is the length of random refresh tokens.
	RefreshTokenBytes = 32
)

// RefreshLifetimeFor returns the refresh token TTL for a given auth
// provider. OIDC sessions get the shorter cap; everything else gets the
// standard window.
func RefreshLifetimeFor(provider string) time.Duration {
	if provider == "oidc" {
		return OIDCRefreshTokenLifetime
	}
	return RefreshTokenLifetime
}

// TokenClaims are the JWT claims for access tokens.
type TokenClaims struct {
	jwt.RegisteredClaims
	Username           string   `json:"username"`
	Provider           string   `json:"provider"`
	KubernetesUsername string   `json:"kubernetesUsername"`
	KubernetesGroups   []string `json:"kubernetesGroups"`
	Roles              []string `json:"roles"`
}

// TokenManager handles JWT creation and validation.
type TokenManager struct {
	signingKey []byte
	issuer     string
}

// NewTokenManager creates a TokenManager with the given HMAC-SHA256 signing key.
func NewTokenManager(signingKey []byte) *TokenManager {
	return &TokenManager{
		signingKey: signingKey,
		issuer:     "kubecenter",
	}
}

// generateJTI creates a cryptographically random JWT ID (16 bytes, hex-encoded).
func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating jti: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// IssueAccessToken creates a signed JWT access token for the given user.
func (tm *TokenManager) IssueAccessToken(user *User) (string, error) {
	jti, err := generateJTI()
	if err != nil {
		return "", err
	}

	now := time.Now()
	claims := TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   user.ID,
			Issuer:    tm.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenLifetime)),
		},
		Username:           user.Username,
		Provider:           user.Provider,
		KubernetesUsername: user.KubernetesUsername,
		KubernetesGroups:   user.KubernetesGroups,
		Roles:              user.Roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(tm.signingKey)
	if err != nil {
		return "", fmt.Errorf("signing access token: %w", err)
	}
	return signed, nil
}

// ValidateAccessToken parses and validates a JWT access token, returning the claims.
func (tm *TokenManager) ValidateAccessToken(tokenString string) (*TokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return tm.signingKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}

	claims, ok := token.Claims.(*TokenClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}

// UserFromClaims reconstructs a User from validated JWT claims.
func UserFromClaims(claims *TokenClaims) *User {
	return &User{
		ID:                 claims.Subject,
		Username:           claims.Username,
		Provider:           claims.Provider,
		KubernetesUsername: claims.KubernetesUsername,
		KubernetesGroups:   claims.KubernetesGroups,
		Roles:              claims.Roles,
	}
}

// GenerateRefreshToken creates a cryptographically random refresh token.
func GenerateRefreshToken() (string, error) {
	b := make([]byte, RefreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating refresh token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
