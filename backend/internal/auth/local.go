package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

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

// UserStore is the interface for persistent user storage.
// Implemented by store.UserStore (PostgreSQL).
type UserStore interface {
	Create(ctx context.Context, u UserRecord) error
	CreateFirstUser(ctx context.Context, u UserRecord) (bool, error)
	GetByUsername(ctx context.Context, username string) (*UserRecord, error)
	GetByID(ctx context.Context, id string) (*UserRecord, error)
	Count(ctx context.Context) (int, error)
	List(ctx context.Context) ([]UserRecord, error)
	Delete(ctx context.Context, id string) error
	UpdatePassword(ctx context.Context, id, passwordPHC string) error
}

// UserRecord is the database representation of a local user.
// PasswordPHC stores the password in PHC format: $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
type UserRecord struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	PasswordPHC string   `json:"-"`
	K8sUsername string   `json:"k8sUsername"`
	K8sGroups   []string `json:"k8sGroups"`
	Roles       []string `json:"roles"`
}

func (r *UserRecord) toUser() *User {
	return &User{
		ID:                 r.ID,
		Username:           r.Username,
		Provider:           "local",
		KubernetesUsername: r.K8sUsername,
		KubernetesGroups:   r.K8sGroups,
		Roles:              r.Roles,
	}
}

// LocalProvider authenticates users against a PostgreSQL-backed local account store.
type LocalProvider struct {
	store   UserStore
	logger  *slog.Logger
	hashSem chan struct{} // limits concurrent Argon2id operations
}

// NewLocalProvider creates a LocalProvider backed by the given UserStore.
func NewLocalProvider(store UserStore, logger *slog.Logger) *LocalProvider {
	return &LocalProvider{
		store:   store,
		logger:  logger,
		hashSem: make(chan struct{}, maxConcurrentHashes),
	}
}

func (p *LocalProvider) Type() string { return "local" }

// Store returns the underlying UserStore for direct List/Delete access.
func (p *LocalProvider) Store() UserStore { return p.store }

// UpdatePassword validates, hashes, and stores a new password for the given user.
// Password validation (8-128 chars) is enforced here as the single source of truth.
func (p *LocalProvider) UpdatePassword(ctx context.Context, id, newPassword string) error {
	if len(newPassword) < 8 || len(newPassword) > 128 {
		return ErrPasswordInvalid
	}

	// Acquire hash semaphore with context cancellation support.
	select {
	case p.hashSem <- struct{}{}:
		defer func() { <-p.hashSem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generating salt: %w", err)
	}
	hash := argon2.IDKey([]byte(newPassword), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	phc := encodePHC(salt, hash)

	return p.store.UpdatePassword(ctx, id, phc)
}

// Authenticate validates credentials against the database.
func (p *LocalProvider) Authenticate(ctx context.Context, creds Credentials) (*User, error) {
	stored, err := p.store.GetByUsername(ctx, creds.Username)

	// Acquire hash semaphore to limit concurrent Argon2id operations (each uses ~64 MB).
	select {
	case p.hashSem <- struct{}{}:
		defer func() { <-p.hashSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if err != nil {
		// Constant-time: hash the password anyway to prevent timing attacks
		dummySalt := make([]byte, argon2SaltLen)
		argon2.IDKey([]byte(creds.Password), dummySalt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
		return nil, ErrInvalidCredentials
	}

	salt, storedHash, err := parsePHC(stored.PasswordPHC)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	hash := argon2.IDKey([]byte(creds.Password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	if subtle.ConstantTimeCompare(hash, storedHash) != 1 {
		return nil, ErrInvalidCredentials
	}

	return stored.toUser(), nil
}

// CreateFirstUser atomically creates the first user only if no users exist.
// Atomicity is enforced at the database level (no Go mutex needed).
func (p *LocalProvider) CreateFirstUser(ctx context.Context, username, password string, roles []string) (*User, error) {
	record, err := p.hashAndBuild(ctx, username, password, roles)
	if err != nil {
		return nil, err
	}

	created, err := p.store.CreateFirstUser(ctx, *record)
	if err != nil {
		return nil, fmt.Errorf("creating first user: %w", err)
	}
	if !created {
		return nil, ErrSetupCompleted
	}

	p.logger.Info("first local user created", "username", username, "roles", roles)
	return record.toUser(), nil
}

// CreateUser adds a new local user with Argon2id-hashed password.
func (p *LocalProvider) CreateUser(ctx context.Context, username, password string, roles []string) (*User, error) {
	record, err := p.hashAndBuild(ctx, username, password, roles)
	if err != nil {
		return nil, err
	}

	if err := p.store.Create(ctx, *record); err != nil {
		if errors.Is(err, ErrDuplicateUser) {
			return nil, ErrDuplicateUser
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}

	p.logger.Info("local user created", "username", username, "roles", roles)
	return record.toUser(), nil
}

// UserCount returns the number of local users.
func (p *LocalProvider) UserCount() int {
	count, err := p.store.Count(context.Background())
	if err != nil {
		p.logger.Error("failed to count users", "error", err)
		return 0
	}
	return count
}

// GetUserByID looks up a user by their ID.
func (p *LocalProvider) GetUserByID(id string) (*User, error) {
	record, err := p.store.GetByID(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUserNotFound, id)
	}
	return record.toUser(), nil
}

// hashAndBuild hashes the password and builds a UserRecord.
func (p *LocalProvider) hashAndBuild(ctx context.Context, username, password string, roles []string) (*UserRecord, error) {
	// Acquire hash semaphore with context cancellation support.
	select {
	case p.hashSem <- struct{}{}:
		defer func() { <-p.hashSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
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

	return &UserRecord{
		ID:          id,
		Username:    username,
		PasswordPHC: encodePHC(salt, hash),
		K8sUsername: username,
		K8sGroups:   []string{"k8scenter:users"},
		Roles:       roles,
	}, nil
}

// encodePHC encodes salt and hash into PHC format: $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
func encodePHC(salt, hash []byte) string {
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2Memory, argon2Time, argon2Threads, b64Salt, b64Hash)
}

// parsePHC extracts salt and hash from a PHC-format string.
func parsePHC(phc string) (salt, hash []byte, err error) {
	var version int
	var memory, time, threads uint32
	var b64Salt, b64Hash string

	_, err = fmt.Sscanf(phc, "$argon2id$v=%d$m=%d,t=%d,p=%d$%s",
		&version, &memory, &time, &threads, &b64Salt)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid PHC format: %w", err)
	}

	// Split the last segment — Sscanf captures everything after the 4th $
	// The format is <salt>$<hash>, so split on $
	parts := splitLast(b64Salt, '$')
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("invalid PHC format: missing hash segment")
	}
	b64Salt = parts[0]
	b64Hash = parts[1]

	salt, err = base64.RawStdEncoding.DecodeString(b64Salt)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding salt: %w", err)
	}
	hash, err = base64.RawStdEncoding.DecodeString(b64Hash)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding hash: %w", err)
	}
	return salt, hash, nil
}

// splitLast splits s into two parts at the last occurrence of sep.
func splitLast(s string, sep byte) []string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
