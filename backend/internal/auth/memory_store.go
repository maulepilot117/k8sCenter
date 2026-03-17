package auth

import (
	"context"
	"sync"
)

// MemoryUserStore is an in-memory UserStore implementation for testing.
type MemoryUserStore struct {
	mu    sync.RWMutex
	users map[string]UserRecord // keyed by username
	byID  map[string]UserRecord // keyed by ID
}

// NewMemoryUserStore creates an empty in-memory user store for tests.
func NewMemoryUserStore() *MemoryUserStore {
	return &MemoryUserStore{
		users: make(map[string]UserRecord),
		byID:  make(map[string]UserRecord),
	}
}

func (m *MemoryUserStore) Create(_ context.Context, u UserRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.users[u.Username]; exists {
		return ErrDuplicateUser
	}
	m.users[u.Username] = u
	m.byID[u.ID] = u
	return nil
}

func (m *MemoryUserStore) CreateFirstUser(_ context.Context, u UserRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) > 0 {
		return false, nil
	}
	m.users[u.Username] = u
	m.byID[u.ID] = u
	return true, nil
}

func (m *MemoryUserStore) GetByUsername(_ context.Context, username string) (*UserRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[username]
	if !ok {
		return nil, ErrUserNotFound
	}
	return &u, nil
}

func (m *MemoryUserStore) GetByID(_ context.Context, id string) (*UserRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return &u, nil
}

func (m *MemoryUserStore) Count(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users), nil
}
