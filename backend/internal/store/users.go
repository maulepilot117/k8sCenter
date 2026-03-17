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
		INSERT INTO local_users (id, username, password_phc, k8s_username, k8s_groups, roles)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, u.Username, u.PasswordPHC, u.K8sUsername, u.K8sGroups, u.Roles)
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
// Uses a PostgreSQL advisory lock to prevent concurrent setup requests from
// both succeeding (INSERT ... WHERE NOT EXISTS is not atomic under READ COMMITTED).
// Returns true if the user was created, false if users already exist.
func (s *UserStore) CreateFirstUser(ctx context.Context, u auth.UserRecord) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Advisory lock ensures only one CreateFirstUser can run at a time.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('create_first_user'))`); err != nil {
		return false, fmt.Errorf("acquiring advisory lock: %w", err)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM local_users`).Scan(&count); err != nil {
		return false, fmt.Errorf("counting users: %w", err)
	}
	if count > 0 {
		return false, nil
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO local_users (id, username, password_phc, k8s_username, k8s_groups, roles)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, u.Username, u.PasswordPHC, u.K8sUsername, u.K8sGroups, u.Roles)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, auth.ErrDuplicateUser
		}
		return false, fmt.Errorf("inserting first user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("committing first user: %w", err)
	}
	return true, nil
}

// GetByUsername looks up a user by username.
func (s *UserStore) GetByUsername(ctx context.Context, username string) (*auth.UserRecord, error) {
	var u auth.UserRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_phc, k8s_username, k8s_groups, roles
		FROM local_users WHERE username = $1`, username).Scan(
		&u.ID, &u.Username, &u.PasswordPHC, &u.K8sUsername, &u.K8sGroups, &u.Roles)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("getting user by username: %w", err)
	}
	return &u, nil
}

// GetByID looks up a user by ID.
func (s *UserStore) GetByID(ctx context.Context, id string) (*auth.UserRecord, error) {
	var u auth.UserRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_phc, k8s_username, k8s_groups, roles
		FROM local_users WHERE id = $1`, id).Scan(
		&u.ID, &u.Username, &u.PasswordPHC, &u.K8sUsername, &u.K8sGroups, &u.Roles)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("getting user by ID: %w", err)
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
