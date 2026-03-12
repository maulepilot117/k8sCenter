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

// Validate checks if a refresh token is valid (exists and not expired).
// If valid, it deletes the token (rotation — single use).
// Returns the associated user ID.
func (s *SessionStore) Validate(token string) (string, error) {
	val, ok := s.sessions.Load(token)
	if !ok {
		return "", fmt.Errorf("refresh token not found")
	}

	session := val.(RefreshSession)

	// Always delete — token is single-use regardless of validity
	s.sessions.Delete(token)

	if time.Now().After(session.ExpiresAt) {
		return "", fmt.Errorf("refresh token expired")
	}

	return session.UserID, nil
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
