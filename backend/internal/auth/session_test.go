package auth

import (
	"testing"
	"time"
)

func TestSessionStore_StoreAndValidate(t *testing.T) {
	store := NewSessionStore()

	store.Store(RefreshSession{
		Token:     "token-abc",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	userID, err := store.Validate("token-abc")
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if userID != "user-1" {
		t.Errorf("expected user-1, got %s", userID)
	}
}

func TestSessionStore_SingleUse(t *testing.T) {
	store := NewSessionStore()

	store.Store(RefreshSession{
		Token:     "token-abc",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// First use succeeds
	_, err := store.Validate("token-abc")
	if err != nil {
		t.Fatalf("first Validate failed: %v", err)
	}

	// Second use fails (rotation)
	_, err = store.Validate("token-abc")
	if err == nil {
		t.Fatal("expected error on second use of refresh token")
	}
}

func TestSessionStore_ExpiredToken(t *testing.T) {
	store := NewSessionStore()

	store.Store(RefreshSession{
		Token:     "expired-token",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	_, err := store.Validate("expired-token")
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestSessionStore_UnknownToken(t *testing.T) {
	store := NewSessionStore()

	_, err := store.Validate("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
}

func TestSessionStore_Revoke(t *testing.T) {
	store := NewSessionStore()

	store.Store(RefreshSession{
		Token:     "token-to-revoke",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	store.Revoke("token-to-revoke")

	_, err := store.Validate("token-to-revoke")
	if err == nil {
		t.Fatal("expected error after revocation")
	}
}

