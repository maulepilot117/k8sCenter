package auth

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
)

// memoryUserStore is a simple in-memory UserStore for tests.
type memoryUserStore struct {
	mu    sync.RWMutex
	users map[string]UserRecord // keyed by username
	byID  map[string]UserRecord // keyed by ID
}

func newMemoryUserStore() *memoryUserStore {
	return &memoryUserStore{
		users: make(map[string]UserRecord),
		byID:  make(map[string]UserRecord),
	}
}

func (m *memoryUserStore) Create(_ context.Context, u UserRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.users[u.Username]; exists {
		return ErrDuplicateUser
	}
	m.users[u.Username] = u
	m.byID[u.ID] = u
	return nil
}

func (m *memoryUserStore) CreateFirstUser(_ context.Context, u UserRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) > 0 {
		return false, nil
	}
	if _, exists := m.users[u.Username]; exists {
		return false, ErrDuplicateUser
	}
	m.users[u.Username] = u
	m.byID[u.ID] = u
	return true, nil
}

func (m *memoryUserStore) GetByUsername(_ context.Context, username string) (*UserRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[username]
	if !ok {
		return nil, ErrUserNotFound
	}
	return &u, nil
}

func (m *memoryUserStore) GetByID(_ context.Context, id string) (*UserRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return &u, nil
}

func (m *memoryUserStore) Count(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users), nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testProvider() *LocalProvider {
	return NewLocalProvider(newMemoryUserStore(), testLogger())
}

func TestLocalProvider_CreateAndAuthenticate(t *testing.T) {
	p := testProvider()

	user, err := p.CreateUser(context.Background(), "admin", "password123", []string{"admin"})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("expected username admin, got %s", user.Username)
	}
	if user.KubernetesUsername != "admin" {
		t.Errorf("expected k8s username admin, got %s", user.KubernetesUsername)
	}

	// Authenticate with correct credentials
	authed, err := p.Authenticate(context.Background(), Credentials{
		Username: "admin",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if authed.Username != "admin" {
		t.Errorf("expected username admin, got %s", authed.Username)
	}
	if authed.ID != user.ID {
		t.Errorf("expected same user ID")
	}
}

func TestLocalProvider_WrongPassword(t *testing.T) {
	p := testProvider()
	p.CreateUser(context.Background(), "admin", "password123", []string{"admin"})

	_, err := p.Authenticate(context.Background(), Credentials{
		Username: "admin",
		Password: "wrongpass",
	})
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestLocalProvider_UnknownUser(t *testing.T) {
	p := testProvider()

	_, err := p.Authenticate(context.Background(), Credentials{
		Username: "nobody",
		Password: "password123",
	})
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
}

func TestLocalProvider_DuplicateUser(t *testing.T) {
	p := testProvider()
	p.CreateUser(context.Background(), "admin", "password123", []string{"admin"})

	_, err := p.CreateUser(context.Background(), "admin", "otherpass", []string{"admin"})
	if err == nil {
		t.Fatal("expected error for duplicate user")
	}
}

func TestLocalProvider_UserCount(t *testing.T) {
	p := testProvider()

	if p.UserCount() != 0 {
		t.Errorf("expected 0 users, got %d", p.UserCount())
	}

	p.CreateUser(context.Background(), "user1", "password123", []string{"viewer"})
	if p.UserCount() != 1 {
		t.Errorf("expected 1 user, got %d", p.UserCount())
	}

	p.CreateUser(context.Background(), "user2", "password123", []string{"viewer"})
	if p.UserCount() != 2 {
		t.Errorf("expected 2 users, got %d", p.UserCount())
	}
}

func TestLocalProvider_GetUserByID(t *testing.T) {
	p := testProvider()
	created, _ := p.CreateUser(context.Background(), "admin", "password123", []string{"admin"})

	found, err := p.GetUserByID(created.ID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if found.Username != "admin" {
		t.Errorf("expected username admin, got %s", found.Username)
	}

	_, err = p.GetUserByID("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
}

func TestLocalProvider_Type(t *testing.T) {
	p := testProvider()
	if p.Type() != "local" {
		t.Errorf("expected type 'local', got %s", p.Type())
	}
}

func TestLocalProvider_CreateFirstUser(t *testing.T) {
	p := testProvider()

	user, err := p.CreateFirstUser(context.Background(), "admin", "password123", []string{"admin"})
	if err != nil {
		t.Fatalf("CreateFirstUser failed: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("expected username admin, got %s", user.Username)
	}

	// Second call should fail
	_, err = p.CreateFirstUser(context.Background(), "admin2", "password456", []string{"admin"})
	if err != ErrSetupCompleted {
		t.Errorf("expected ErrSetupCompleted, got %v", err)
	}
}
