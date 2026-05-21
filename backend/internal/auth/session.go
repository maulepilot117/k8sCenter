package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrSessionNotFound is returned by [SessionStore.Consume] when the
// referenced refresh token row no longer exists — either because a
// concurrent caller already consumed it or because cleanup removed an
// expired entry between Peek and Consume. Callers should treat this as
// non-fatal: the rotation has effectively already happened.
var ErrSessionNotFound = errors.New("session not found")

// RefreshSession holds a refresh token and its associated user/expiry.
type RefreshSession struct {
	Token     string
	UserID    string
	Provider  string // auth provider that created this session
	ExpiresAt time.Time
	// CachedUser stores the full user for providers without a local store (OIDC).
	// For local and LDAP providers, this is nil and the user is looked up by ID.
	CachedUser *User
}

// SessionStore manages server-side refresh token storage.
// Tokens are stored in memory with a sync.Map for concurrent access.
// In production, this could be backed by k8s Secrets or SQLite.
type SessionStore struct {
	sessions sync.Map // map[token]RefreshSession
}

// NewSessionStore creates a new in-memory session store.
func NewSessionStore() *SessionStore {
	return &SessionStore{}
}

// Store saves a refresh session. The token itself is the lookup key.
func (s *SessionStore) Store(session RefreshSession) {
	s.sessions.Store(session.Token, session)
}

// ValidatedSession is the result of a successful refresh token validation.
type ValidatedSession struct {
	UserID     string
	Provider   string
	CachedUser *User // non-nil for OIDC users
}

// Validate checks if a refresh token is valid (exists and not expired).
// If valid, it deletes the token (rotation — single use).
// Returns the session data needed for token refresh.
//
// Note: this is the legacy single-call API. Callers that can fail
// between validation and consumption (e.g., the refresh handler, where
// issueTokenPair may error after Validate succeeds) should use
// [Peek] + [Consume] instead — see issue #274. Validate remains for
// flows where the consumption is unconditional.
func (s *SessionStore) Validate(token string) (*ValidatedSession, error) {
	val, ok := s.sessions.Load(token)
	if !ok {
		return nil, fmt.Errorf("refresh token not found")
	}

	session := val.(RefreshSession)

	// Always delete — token is single-use regardless of validity
	s.sessions.Delete(token)

	if time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("refresh token expired")
	}

	return &ValidatedSession{
		UserID:     session.UserID,
		Provider:   session.Provider,
		CachedUser: session.CachedUser,
	}, nil
}

// Peek validates a refresh token WITHOUT consuming it. Returns the
// session payload if the token exists and is not expired. Expired
// tokens ARE deleted (no value in keeping them), but valid tokens
// remain in the store — callers MUST follow up with [Consume] on
// success to enforce single-use rotation.
//
// Issue #274 — separating Peek from Consume closes the race window
// where [Validate] would delete a session BEFORE the caller had
// successfully minted a replacement. If the post-Peek work (user
// lookup, token minting) fails, the caller skips Consume and the
// session remains valid for client retry. Without this split, a
// transient backend failure between Validate and issueTokenPair
// silently logs the user out.
//
// Concurrency: Peek + Consume is NOT atomic. Two callers can Peek the
// same token simultaneously and both proceed. The first Consume wins;
// the second returns [ErrSessionNotFound] (informational — the new
// pair has already been minted in both branches).
func (s *SessionStore) Peek(token string) (*ValidatedSession, error) {
	val, ok := s.sessions.Load(token)
	if !ok {
		return nil, fmt.Errorf("refresh token not found")
	}

	session := val.(RefreshSession)

	if time.Now().After(session.ExpiresAt) {
		// Expired — purge so it doesn't linger until the next cleanup
		// tick. Mirrors Validate's behavior on expired tokens.
		s.sessions.Delete(token)
		return nil, fmt.Errorf("refresh token expired")
	}

	return &ValidatedSession{
		UserID:     session.UserID,
		Provider:   session.Provider,
		CachedUser: session.CachedUser,
	}, nil
}

// Consume atomically deletes a refresh token. Used by callers that have
// finished the post-[Peek] work and need to enforce single-use
// rotation. Returns [ErrSessionNotFound] if the row is already gone
// (concurrent Peek+Consume race, cleanup race, or explicit Revoke);
// callers should treat that as non-fatal — the rotation has effectively
// already happened on the other path.
func (s *SessionStore) Consume(token string) error {
	if _, ok := s.sessions.LoadAndDelete(token); !ok {
		return ErrSessionNotFound
	}
	return nil
}

// Revoke deletes a refresh token (e.g., on logout).
func (s *SessionStore) Revoke(token string) {
	s.sessions.Delete(token)
}

// RangeSessions iterates stored sessions, invoking fn for each. fn returns
// false to stop iteration. Exposed for tests that need to inspect stored
// expiry values without consuming the token via Validate (which is single-
// use and would also strip the row). Production callers should prefer
// Validate / Revoke.
func (s *SessionStore) RangeSessions(fn func(RefreshSession) bool) {
	s.sessions.Range(func(_, val any) bool {
		return fn(val.(RefreshSession))
	})
}

// StartCleanup runs a background goroutine to evict expired sessions.
func (s *SessionStore) StartCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				s.sessions.Range(func(key, val any) bool {
					session := val.(RefreshSession)
					if now.After(session.ExpiresAt) {
						s.sessions.Delete(key)
					}
					return true
				})
			}
		}
	}()
}
