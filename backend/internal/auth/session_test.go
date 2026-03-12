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

func TestSessionStore_RevokeAllForUser(t *testing.T) {
	store := NewSessionStore()

	store.Store(RefreshSession{Token: "t1", UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour)})
	store.Store(RefreshSession{Token: "t2", UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour)})
	store.Store(RefreshSession{Token: "t3", UserID: "user-2", ExpiresAt: time.Now().Add(time.Hour)})

	store.RevokeAllForUser("user-1")

	// user-1 tokens should be gone
	_, err := store.Validate("t1")
	if err == nil {
		t.Fatal("expected t1 revoked")
	}
	_, err = store.Validate("t2")
	if err == nil {
		t.Fatal("expected t2 revoked")
	}

	// user-2 token should still work
	userID, err := store.Validate("t3")
	if err != nil {
		t.Fatalf("t3 should still be valid: %v", err)
	}
	if userID != "user-2" {
		t.Errorf("expected user-2, got %s", userID)
	}
}
