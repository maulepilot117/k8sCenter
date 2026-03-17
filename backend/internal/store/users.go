package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubecenter/kubecenter/internal/auth"
)

// UserStore handles CRUD for the local_users table.
// Implements auth.UserStore.
type UserStore struct {
	pool *pgxpool.Pool
}

// NewUserStore creates a user store backed by PostgreSQL.
func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// Create inserts a new local user. Returns auth.ErrDuplicateUser on unique violation.
func (s *UserStore) Create(ctx context.Context, u auth.UserRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO local_users (id, username, password_hash, salt, k8s_username, k8s_groups, roles)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.ID, u.Username, u.PasswordHash, u.Salt, u.K8sUsername, u.K8sGroups, u.Roles)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return auth.ErrDuplicateUser
		}
		return fmt.Errorf("creating user: %w", err)
	}
	return nil
}

// CreateFirstUser atomically inserts a user only if no users exist.
// Uses a subquery to ensure database-level atomicity (no Go mutex needed).
// Returns true if the user was created, false if users already exist.
func (s *UserStore) CreateFirstUser(ctx context.Context, u auth.UserRecord) (bool, error) {
	result, err := s.pool.Exec(ctx, `
		INSERT INTO local_users (id, username, password_hash, salt, k8s_username, k8s_groups, roles)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE NOT EXISTS (SELECT 1 FROM local_users)`,
		u.ID, u.Username, u.PasswordHash, u.Salt, u.K8sUsername, u.K8sGroups, u.Roles)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, auth.ErrDuplicateUser
		}
		return false, fmt.Errorf("creating first user: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

// GetByUsername looks up a user by username.
func (s *UserStore) GetByUsername(ctx context.Context, username string) (*auth.UserRecord, error) {
	var u auth.UserRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, salt, k8s_username, k8s_groups, roles
		FROM local_users WHERE username = $1`, username).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Salt, &u.K8sUsername, &u.K8sGroups, &u.Roles)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("getting user by username %s: %w", username, err)
	}
	return &u, nil
}

// GetByID looks up a user by ID.
func (s *UserStore) GetByID(ctx context.Context, id string) (*auth.UserRecord, error) {
	var u auth.UserRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, salt, k8s_username, k8s_groups, roles
		FROM local_users WHERE id = $1`, id).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Salt, &u.K8sUsername, &u.K8sGroups, &u.Roles)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("getting user by ID %s: %w", id, err)
	}
	return &u, nil
}

// Count returns the number of local users.
func (s *UserStore) Count(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM local_users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}
