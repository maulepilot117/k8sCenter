package auth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

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

// Revoke deletes a refresh token (e.g., on logout).
func (s *SessionStore) Revoke(token string) {
	s.sessions.Delete(token)
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
