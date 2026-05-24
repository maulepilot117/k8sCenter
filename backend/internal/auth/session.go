package auth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrSessionNotFound is returned by [SessionStore.Rotate] when the
// referenced refresh token row no longer exists — either because a
// concurrent caller already rotated it or because cleanup removed an
// expired entry before this call landed.
var ErrSessionNotFound = errors.New("session not found")

// ErrSessionExpired is returned by [SessionStore.Rotate] when the
// referenced token's ExpiresAt is in the past. The row is removed by
// Rotate regardless (an expired token has no value going forward).
var ErrSessionExpired = errors.New("session expired")

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

// Rotate atomically removes a refresh token from the store and returns its
// session payload. This is the single-step primitive that backs refresh-
// token rotation: callers receive the session and may then mint a successor
// pair. Returns [ErrSessionNotFound] when the row is missing (concurrent
// rotation winner, explicit Revoke, or cleanup eviction) and
// [ErrSessionExpired] when the row existed but had expired (the row is
// removed in both cases so it can't be replayed).
//
// Concurrency contract: Rotate uses sync.Map.LoadAndDelete, which is
// atomic. Two concurrent Rotate calls for the same token therefore can
// never both succeed — exactly one returns the session, every other
// caller observes ErrSessionNotFound. This closes the race documented in
// audit finding P2-2 (2026-05-22), where the legacy Peek+Consume split
// allowed two callers to each mint a valid successor pair from a single
// stolen refresh token.
//
// Failure handling: if the caller's post-rotate work (issueTokenPair,
// LDAP revalidation, etc.) fails, call [Restore] with the returned
// session to put it back so the client can retry. The returned
// *RefreshSession is opaque metadata for that purpose; do not mutate it
// between Rotate and Restore.
func (s *SessionStore) Rotate(token string) (*RefreshSession, error) {
	val, ok := s.sessions.LoadAndDelete(token)
	if !ok {
		return nil, ErrSessionNotFound
	}
	session := val.(RefreshSession)
	if time.Now().After(session.ExpiresAt) {
		return nil, ErrSessionExpired
	}
	return &session, nil
}

// Restore re-stores a session previously removed by [Rotate]. Used by the
// refresh handler when the post-rotate work fails (transient signing
// error, IdP revalidation timeout) — putting the session back lets the
// client retry with the same refresh token rather than being silently
// logged out. The intent matches the original issue #274 fix, but the
// surrounding primitive is now atomic (no double-mint race).
//
// Safe to call after Rotate succeeded. No-ops are intentional: if the
// caller passes a stale or zero-value session, Store overwrites whatever
// is there — and that pathological mis-use is already a programming
// error the type system can't prevent.
func (s *SessionStore) Restore(session *RefreshSession) {
	if session == nil || session.Token == "" {
		return
	}
	s.sessions.Store(session.Token, *session)
}

// Revoke deletes a refresh token (e.g., on logout).
func (s *SessionStore) Revoke(token string) {
	s.sessions.Delete(token)
}

// RangeSessions iterates stored sessions, invoking fn for each. fn returns
// false to stop iteration. Exposed for tests that need to inspect stored
// expiry values without consuming the token via Rotate (which is single-
// use and would also strip the row). Production callers should prefer
// Rotate / Revoke.
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

