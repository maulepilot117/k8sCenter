package auth

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionStore_StoreAndRotate(t *testing.T) {
	store := NewSessionStore()

	store.Store(RefreshSession{
		Token:     "token-abc",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	session, err := store.Rotate("token-abc")
	if err != nil {
		t.Fatalf("Rotate failed: %v", err)
	}
	if session.UserID != "user-1" {
		t.Errorf("expected user-1, got %s", session.UserID)
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
	if _, err := store.Rotate("token-abc"); err != nil {
		t.Fatalf("first Rotate failed: %v", err)
	}

	// Second use fails — the atomic LoadAndDelete already removed the row.
	if _, err := store.Rotate("token-abc"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound on second Rotate, got %v", err)
	}
}

func TestSessionStore_ExpiredToken(t *testing.T) {
	store := NewSessionStore()

	store.Store(RefreshSession{
		Token:     "expired-token",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	if _, err := store.Rotate("expired-token"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
	// Expired row should also be gone now.
	if _, err := store.Rotate("expired-token"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired token should have been deleted on first Rotate, got %v", err)
	}
}

func TestSessionStore_UnknownToken(t *testing.T) {
	store := NewSessionStore()

	if _, err := store.Rotate("nonexistent"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for unknown token, got %v", err)
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

	if _, err := store.Rotate("token-to-revoke"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound after Revoke, got %v", err)
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

	session, err := store.Rotate("oidc-token")
	if err != nil {
		t.Fatalf("Rotate failed: %v", err)
	}
	if session.CachedUser == nil {
		t.Fatal("expected CachedUser to be non-nil for OIDC session")
	}
	if session.CachedUser.Username != "alice@example.com" {
		t.Errorf("CachedUser.Username = %q, want %q", session.CachedUser.Username, "alice@example.com")
	}
}

// ---------------------------------------------------------------------
// Audit finding P2-2 (2026-05-22) — atomic rotation tests.
// ---------------------------------------------------------------------

// TestSessionStore_Rotate_RaceOnlyOneWins pins the property that backs the
// P2-2 fix: N concurrent goroutines racing on the same token must observe
// exactly one success. The legacy Peek+Consume split allowed all N
// Peeks to succeed, which in handleRefresh would mint N successor pairs
// from a single stolen refresh token. LoadAndDelete is atomic per
// sync.Map, so this property holds — a future refactor that splits the
// operation can't silently reintroduce a double-mint bug without failing
// this test.
func TestSessionStore_Rotate_RaceOnlyOneWins(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-race",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	const N = 64
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			if _, err := store.Rotate("token-race"); err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("expected exactly 1 successful Rotate across %d goroutines, got %d", N, got)
	}
}

// TestSessionStore_Rotate_RaceWithRevoke ensures Rotate and Revoke can't
// both succeed on the same token. The combined race (one refresher +
// one logout) must end with exactly one observable side effect: either
// the refresh mints a successor (Rotate won) OR the session is gone
// with no successor (Revoke won). The handler treats Rotate's
// ErrSessionNotFound as "logged out by another path" — this test pins
// that contract.
func TestSessionStore_Rotate_RaceWithRevoke(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-revoke-race",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	var rotateOK atomic.Bool
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		if _, err := store.Rotate("token-revoke-race"); err == nil {
			rotateOK.Store(true)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		store.Revoke("token-revoke-race")
	}()
	close(start)
	wg.Wait()

	// Whatever happened, the token must be gone afterward.
	if _, err := store.Rotate("token-revoke-race"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session should be removed after Rotate/Revoke race, got %v", err)
	}
	// rotateOK is informational — the test passes whether Rotate or
	// Revoke won, so long as the post-race state is consistent (single
	// observable removal).
	t.Logf("Rotate won race: %v", rotateOK.Load())
}

// TestSessionStore_Rotate_FailurePreservesSession is the load-bearing
// integration test for the issue #274 fix re-anchored on the new atomic
// API: Rotate removes the session, then post-rotate work errors, then
// Restore puts it back so the client can retry without being logged
// out. Without Restore, a transient signing failure would silently
// invalidate the user's refresh credential.
func TestSessionStore_Rotate_FailurePreservesSession(t *testing.T) {
	store := NewSessionStore()
	store.Store(RefreshSession{
		Token:     "token-survive",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// Pretend handleRefresh: Rotate succeeds.
	session, err := store.Rotate("token-survive")
	if err != nil {
		t.Fatalf("Rotate failed: %v", err)
	}
	if session.UserID != "user-1" {
		t.Fatalf("Rotate UserID = %q", session.UserID)
	}

	// Pretend issueTokenPair errors. handleRefresh calls Restore.
	store.Restore(session)

	// Client retries with the same refresh token. Rotate must still
	// succeed against the restored row.
	retry, err := store.Rotate("token-survive")
	if err != nil {
		t.Fatalf("retry Rotate after Restore failed: %v", err)
	}
	if retry.UserID != "user-1" {
		t.Fatalf("retry UserID = %q", retry.UserID)
	}

	// The session is gone after the successful retry — no second Restore call.
	if _, err := store.Rotate("token-survive"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("after successful retry, token should be unusable, got %v", err)
	}
}

// TestSessionStore_Restore_NilSafe — Restore must tolerate a nil pointer
// (defensive: a handler that branches on error might call Restore on a
// nil session by mistake; the type system can't prevent it). Zero-token
// is treated as "nothing to restore" — silent no-op.
func TestSessionStore_Restore_NilSafe(t *testing.T) {
	store := NewSessionStore()
	store.Restore(nil)
	store.Restore(&RefreshSession{}) // empty Token
	// No assertion needed — the test passes if neither call panics.
}
