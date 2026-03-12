package auth

import (
	"testing"
	"time"
)

func TestIssueAndValidateAccessToken(t *testing.T) {
	tm := NewTokenManager([]byte("test-secret-key-32-bytes-long!!"))

	user := &User{
		ID:                 "user-1",
		Username:           "admin",
		KubernetesUsername: "admin",
		KubernetesGroups:   []string{"system:masters"},
		Roles:              []string{"admin"},
	}

	token, err := tm.IssueAccessToken(user)
	if err != nil {
		t.Fatalf("IssueAccessToken failed: %v", err)
	}

	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := tm.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken failed: %v", err)
	}

	if claims.Subject != "user-1" {
		t.Errorf("expected subject user-1, got %s", claims.Subject)
	}
	if claims.Username != "admin" {
		t.Errorf("expected username admin, got %s", claims.Username)
	}
	if claims.KubernetesUsername != "admin" {
		t.Errorf("expected k8s username admin, got %s", claims.KubernetesUsername)
	}
	if len(claims.KubernetesGroups) != 1 || claims.KubernetesGroups[0] != "system:masters" {
		t.Errorf("unexpected k8s groups: %v", claims.KubernetesGroups)
	}
}

func TestValidateAccessToken_InvalidSignature(t *testing.T) {
	tm1 := NewTokenManager([]byte("key-one-is-32-bytes-long-here!!"))
	tm2 := NewTokenManager([]byte("key-two-is-32-bytes-long-here!!"))

	user := &User{ID: "u1", Username: "test"}
	token, err := tm1.IssueAccessToken(user)
	if err != nil {
		t.Fatalf("IssueAccessToken failed: %v", err)
	}

	_, err = tm2.ValidateAccessToken(token)
	if err == nil {
		t.Fatal("expected error validating token with wrong key")
	}
}

func TestValidateAccessToken_EmptyToken(t *testing.T) {
	tm := NewTokenManager([]byte("test-secret-key-32-bytes-long!!"))
	_, err := tm.ValidateAccessToken("")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestValidateAccessToken_GarbageToken(t *testing.T) {
	tm := NewTokenManager([]byte("test-secret-key-32-bytes-long!!"))
	_, err := tm.ValidateAccessToken("not.a.valid.jwt")
	if err == nil {
		t.Fatal("expected error for garbage token")
	}
}

func TestUserFromClaims(t *testing.T) {
	claims := &TokenClaims{
		Username:           "chris",
		KubernetesUsername: "chris@example.com",
		KubernetesGroups:   []string{"developers"},
		Roles:              []string{"viewer"},
	}
	claims.Subject = "user-42"

	user := UserFromClaims(claims)
	if user.ID != "user-42" {
		t.Errorf("expected ID user-42, got %s", user.ID)
	}
	if user.Username != "chris" {
		t.Errorf("expected username chris, got %s", user.Username)
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	token1, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken failed: %v", err)
	}
	token2, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken failed: %v", err)
	}

	if token1 == token2 {
		t.Error("expected unique refresh tokens")
	}
	if len(token1) != RefreshTokenBytes*2 { // hex encoded
		t.Errorf("expected token length %d, got %d", RefreshTokenBytes*2, len(token1))
	}
}

func TestAccessTokenLifetime(t *testing.T) {
	if AccessTokenLifetime != 15*time.Minute {
		t.Errorf("expected 15 minute lifetime, got %v", AccessTokenLifetime)
	}
}

func TestRefreshTokenLifetime(t *testing.T) {
	if RefreshTokenLifetime != 7*24*time.Hour {
		t.Errorf("expected 7 day lifetime, got %v", RefreshTokenLifetime)
	}
}
