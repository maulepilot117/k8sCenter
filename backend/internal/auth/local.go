package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters following OWASP recommendations.
const (
	argon2Time    = 1
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4
	argon2KeyLen  = 32
	argon2SaltLen = 16
)

// storedUser is the internal representation persisted in the user store.
type storedUser struct {
	ID                 string   `json:"id"`
	Username           string   `json:"username"`
	PasswordHash       string   `json:"passwordHash"`
	Salt               string   `json:"salt"`
	KubernetesUsername string   `json:"kubernetesUsername"`
	KubernetesGroups   []string `json:"kubernetesGroups"`
	Roles              []string `json:"roles"`
}

// LocalProvider authenticates users against a local account store.
// User data is held in memory and can be persisted externally (e.g., k8s Secret).
type LocalProvider struct {
	mu     sync.RWMutex
	users  map[string]storedUser // keyed by username
	logger *slog.Logger
}

// NewLocalProvider creates a LocalProvider.
func NewLocalProvider(logger *slog.Logger) *LocalProvider {
	return &LocalProvider{
		users:  make(map[string]storedUser),
		logger: logger,
	}
}

func (p *LocalProvider) Type() string { return "local" }

// Authenticate validates credentials against the local store.
func (p *LocalProvider) Authenticate(_ context.Context, creds Credentials) (*User, error) {
	p.mu.RLock()
	stored, ok := p.users[creds.Username]
	p.mu.RUnlock()

	if !ok {
		// Constant-time: hash the password anyway to prevent timing attacks
		dummySalt := make([]byte, argon2SaltLen)
		argon2.IDKey([]byte(creds.Password), dummySalt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
		return nil, fmt.Errorf("invalid credentials")
	}

	salt, err := hex.DecodeString(stored.Salt)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	hash := argon2.IDKey([]byte(creds.Password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	if hex.EncodeToString(hash) != stored.PasswordHash {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &User{
		ID:                 stored.ID,
		Username:           stored.Username,
		KubernetesUsername: stored.KubernetesUsername,
		KubernetesGroups:   stored.KubernetesGroups,
		Roles:              stored.Roles,
	}, nil
}

// CreateUser adds a new local user with Argon2id-hashed password.
func (p *LocalProvider) CreateUser(username, password string, roles []string) (*User, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.users[username]; exists {
		return nil, fmt.Errorf("user %q already exists", username)
	}

	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	id, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("generating user ID: %w", err)
	}

	stored := storedUser{
		ID:                 id,
		Username:           username,
		PasswordHash:       hex.EncodeToString(hash),
		Salt:               hex.EncodeToString(salt),
		KubernetesUsername: username,
		KubernetesGroups:   []string{"kubecenter:users"},
		Roles:              roles,
	}

	p.users[username] = stored

	p.logger.Info("local user created", "username", username, "roles", roles)

	return &User{
		ID:                 stored.ID,
		Username:           stored.Username,
		KubernetesUsername: stored.KubernetesUsername,
		KubernetesGroups:   stored.KubernetesGroups,
		Roles:              stored.Roles,
	}, nil
}

// UserCount returns the number of local users.
func (p *LocalProvider) UserCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.users)
}

// GetUserByID looks up a user by their ID.
func (p *LocalProvider) GetUserByID(id string) (*User, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, stored := range p.users {
		if stored.ID == id {
			return &User{
				ID:                 stored.ID,
				Username:           stored.Username,
				KubernetesUsername: stored.KubernetesUsername,
				KubernetesGroups:   stored.KubernetesGroups,
				Roles:              stored.Roles,
			}, nil
		}
	}
	return nil, fmt.Errorf("user not found: %s", id)
}

// ExportUsers serializes the user store to JSON for persistence.
func (p *LocalProvider) ExportUsers() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	users := make([]storedUser, 0, len(p.users))
	for _, u := range p.users {
		users = append(users, u)
	}
	return json.Marshal(users)
}

// ImportUsers loads users from JSON into the store.
func (p *LocalProvider) ImportUsers(data []byte) error {
	var users []storedUser
	if err := json.Unmarshal(data, &users); err != nil {
		return fmt.Errorf("unmarshaling users: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, u := range users {
		p.users[u.Username] = u
	}

	p.logger.Info("imported local users", "count", len(users))
	return nil
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
