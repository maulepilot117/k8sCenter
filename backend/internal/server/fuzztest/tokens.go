package fuzztest

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kubecenter/kubecenter/internal/auth"
)

// GarbageToken is a syntactically invalid JWT string that the Auth middleware
// must reject with 401. Use as a negative-case Authorization header value.
const GarbageToken = "not.a.valid.jwt.at.all"

// MintAccessToken mints a valid, non-expired JWT access token for the given
// user using the harness signing key. Equivalent to a successful login.
func MintAccessToken(t testing.TB, tm *auth.TokenManager, user *auth.User) string {
	t.Helper()
	tok, err := tm.IssueAccessToken(user)
	if err != nil {
		t.Fatalf("fuzztest.MintAccessToken: %v", err)
	}
	return tok
}

// MintExpiredToken mints a JWT whose ExpiresAt is in the past so the Auth
// middleware rejects it with 401. The token is otherwise well-formed and
// correctly signed.
func MintExpiredToken(t testing.TB, user *auth.User) string {
	t.Helper()

	past := time.Now().Add(-time.Hour)
	claims := auth.TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "expired-jti",
			Subject:   user.ID,
			Issuer:    "kubecenter",
			IssuedAt:  jwt.NewNumericDate(past.Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(past),
		},
		Username:           user.Username,
		Provider:           user.Provider,
		KubernetesUsername: user.KubernetesUsername,
		KubernetesGroups:   user.KubernetesGroups,
		Roles:              user.Roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(testSigningKey)
	if err != nil {
		t.Fatalf("fuzztest.MintExpiredToken: SignedString: %v", err)
	}
	return signed
}

// AdminUser returns a synthetic admin *auth.User suitable for token minting.
// It does NOT create the user in LocalAuth — call CreateAdminUser for that.
func AdminUser() *auth.User {
	return &auth.User{
		ID:                 "admin",
		Username:           "admin",
		Provider:           "local",
		KubernetesUsername: "system:serviceaccount:default:admin",
		KubernetesGroups:   []string{"system:masters"},
		Roles:              []string{"admin"},
	}
}
