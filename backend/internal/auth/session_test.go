package auth

import (
	"errors"
	"sync"
	"sync/atomic"
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

	result, err := store.Validate("token-abc")
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if result.UserID != "user-1" {
		t.Errorf("expected user-1, got %s", result.UserID)
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

func TestSessionStore_CachedUser(t *testing.T) {
	store := NewSessionStore()

	cachedUser := &User{
		ID:                 "oidc:google:sub-123",
		Username:           "alice@example.com",
		Provider:           "oidc",
		KubernetesUsername: "alice@example.com",
		KubernetesGroups:   []string{"k8scenter:users", "oidc:devs"},
	}

	store.Store(RefreshSession{
		Token:      "oidc-token",
		UserID:     "oidc:google:sub-123",
		Provider:   "oidc",
		ExpiresAt:  time.Now().Add(time.Hour),
		CachedUser: cachedUser,
	})

	result, err := store.Validate("oidc-token")
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if result.CachedUser == nil {
		t.Fatal("expected CachedUser to be non-nil for OIDC session")
	}
	if result.CachedUser.Username != "alice@example.com" {
		t.Errorf("CachedUser.Username = %q, want %q", result.CachedUser.Username, "alice@example.com")
	}
}

// ---------------------------------------------------------------------
// Issue #274 — Peek / Consume split tests.
// ---------------------------------------------------------------------

// TestSessionStore_Peek_DoesNotConsume — the core fix. Peek returns the
// session WITHOUT deleting it, so a subsequent failure (token-mint, user
// lookup, etc.) doesn't silently destroy the refresh credential.
func TestSessionStore_Peek_DoesNotConsume(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-peek",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	first, err := store.Peek("token-peek")
	if err != nil {
		t.Fatalf("first Peek failed: %v", err)
	}
	if first.UserID != "user-1" {
		t.Errorf("first Peek UserID = %q, want %q", first.UserID, "user-1")
	}

	// Second Peek must still succeed — Peek is idempotent. Without this
	// property, a transient post-Peek failure followed by a retry would
	// surface as "refresh token not found" to the client.
	second, err := store.Peek("token-peek")
	if err != nil {
		t.Fatalf("second Peek failed (expected idempotent): %v", err)
	}
	if second.UserID != "user-1" {
		t.Errorf("second Peek UserID = %q, want %q", second.UserID, "user-1")
	}
}

// TestSessionStore_Peek_ExpiredPurges — expired tokens get cleaned up
// immediately, matching Validate's behavior. Surfaces as "token expired"
// to the caller AND removes the row so it doesn't linger until the
// background cleanup tick.
func TestSessionStore_Peek_ExpiredPurges(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-expired",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	if _, err := store.Peek("token-expired"); err == nil {
		t.Fatal("expected expired-token error from Peek")
	}
	// Should also be gone from the store now.
	if _, err := store.Peek("token-expired"); err == nil {
		t.Fatal("expired token should have been deleted on first Peek")
	}
}

// TestSessionStore_Consume_DeletesOnce — Consume atomically removes the
// row; a second Consume of the same token returns ErrSessionNotFound.
func TestSessionStore_Consume_DeletesOnce(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-consume",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	if err := store.Consume("token-consume"); err != nil {
		t.Fatalf("first Consume failed: %v", err)
	}
	if err := store.Consume("token-consume"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("second Consume: expected ErrSessionNotFound, got %v", err)
	}
}

// TestSessionStore_Consume_RaceOnlyOneWins — the LoadAndDelete
// underneath Consume is atomic per sync.Map, so concurrent Consume
// calls for the same token are mutually exclusive. Pins this property
// so a future refactor that splits the operation can't silently
// reintroduce a double-consume bug.
func TestSessionStore_Consume_RaceOnlyOneWins(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-race",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	const N = 32
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			if err := store.Consume("token-race"); err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("expected exactly 1 successful Consume across %d goroutines, got %d", N, got)
	}
}

// TestSessionStore_PeekConsume_RefreshFailurePreservesSession — the
// load-bearing integration test for #274. Simulates the handleRefresh
// flow when post-Peek work fails: Peek succeeds, work fails, no Consume
// → original token is still valid for client retry.
func TestSessionStore_PeekConsume_RefreshFailurePreservesSession(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-survive",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Pretend handleRefresh: Peek succeeds.
	session, err := store.Peek("token-survive")
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if session.UserID != "user-1" {
		t.Fatalf("Peek UserID = %q", session.UserID)
	}

	// Pretend issueTokenPair errors. handleRefresh returns 500 without
	// calling Consume. The session must remain valid for retry.
	// (We do not call Consume here.)

	// Client retries with the same refresh token. Peek must still
	// succeed against the same row.
	retry, err := store.Peek("token-survive")
	if err != nil {
		t.Fatalf("retry Peek failed (regression of #274): %v", err)
	}
	if retry.UserID != "user-1" {
		t.Fatalf("retry UserID = %q", retry.UserID)
	}

	// Now the retry succeeds at issueTokenPair time. handleRefresh
	// calls Consume; the original token is finally removed.
	if err := store.Consume("token-survive"); err != nil {
		t.Fatalf("Consume on successful retry failed: %v", err)
	}
	if _, err := store.Peek("token-survive"); err == nil {
		t.Fatal("after Consume, token should be unusable")
	}
}
