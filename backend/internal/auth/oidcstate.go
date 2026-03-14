package auth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	// OIDCStateTTL is how long OIDC flow state (state, nonce, PKCE) is valid.
	OIDCStateTTL = 5 * time.Minute
)

// OIDCFlowState holds server-side state for an in-progress OIDC authorization flow.
type OIDCFlowState struct {
	State        string    // random state parameter (also the lookup key)
	Nonce        string    // random nonce for ID token validation
	PKCEVerifier string    // PKCE code_verifier (never sent to browser)
	ProviderID   string    // which OIDC provider this flow is for
	CreatedAt    time.Time // for TTL-based expiry
}

// OIDCStateStore manages server-side OIDC authorization flow state.
// State is keyed by the random state parameter and consumed (single-use) on callback.
type OIDCStateStore struct {
	states sync.Map // map[stateParam]OIDCFlowState
}

// NewOIDCStateStore creates a new state store.
func NewOIDCStateStore() *OIDCStateStore {
	return &OIDCStateStore{}
}

// Store saves an OIDC flow state entry.
func (s *OIDCStateStore) Store(state OIDCFlowState) {
	s.states.Store(state.State, state)
}

// Consume retrieves and deletes an OIDC flow state entry (single-use).
// Returns an error if the state is not found or has expired.
func (s *OIDCStateStore) Consume(stateParam string) (*OIDCFlowState, error) {
	val, ok := s.states.LoadAndDelete(stateParam)
	if !ok {
		return nil, fmt.Errorf("unknown or expired OIDC state")
	}

	state := val.(OIDCFlowState)
	if time.Since(state.CreatedAt) > OIDCStateTTL {
		return nil, fmt.Errorf("OIDC state expired")
	}

	return &state, nil
}

// StartCleanup runs a background goroutine to evict expired state entries.
func (s *OIDCStateStore) StartCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				s.states.Range(func(key, val any) bool {
					state := val.(OIDCFlowState)
					if now.Sub(state.CreatedAt) > OIDCStateTTL {
						s.states.Delete(key)
					}
					return true
				})
			}
		}
	}()
}
