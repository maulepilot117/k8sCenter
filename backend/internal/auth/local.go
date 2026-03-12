package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/crypto/argon2"
)

// maxConcurrentHashes limits parallel Argon2id operations to prevent OOM.
// Each hash uses ~64 MB of memory.
const maxConcurrentHashes = 3

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

func (s storedUser) toUser() *User {
	return &User{
		ID:                 s.ID,
		Username:           s.Username,
		Provider:           "local",
		KubernetesUsername: s.KubernetesUsername,
		KubernetesGroups:   s.KubernetesGroups,
		Roles:              s.Roles,
	}
}

// LocalProvider authenticates users against a local account store.
// User data is held in memory and can be persisted externally (e.g., k8s Secret).
type LocalProvider struct {
	mu       sync.RWMutex
	users    map[string]storedUser // keyed by username
	logger   *slog.Logger
	hashSem  chan struct{} // limits concurrent Argon2id operations
}

// NewLocalProvider creates a LocalProvider.
func NewLocalProvider(logger *slog.Logger) *LocalProvider {
	return &LocalProvider{
		users:   make(map[string]storedUser),
		logger:  logger,
		hashSem: make(chan struct{}, maxConcurrentHashes),
	}
}

func (p *LocalProvider) Type() string { return "local" }

// Authenticate validates credentials against the local store.
func (p *LocalProvider) Authenticate(ctx context.Context, creds Credentials) (*User, error) {
	p.mu.RLock()
	stored, ok := p.users[creds.Username]
	p.mu.RUnlock()

	// Acquire hash semaphore to limit concurrent Argon2id operations (each uses ~64 MB).
	select {
	case p.hashSem <- struct{}{}:
		defer func() { <-p.hashSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

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
	storedHash, err := hex.DecodeString(stored.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if subtle.ConstantTimeCompare(hash, storedHash) != 1 {
		return nil, fmt.Errorf("invalid credentials")
	}

	return stored.toUser(), nil
}

// CreateFirstUser atomically creates the first user only if no users exist.
// This prevents the TOCTOU race where two concurrent setup requests could
// both pass a UserCount() == 0 check and create two admin accounts.
func (p *LocalProvider) CreateFirstUser(username, password string, roles []string) (*User, error) {
	return p.createUser(username, password, roles, true)
}

// CreateUser adds a new local user with Argon2id-hashed password.
func (p *LocalProvider) CreateUser(username, password string, roles []string) (*User, error) {
	return p.createUser(username, password, roles, false)
}

func (p *LocalProvider) createUser(username, password string, roles []string, requireEmpty bool) (*User, error) {
	// Acquire hash semaphore before the expensive Argon2id operation.
	p.hashSem <- struct{}{}
	defer func() { <-p.hashSem }()

	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	id, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("generating user ID: %w", err)
	}

	// Lock after hashing to minimize lock hold time. The atomic check happens here.
	p.mu.Lock()
	defer p.mu.Unlock()

	if requireEmpty && len(p.users) > 0 {
		return nil, fmt.Errorf("setup already completed")
	}

	if _, exists := p.users[username]; exists {
		return nil, fmt.Errorf("user %q already exists", username)
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

	return stored.toUser(), nil
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
			return stored.toUser(), nil
		}
	}
	return nil, fmt.Errorf("user not found: %s", id)
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
